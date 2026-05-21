package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// --- helpers ------------------------------------------------------------

// writeOpenAISSE writes one OpenAI-shaped SSE chunk to w and flushes.
// Mirrors the helper in plugins/cloud/internal/csllm/csllm_test.go so
// the integration suite produces wire-identical payloads to the unit
// suite (and, in turn, the real Cloudflare Workers AI gateway).
func writeOpenAISSE(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		t.Fatalf("writeOpenAISSE: %v", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// streamingOpenAIHandler returns an http.HandlerFunc that emits a
// canonical OpenAI chat-completions stream: three delta chunks
// ("hello ", "world", "!"), one finish-reason chunk, and the [DONE]
// terminator.
func streamingOpenAIHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-test","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"hello "}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"world"}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"!"}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		writeOpenAISSE(t, w, "[DONE]")
	}
}

// --- tests --------------------------------------------------------------

func TestVersionFlag(t *testing.T) {
	s := run(t, runOpts{
		args: []string{"--version"},
	})
	s.assertExit(0)
	s.assertStdoutContains("aish-inference-cloud")
}

func TestMissingAPIKeyFailsFast(t *testing.T) {
	s := run(t, runOpts{
		stdin: "",
		env:   envWithoutKey(),
	})
	s.assertExitNonZero()
	// The diagnostic must name the *new* canonical env var. We accept
	// either form so the back-compat banner does not break this test
	// for the v0.2 deprecation window.
	if !strings.Contains(s.stderr, "CS_API_KEY") && !strings.Contains(s.stderr, "ANTHROPIC_API_KEY") {
		t.Fatalf("stderr does not name an API-key env var (CS_API_KEY or ANTHROPIC_API_KEY):\n%s", s.stderr)
	}
	// The diagnostic MUST NOT echo the env var's value. Since the var
	// was unset for this run, the strongest assertion is the absence
	// of a recognisable key prefix.
	s.assertStderrDoesNotContain("cs_")
	s.assertStderrDoesNotContain("sk-")
}

func TestPingReturnsPong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// Ping never reaches the upstream; this handler is here only
		// because we still pass --api-url for uniformity.
	}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "ping-1",
		Method:  proto.MethodPing,
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)
	resps := readResponses(t, s.stdout)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(resps), resps)
	}
	r := resps[0]
	if r.ID != "ping-1" {
		t.Errorf("response.ID=%q, want %q", r.ID, "ping-1")
	}
	if r.Result == nil {
		t.Fatalf("expected Result, got Error: %+v", r.Error)
	}
	if r.Result.Type != proto.KindPong {
		t.Errorf("Result.Type=%q, want %q", r.Result.Type, proto.KindPong)
	}
}

func TestInferStreamsTokensThenComplete(t *testing.T) {
	srv := httptest.NewServer(streamingOpenAIHandler(t))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "infer-stream-1",
		Method:  proto.MethodInfer,
		Params: proto.InferParams{
			Intent: "say hello",
			Stream: true,
		},
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	resps := readResponses(t, s.stdout)
	if len(resps) < 2 {
		t.Fatalf("want >= 2 responses (tokens + complete), got %d: %+v", len(resps), resps)
	}

	// Every response MUST carry the originating ID.
	for i, r := range resps {
		if r.ID != "infer-stream-1" {
			t.Errorf("resps[%d].ID=%q, want %q", i, r.ID, "infer-stream-1")
		}
		if r.Result == nil {
			t.Fatalf("resps[%d] has no Result (Error=%+v)", i, r.Error)
		}
	}

	// All but the last frame MUST be tokens; the last MUST be Complete.
	var tokenCount int
	for i, r := range resps[:len(resps)-1] {
		if r.Result.Type != proto.KindToken {
			t.Errorf("resps[%d].Result.Type=%q, want %q", i, r.Result.Type, proto.KindToken)
		}
		tokenCount++
	}
	last := resps[len(resps)-1]
	if last.Result.Type != proto.KindComplete {
		t.Errorf("final frame Type=%q, want %q", last.Result.Type, proto.KindComplete)
	}
	if tokenCount == 0 {
		t.Errorf("expected at least one token frame; got %d", tokenCount)
	}
	if last.Result.Invocation == "" {
		t.Errorf("Complete frame Invocation is empty; want assembled tokens")
	}
}

func TestInferSendsBearerAuthAndPostsToChatCompletions(t *testing.T) {
	// Capture the request the plugin makes so we can assert wire shape
	// end-to-end: path, Authorization header, body model.
	type captured struct {
		path  string
		auth  string
		xapi  string
		body  map[string]any
		xforw string
	}
	gotCh := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		select {
		case gotCh <- captured{
			path: r.URL.Path,
			auth: r.Header.Get("Authorization"),
			xapi: r.Header.Get("x-api-key"),
			body: m,
		}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, "[DONE]")
	}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "wire-1",
		Method:  proto.MethodInfer,
		Params:  proto.InferParams{Intent: "probe"},
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	got := <-gotCh
	if !strings.HasSuffix(got.path, "/chat/completions") {
		t.Errorf("request path = %q; want suffix /chat/completions", got.path)
	}
	if strings.Contains(got.path, "/messages") {
		t.Errorf("request path = %q; must not contain legacy /messages", got.path)
	}
	if got.auth != "Bearer "+fakeAPIKey {
		t.Errorf("Authorization header = %q; want %q", got.auth, "Bearer "+fakeAPIKey)
	}
	if got.xapi != "" {
		t.Errorf("x-api-key header present (%q); want absent", got.xapi)
	}
	if msgs, ok := got.body["messages"].([]any); !ok || len(msgs) == 0 {
		t.Errorf("request body.messages missing or empty (body=%+v)", got.body)
	}
}

