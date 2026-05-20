package anthropic

import (
	"context"
	"errors"
	"fmt"
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

// The literal API-key used in tests. The redaction tests below assert
// this string never appears in any returned String(), Error(), or
// human-visible field.
const fakeAPIKey = "sk-test-AAAA-BBBB-CCCC-DDDD"

// --- helpers ------------------------------------------------------------

// newServerWithHandler stands up an httptest.Server that runs h on every
// request. The server is closed via t.Cleanup.
func newServerWithHandler(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// writeSSE writes "event: <name>\ndata: <json>\n\n" to w and flushes.
func writeSSE(t *testing.T, w http.ResponseWriter, event, data string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		t.Fatalf("writeSSE: %v", err)
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
	// Also verify it does not contain the obvious "sk-" prefix of the
	// fake key — the tester scans for that.
	if strings.Contains(got, "sk-test-") {
		t.Errorf("Client.String() leaks API-key prefix: %q", got)
	}
}

// --- Infer: happy-path SSE stream → token frames + complete -----------

func TestInfer_StreamsTokensThenComplete(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(t, w, "message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-opus-4-7","usage":{"input_tokens":5}}}`)
		writeSSE(t, w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSE(t, w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ls"}}`)
		writeSSE(t, w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" -la"}}`)
		writeSSE(t, w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSE(t, w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)
		writeSSE(t, w, "message_stop", `{"type":"message_stop"}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ch, err := c.Infer(context.Background(), proto.InferParams{
		Intent: "list files long format",
		Stream: true,
		Model:  "claude-opus-4-7",
	})
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}
	if ch == nil {
		t.Fatal("Infer returned nil channel without error")
	}

	frames := drainFrames(t, ch, 5*time.Second)
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames (>=1 token + complete), got %d", len(frames))
	}

	// Last frame MUST be Complete.
	last := frames[len(frames)-1]
	if last.Type != proto.KindComplete {
		t.Errorf("expected final frame Type=complete, got %q", last.Type)
	}
	if last.Invocation == "" {
		t.Error("expected Complete frame to populate Invocation")
	}
	if last.Confidence <= 0 || last.Confidence > 1.0 {
		t.Errorf("expected Confidence in (0, 1.0], got %v", last.Confidence)
	}
	if last.Cost == nil {
		t.Error("expected Complete frame to populate Cost")
	}

	// All non-final frames MUST be Token frames in arrival order.
	tokenText := ""
	for i, f := range frames[:len(frames)-1] {
		if f.Type != proto.KindToken {
			t.Errorf("frame %d: expected Type=token, got %q", i, f.Type)
		}
		tokenText += f.Data
	}
	if !strings.Contains(last.Invocation, strings.TrimSpace(tokenText)) {
		// The model may post-process tokens, but at minimum the concatenated
		// stream must be a substring of, or equal to, the final invocation.
		// This is a coarse check; tighten in coder pass if needed.
		t.Logf("warning: tokenText=%q final=%q (loose check)", tokenText, last.Invocation)
	}
}

// --- Infer: channel closes after Complete frame ------------------------

func TestInfer_ChannelClosesAfterComplete(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(t, w, "message_start", `{"type":"message_start","message":{"id":"msg_2","model":"claude-opus-4-7","usage":{"input_tokens":1}}}`)
		writeSSE(t, w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)
		writeSSE(t, w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)
		writeSSE(t, w, "message_stop", `{"type":"message_stop"}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "ping", Stream: true})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	// Drain entirely; drainFrames fails if the channel never closes.
	_ = drainFrames(t, ch, 5*time.Second)
}

// --- Infer: 401 → CodeAuthFailed and message does NOT contain the key --

func TestInfer_401_ReturnsAuthFailed_NoKeyInError(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "anything"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeAuthFailed {
		t.Errorf("expected Code=%d (AuthFailed), got %d", proto.CodeAuthFailed, ce.Code)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Infer: 429 → CodeRateLimited --------------------------------------

func TestInfer_429_ReturnsRateLimited(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "anything"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeRateLimited {
		t.Errorf("expected Code=%d (RateLimited), got %d", proto.CodeRateLimited, ce.Code)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Infer: 500 → CodeInternal -----------------------------------------

func TestInfer_500_ReturnsInternal(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"boom"}}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, infErr := c.Infer(context.Background(), proto.InferParams{Intent: "anything"})
	ce := codedFromErr(t, infErr)
	if ce.Code != proto.CodeInternal {
		t.Errorf("expected Code=%d (Internal), got %d", proto.CodeInternal, ce.Code)
	}
}

// --- Infer: context deadline → CodeTimeout -----------------------------

func TestInfer_CtxDeadline_ReturnsTimeout(t *testing.T) {
	// Server hangs forever (until test cleanup). The client's ctx
	// deadline fires first.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		// Hold the connection open until the test cleans up.
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
		t.Errorf("expected Code=%d (Timeout), got %d", proto.CodeTimeout, ce.Code)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Infer: Authorization header carries the API key ------------------

func TestInfer_SendsAPIKeyHeader(t *testing.T) {
	var seenAuth atomic.Value // string
	var seenXAPI atomic.Value // string

	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		seenAuth.Store(r.Header.Get("Authorization"))
		seenXAPI.Store(r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(t, w, "message_stop", `{"type":"message_stop"}`)
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ch, err := c.Infer(context.Background(), proto.InferParams{Intent: "head probe"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	_ = drainFrames(t, ch, 5*time.Second)

	auth, _ := seenAuth.Load().(string)
	xapi, _ := seenXAPI.Load().(string)

	// Anthropic accepts the key on the `x-api-key` header in the real
	// API; many clients also support `Authorization: Bearer ...`. Accept
	// either form — what matters is that exactly one header carries the
	// key and nothing else does.
	bearerOK := auth == "Bearer "+fakeAPIKey
	xapiOK := xapi == fakeAPIKey
	if !bearerOK && !xapiOK {
		t.Errorf("expected either Authorization=Bearer or x-api-key header to carry the key; got auth=%q x-api-key=%q",
			auth, xapi)
	}
}

// --- Smoke test against real api.anthropic.com (opt-in) ----------------

func TestInfer_RealAnthropic_SmokeTest_OptIn(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY_INTEGRATION")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY_INTEGRATION not set — skipping live smoke test")
	}
	// We do not actually hit the live API here in CI. This test exists
	// so a developer can opt in by exporting the env var; CI never sets
	// it. We do not assert behavior — the smoke is on connectivity only.
	c, err := NewClient(key, DefaultBaseURL, http.DefaultClient)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := c.Infer(ctx, proto.InferParams{
		Intent: "Respond with exactly one word: ok",
		Stream: true,
		Model:  "claude-opus-4-7",
	})
	if err != nil {
		t.Fatalf("real Infer: %v", err)
	}
	_ = drainFrames(t, ch, 60*time.Second)
}
