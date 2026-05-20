package integration

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// --- helpers ------------------------------------------------------------

// writeSSE writes one Anthropic SSE block to w and flushes. Mirrors the
// helper in plugins/cloud/internal/anthropic/anthropic_test.go so the
// integration suite produces wire-identical payloads.
func writeSSE(t *testing.T, w http.ResponseWriter, event, data string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		t.Fatalf("writeSSE: %v", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// streamingAnthropicHandler returns an http.HandlerFunc that emits a
// canonical Anthropic message stream: message_start, three
// content_block_delta events (tokens "hello ", "world", "!"),
// message_delta (usage), message_stop.
func streamingAnthropicHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(t, w, "message_start", `{"type":"message_start","message":{"id":"msg_test","model":"claude-opus-4-7","usage":{"input_tokens":3,"output_tokens":0}}}`)
		writeSSE(t, w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}`)
		writeSSE(t, w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}`)
		writeSSE(t, w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"!"}}`)
		writeSSE(t, w, "message_delta", `{"type":"message_delta","usage":{"input_tokens":3,"output_tokens":7}}`)
		writeSSE(t, w, "message_stop", `{"type":"message_stop"}`)
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
	s.assertStderrContains("ANTHROPIC_API_KEY")
	// The diagnostic MUST NOT echo the env var's value. Since the
	// var was unset for this run, the strongest assertion is the
	// absence of a recognisable key prefix.
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
	srv := httptest.NewServer(streamingAnthropicHandler(t))
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

	// Use a method name the plugin does not register. (MethodEmbed
	// previously played this role, but it's now wired in by v0.1-2-followup;
	// see TestEmbedMethodReturnsVector below.)
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

// TestEmbedMethodReturnsVector exercises MethodEmbed end-to-end through
// the plugin: stub the gateway's /embeddings endpoint, send a JSON-RPC
// embed request via the plugin's stdin, and assert the plugin emits one
// Embedding frame carrying the vector + cost telemetry.
func TestEmbedMethodReturnsVector(t *testing.T) {
	wantVector := []float64{0.1, -0.2, 0.3, 0.4, 0.5}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			t.Errorf("unexpected path %q; want suffix /embeddings", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": wantVector, "index": 0}},
			"model": "voyage-3",
			"usage": map[string]any{"prompt_tokens": 4, "total_tokens": 4},
		})
	}))
	defer srv.Close()

	stdin := marshalRequest(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "embed-e2e-1",
		Method:  proto.MethodEmbed,
		Params: proto.InferParams{
			Intent: "list files",
			Model:  "voyage-3",
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
	if r.Error != nil {
		t.Fatalf("unexpected Error=%+v", r.Error)
	}
	if r.Result == nil {
		t.Fatalf("expected Result, got nil")
	}
	if r.Result.Type != proto.KindEmbedding {
		t.Errorf("Result.Type=%q, want %q", r.Result.Type, proto.KindEmbedding)
	}
	if len(r.Result.Vector) != len(wantVector) {
		t.Fatalf("Vector len=%d, want %d", len(r.Result.Vector), len(wantVector))
	}
	// Tolerance compare — float64 JSON values narrowed to float32 by the
	// anthropic client introduce ~1e-7 representation noise.
	const tol = 1e-6
	for i, v := range wantVector {
		if d := math.Abs(float64(r.Result.Vector[i]) - v); d > tol {
			t.Errorf("Vector[%d]=%v, want %v (delta %v > %v)", i, r.Result.Vector[i], v, d, tol)
		}
	}
	if r.Result.Cost == nil {
		t.Fatal("Cost is nil; want populated")
	}
	if r.Result.Cost.Model == "" {
		t.Errorf("Cost.Model empty; want a model id distinct from the infer model")
	}
	if r.ID != "embed-e2e-1" {
		t.Errorf("response.ID=%q, want %q", r.ID, "embed-e2e-1")
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
