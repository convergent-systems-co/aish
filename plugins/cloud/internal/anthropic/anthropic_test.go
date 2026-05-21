package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// The literal API-token used in tests. Mirrors the cs_<random> shape the
// real auth-proxy issues (per core-infra/README.md §"Obtaining a token");
// the redaction tests assert this string never appears in any returned
// String(), Error(), or human-visible field.
const fakeAPIKey = "cs_test_AAAA_BBBB_CCCC_DDDD"

// --- helpers ------------------------------------------------------------

// newServerWithHandler stands up an httptest.Server that runs h on every
// request. The server is closed via t.Cleanup.
func newServerWithHandler(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// writeOpenAISSE writes "data: <json>\n\n" to w and flushes. This is the
// wire shape of the gateway's stream (workers-ai/src/worker.js issues
// "stream":true and the AI binding emits OpenAI-shaped chunks).
func writeOpenAISSE(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	if _, err := w.Write([]byte("data: " + data + "\n\n")); err != nil {
		t.Fatalf("writeOpenAISSE: %v", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// drainFrames pulls every frame off ch until it closes, returning them
// in order. Fails the test if ch does not close within deadline.
func drainFrames(t *testing.T, ch <-chan proto.Frame, deadline time.Duration) []proto.Frame {
	t.Helper()
	frames := []proto.Frame{}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return frames
			}
			frames = append(frames, f)
		case <-timer.C:
			t.Fatalf("frame channel did not close within %v (got %d frames so far)", deadline, len(frames))
			return frames
		}
	}
}

// codedFromErr asserts err is a *CodedError and returns it. Fails the
// test if the error is nil or the wrong shape.
func codedFromErr(t *testing.T, err error) *CodedError {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce *CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CodedError, got %T: %v", err, err)
	}
	return ce
}

// --- NewClient ----------------------------------------------------------

func TestNewClient_Success(t *testing.T) {
	c, err := NewClient(fakeAPIKey, "https://example.invalid", http.DefaultClient)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client without error")
	}
}

func TestNewClient_EmptyAPIKey_ReturnsError(t *testing.T) {
	c, err := NewClient("", "https://example.invalid", http.DefaultClient)
	if err == nil {
		t.Fatal("expected error for empty apiKey, got nil")
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("expected errors.Is(err, ErrMissingAPIKey), got %v", err)
	}
	if c != nil {
		t.Errorf("expected nil client when apiKey empty, got %+v", c)
	}
}

// --- String/Stringer redaction -----------------------------------------

func TestClient_String_DoesNotLeakAPIKey(t *testing.T) {
	c, err := NewClient(fakeAPIKey, "https://example.invalid", http.DefaultClient)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got := c.String()
	if strings.Contains(got, fakeAPIKey) {
		t.Errorf("Client.String() leaks API key value: %q", got)
	}
	// Also verify it does not contain the obvious "cs_" prefix of the
	// fake key — the tester scans for that.
	if strings.Contains(got, "cs_test_") {
		t.Errorf("Client.String() leaks API-key prefix: %q", got)
	}
}

// --- Infer: path is /chat/completions (NOT /messages) ------------------

func TestInfer_PostsToChatCompletionsPath(t *testing.T) {
	var seenPath atomic.Value
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "probe"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)

	got, _ := seenPath.Load().(string)
	if !strings.HasSuffix(got, "/chat/completions") {
		t.Errorf("path = %q; want suffix /chat/completions", got)
	}
	if strings.Contains(got, "/messages") {
		t.Errorf("path = %q; must not contain legacy /messages segment", got)
	}
}

// --- Infer: Authorization: Bearer header carries the token -------------

func TestInfer_SendsBearerAuth(t *testing.T) {
	var seenAuth atomic.Value
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		seenAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "probe"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)

	auth, _ := seenAuth.Load().(string)
	want := "Bearer " + fakeAPIKey
	if auth != want {
		t.Errorf("Authorization = %q; want %q", auth, want)
	}
}