func TestInfer401ReturnsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "auth-1",
		Method:  proto.MethodInfer,
		Params:  proto.InferParams{Intent: "x"},
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	resps := readResponses(t, s.stdout)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(resps), resps)
	}
	r := resps[0]
	if r.Error == nil {
		t.Fatalf("expected Error, got Result=%+v", r.Result)
	}
	if r.Error.Code != proto.CodeAuthFailed {
		t.Errorf("Error.Code=%d, want %d (CodeAuthFailed)", r.Error.Code, proto.CodeAuthFailed)
	}
	// Critical: the API-key value MUST NOT appear in the error
	// message. The httptest server never sees the key on a successful
	// path, but a misbehaving server could echo Authorization headers,
	// and our redaction must hold up either way.
	if strings.Contains(r.Error.Message, fakeAPIKey) {
		t.Errorf("Error.Message leaks API key value: %q", r.Error.Message)
	}
	s.assertStdoutDoesNotContain(fakeAPIKey)
	s.assertStderrDoesNotContain(fakeAPIKey)
}

func TestInfer429ReturnsRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "rate-1",
		Method:  proto.MethodInfer,
		Params:  proto.InferParams{Intent: "x"},
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	resps := readResponses(t, s.stdout)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(resps), resps)
	}
	r := resps[0]
	if r.Error == nil {
		t.Fatalf("expected Error, got Result=%+v", r.Result)
	}
	if r.Error.Code != proto.CodeRateLimited {
		t.Errorf("Error.Code=%d, want %d (CodeRateLimited)", r.Error.Code, proto.CodeRateLimited)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "unknown-1",
		Method:  "no-such-method-do-not-implement",
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	resps := readResponses(t, s.stdout)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(resps), resps)
	}
	r := resps[0]
	if r.Error == nil {
		t.Fatalf("expected Error, got Result=%+v", r.Result)
	}
	if r.Error.Code != proto.CodeMethodNotFound {
		t.Errorf("Error.Code=%d, want %d (CodeMethodNotFound)", r.Error.Code, proto.CodeMethodNotFound)
	}
	if r.ID != "unknown-1" {
		t.Errorf("response.ID=%q, want %q", r.ID, "unknown-1")
	}
}

// TestEmbedMethodReturnsNotImplementedError exercises MethodEmbed
// end-to-end through the plugin: the post-#178 gateway has no
// /embeddings endpoint, so the plugin MUST surface a typed
// proto.CodeNotImplemented JSON-RPC error without making a request.
// The httptest server here fails the test if it is hit at all —
// proving the short-circuit.
func TestEmbedMethodReturnsNotImplementedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request reached gateway stub: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "embed-e2e-1",
		Method:  proto.MethodEmbed,
		Params: proto.InferParams{
			Intent: "list files",
		},
	})

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	resps := readResponses(t, s.stdout)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(resps), resps)
	}
	r := resps[0]
	if r.Error == nil {
		t.Fatalf("expected Error, got Result=%+v", r.Result)
	}
	if r.Error.Code != proto.CodeNotImplemented {
		t.Errorf("Error.Code=%d, want %d (CodeNotImplemented)", r.Error.Code, proto.CodeNotImplemented)
	}
	// And the message names the failure mode without leaking the API
	// key (defence in depth — the plugin should never send a request,
	// but the redactor must still cover any error string).
	if strings.Contains(r.Error.Message, fakeAPIKey) {
		t.Errorf("Error.Message leaks API key value: %q", r.Error.Message)
	}
}

func TestMalformedJSONContinuesREPL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	// First line: malformed JSON.
	// Second line: valid ping. The dispatcher MUST emit a parse-error
	// response for the first and a pong for the second.
	pingReq := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "ping-after-bad",
		Method:  proto.MethodPing,
	})
	stdin := "{not valid json\n" + pingReq

	s := run(t, runOpts{
		stdin: stdin,
		env:   envWithKey(),
		args:  []string{"--api-url", srv.URL},
	})
	s.assertExit(0)

	resps := readResponses(t, s.stdout)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses (parse-error + pong), got %d: %+v", len(resps), resps)
	}

	// First response: parse error.
	first := resps[0]
	if first.Error == nil {
		t.Fatalf("first response: want Error, got Result=%+v", first.Result)
	}
	if first.Error.Code != proto.CodeParseError {
		t.Errorf("first.Error.Code=%d, want %d (CodeParseError)", first.Error.Code, proto.CodeParseError)
	}

	// Second response: pong.
	second := resps[1]
	if second.Result == nil {
		t.Fatalf("second response: want Result, got Error=%+v", second.Error)
	}
	if second.Result.Type != proto.KindPong {
		t.Errorf("second.Result.Type=%q, want %q", second.Result.Type, proto.KindPong)
	}
	if second.ID != "ping-after-bad" {
		t.Errorf("second.ID=%q, want %q", second.ID, "ping-after-bad")
	}
}