// --- Infer: legacy x-api-key + anthropic-version headers are absent ----

func TestInfer_OmitsLegacyHeaders(t *testing.T) {
	var seenXAPI atomic.Value
	var seenVersion atomic.Value
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		seenXAPI.Store(r.Header.Get("x-api-key"))
		seenVersion.Store(r.Header.Get("anthropic-version"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "probe"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)

	if v, _ := seenXAPI.Load().(string); v != "" {
		t.Errorf("x-api-key header present (%q); must be omitted for CS gateway", v)
	}
	if v, _ := seenVersion.Load().(string); v != "" {
		t.Errorf("anthropic-version header present (%q); must be omitted for CS gateway", v)
	}
}

// --- Infer: body is OpenAI chat-completions shape ----------------------

func TestInfer_BodyIsOpenAIShape(t *testing.T) {
	var captured atomic.Value // map[string]any
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("body unmarshal: %v (raw=%s)", err, string(body))
		}
		captured.Store(m)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{
		Intent: "list files",
		Stream: true,
		Model:  "@cf/meta/llama-3.1-8b-instruct",
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)

	m, _ := captured.Load().(map[string]any)
	if m == nil {
		t.Fatal("no body captured")
	}
	if got, _ := m["model"].(string); got != "@cf/meta/llama-3.1-8b-instruct" {
		t.Errorf("body.model = %q; want %q", got, "@cf/meta/llama-3.1-8b-instruct")
	}
	if got, _ := m["stream"].(bool); !got {
		t.Errorf("body.stream = %v; want true", m["stream"])
	}
	msgs, ok := m["messages"].([]any)
	if !ok {
		t.Fatalf("body.messages = %T; want []any (raw=%+v)", m["messages"], m)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(body.messages) = %d; want 1 (only the user turn)", len(msgs))
	}
	first, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatalf("body.messages[0] = %T; want map[string]any", msgs[0])
	}
	if first["role"] != "user" {
		t.Errorf("body.messages[0].role = %v; want \"user\"", first["role"])
	}
	if first["content"] != "list files" {
		t.Errorf("body.messages[0].content = %v; want %q", first["content"], "list files")
	}
	// The legacy "max_tokens" field MAY still be present (the worker
	// accepts it), but the legacy Anthropic top-level requirement is
	// gone — verify the OpenAI top-level fields are present and the
	// Anthropic-only "max_tokens" is no longer wrapped under an extra
	// key. (No assertion required either way; documented for clarity.)
}

// --- Infer: default model is Workers-AI llama-3.1-8b-instruct ----------

func TestInfer_DefaultsToWorkersAILlamaModel(t *testing.T) {
	var captured atomic.Value
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		captured.Store(m)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "x"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)

	m, _ := captured.Load().(map[string]any)
	if got, _ := m["model"].(string); got != "@cf/meta/llama-3.1-8b-instruct" {
		t.Errorf("default model = %q; want %q", got, "@cf/meta/llama-3.1-8b-instruct")
	}
}

// --- Infer: SSE OpenAI-delta stream → tokens + Complete ---------------

func TestInfer_StreamsOpenAIDeltasThenComplete(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"ls"}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" -la"}}]}`)
		writeOpenAISSE(t, w, `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{
		Intent: "list files long format",
		Stream: true,
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	frames := drainFrames(t, ch, 5*time.Second)
	if len(frames) < 2 {
		t.Fatalf("expected >= 2 frames (>=1 token + complete), got %d", len(frames))
	}
	last := frames[len(frames)-1]
	if last.Type != proto.KindComplete {
		t.Errorf("final frame Type = %q; want %q", last.Type, proto.KindComplete)
	}
	if last.Invocation == "" {
		t.Error("Complete.Invocation is empty; want assembled deltas")
	}
	// The empty-content and finish-reason-only chunks must NOT emit
	// Token frames; only the two non-empty content deltas do.
	tokens := 0
	tokenText := ""
	for _, f := range frames[:len(frames)-1] {
		if f.Type != proto.KindToken {
			t.Errorf("non-final frame %q; want token", f.Type)
		}
		tokens++
		tokenText += f.Data
	}
	if tokens != 2 {
		t.Errorf("token frame count = %d; want exactly 2 (empty + finish-reason chunks must be filtered)", tokens)
	}
	if want := "ls -la"; tokenText != want {
		t.Errorf("assembled tokens = %q; want %q", tokenText, want)
	}
	if !strings.Contains(last.Invocation, "ls -la") {
		t.Errorf("Complete.Invocation = %q; expected to contain assembled tokens %q", last.Invocation, "ls -la")
	}
}

// --- Infer: [DONE] terminates even without a prior finish_reason chunk -

func TestInfer_DoneTerminatorTerminatesStream(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, `{"choices":[{"delta":{"content":"x"}}]}`)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "x"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	frames := drainFrames(t, ch, 5*time.Second)
	if len(frames) < 1 {
		t.Fatal("expected at least one frame")
	}
	last := frames[len(frames)-1]
	if last.Type != proto.KindComplete {
		t.Errorf("final frame Type = %q; want %q", last.Type, proto.KindComplete)
	}
}

// --- Infer: channel closes after Complete frame ------------------------

func TestInfer_ChannelClosesAfterComplete(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeOpenAISSE(t, w, `{"choices":[{"delta":{"content":"x"}}]}`)
		writeOpenAISSE(t, w, "[DONE]")
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "ping"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)
}

// --- Infer: 401 → CodeAuthFailed; no key in error -----------------------

func TestInfer_401_ReturnsAuthFailed_NoKeyInError(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Unauthorized","type":"invalid_request_error","code":"invalid_api_key"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "x"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeAuthFailed {
		t.Errorf("Code = %d; want %d (AuthFailed)", ce.Code, proto.CodeAuthFailed)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Infer: 429 → CodeRateLimited --------------------------------------

func TestInfer_429_ReturnsRateLimited(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_error"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "x"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeRateLimited {
		t.Errorf("Code = %d; want %d (RateLimited)", ce.Code, proto.CodeRateLimited)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Infer: 500 → CodeInternal -----------------------------------------

func TestInfer_500_ReturnsInternal(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom","type":"server_error"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "x"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeInternal {
		t.Errorf("Code = %d; want %d (Internal)", ce.Code, proto.CodeInternal)
	}
}

// --- Infer: 400 → CodeInvalidParams ------------------------------------

func TestInfer_400_ReturnsInvalidParams(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Model `+"`bad`"+` is not in the allowlist.","type":"invalid_request_error"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "x", Model: "bad"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeInvalidParams {
		t.Errorf("Code = %d; want %d (InvalidParams)", ce.Code, proto.CodeInvalidParams)
	}
}

// --- Infer: context deadline → CodeTimeout -----------------------------

func TestInfer_CtxDeadline_ReturnsTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-block:
		}
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, infErr := c.Infer(ctx, proto.InferParams{Intent: "hang"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeTimeout {
		t.Errorf("Code = %d; want %d (Timeout)", ce.Code, proto.CodeTimeout)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Smoke test against the real CS gateway (opt-in) -------------------

func TestInfer_RealGateway_SmokeTest_OptIn(t *testing.T) {
	key := os.Getenv("CS_API_KEY_INTEGRATION")
	if key == "" {
		t.Skip("CS_API_KEY_INTEGRATION not set — skipping live smoke test")
	}
	c, err := NewClient(key, DefaultBaseURL, http.DefaultClient)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := c.Infer(ctx, proto.InferParams{
		Intent: "Respond with exactly one word: ok",
		Stream: true,
		Model:  "@cf/meta/llama-3.1-8b-instruct",
	})
	if err != nil {
		t.Fatalf("real Infer: %v", err)
	}
	_ = drainFrames(t, ch, 60*time.Second)
}
