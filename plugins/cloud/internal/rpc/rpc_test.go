package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// --- helpers ------------------------------------------------------------

// requestLine encodes a Request as a single NDJSON line (with trailing \n).
func requestLine(t *testing.T, r proto.Request) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return append(b, '\n')
}

// readLines splits an NDJSON buffer into trimmed non-empty lines.
func readLines(b []byte) []string {
	out := []string{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

// decodeResponse parses one NDJSON line as a proto.Response.
func decodeResponse(t *testing.T, line string) proto.Response {
	t.Helper()
	var r proto.Response
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	return r
}

// staticHandler returns a Handler that emits the supplied frames in order
// and then closes its channel. Honors ctx cancellation between sends.
func staticHandler(frames []proto.Frame) Handler {
	return func(ctx context.Context, _ proto.InferParams) (<-chan proto.Frame, error) {
		ch := make(chan proto.Frame, len(frames))
		go func() {
			defer close(ch)
			for _, f := range frames {
				select {
				case <-ctx.Done():
					return
				case ch <- f:
				}
			}
		}()
		return ch, nil
	}
}

// runWithTimeout runs d.Run in a goroutine and returns when it exits or
// the deadline fires. If the deadline fires first the test fails so we
// never hang the suite.
func runWithTimeout(t *testing.T, d *Dispatcher, deadline time.Duration) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(deadline):
		t.Fatalf("Run did not return within %v", deadline)
		return nil
	}
}

// --- NewDispatcher ------------------------------------------------------

func TestNewDispatcher_NotNil_EmptyHandlers(t *testing.T) {
	d := NewDispatcher(strings.NewReader(""), io.Discard, io.Discard)
	if d == nil {
		t.Fatal("NewDispatcher returned nil")
	}
	if got := len(d.Handlers()); got != 0 {
		t.Fatalf("expected empty handler map, got %d entries", got)
	}
}

// --- Register -----------------------------------------------------------

func TestRegister_StoresAndOverwrites(t *testing.T) {
	d := NewDispatcher(strings.NewReader(""), io.Discard, io.Discard)

	calledFirst := false
	first := func(ctx context.Context, _ proto.InferParams) (<-chan proto.Frame, error) {
		calledFirst = true
		ch := make(chan proto.Frame)
		close(ch)
		return ch, nil
	}
	d.Register(proto.MethodInfer, first)
	if _, ok := d.Handlers()[proto.MethodInfer]; !ok {
		t.Fatal("Register did not store handler for MethodInfer")
	}

	calledSecond := false
	second := func(ctx context.Context, _ proto.InferParams) (<-chan proto.Frame, error) {
		calledSecond = true
		ch := make(chan proto.Frame)
		close(ch)
		return ch, nil
	}
	d.Register(proto.MethodInfer, second)

	// Invoke the stored handler; only the second should run.
	h := d.Handlers()[proto.MethodInfer]
	ch, err := h(context.Background(), proto.InferParams{})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	for range ch {
	}
	if calledFirst {
		t.Error("first handler was called after being overwritten")
	}
	if !calledSecond {
		t.Error("second handler was not invoked after overwrite")
	}
}

// --- Run: round-trip a streaming handler --------------------------------

func TestRun_StreamingRoundTrip_TwoFrames(t *testing.T) {
	in := bytes.NewBuffer(requestLine(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "req-1",
		Method:  proto.MethodInfer,
		Params:  proto.InferParams{Intent: "list files", Stream: true},
	}))
	var out, errOut bytes.Buffer

	d := NewDispatcher(in, &out, &errOut)
	d.Register(proto.MethodInfer, staticHandler([]proto.Frame{
		{Type: proto.KindToken, Data: "ls"},
		{Type: proto.KindComplete, Invocation: "ls -la", Confidence: 0.9},
	}))

	if err := runWithTimeout(t, d, 2*time.Second); err != nil && err != io.EOF {
		t.Fatalf("Run returned error: %v", err)
	}

	lines := readLines(out.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 response lines, got %d: %q", len(lines), out.String())
	}

	r0 := decodeResponse(t, lines[0])
	r1 := decodeResponse(t, lines[1])

	if r0.ID != "req-1" || r1.ID != "req-1" {
		t.Errorf("expected both frames to carry ID req-1, got %q and %q", r0.ID, r1.ID)
	}
	if r0.JSONRPC != proto.Version || r1.JSONRPC != proto.Version {
		t.Errorf("expected JSONRPC %q on both frames, got %q and %q", proto.Version, r0.JSONRPC, r1.JSONRPC)
	}
	if r0.Result == nil || r0.Result.Type != proto.KindToken {
		t.Errorf("expected first frame Result.Type=token, got %+v", r0.Result)
	}
	if r1.Result == nil || r1.Result.Type != proto.KindComplete {
		t.Errorf("expected second frame Result.Type=complete, got %+v", r1.Result)
	}
	if r1.Result != nil && r1.Result.Invocation != "ls -la" {
		t.Errorf("expected complete frame Invocation=%q, got %q", "ls -la", r1.Result.Invocation)
	}
}

// --- Run: streaming with N tokens emits N+1 lines (token*N + complete) --

func TestRun_StreamingNPlusOneLines_SameID(t *testing.T) {
	tokens := []string{"echo", " ", "hello"}
	frames := make([]proto.Frame, 0, len(tokens)+1)
	for _, tok := range tokens {
		frames = append(frames, proto.Frame{Type: proto.KindToken, Data: tok})
	}
	frames = append(frames, proto.Frame{Type: proto.KindComplete, Invocation: "echo hello", Confidence: 1.0})

	in := bytes.NewBuffer(requestLine(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "stream-42",
		Method:  proto.MethodInfer,
		Params:  proto.InferParams{Intent: "say hi", Stream: true},
	}))
	var out bytes.Buffer

	d := NewDispatcher(in, &out, io.Discard)
	d.Register(proto.MethodInfer, staticHandler(frames))

	if err := runWithTimeout(t, d, 2*time.Second); err != nil && err != io.EOF {
		t.Fatalf("Run returned error: %v", err)
	}

	lines := readLines(out.Bytes())
	if len(lines) != len(tokens)+1 {
		t.Fatalf("expected %d lines (tokens+complete), got %d: %q", len(tokens)+1, len(lines), out.String())
	}
	for i, ln := range lines {
		r := decodeResponse(t, ln)
		if r.ID != "stream-42" {
			t.Errorf("line %d: expected ID stream-42, got %q", i, r.ID)
		}
		// Each line MUST be a valid JSON object.
		var probe map[string]any
		if err := json.Unmarshal([]byte(ln), &probe); err != nil {
			t.Errorf("line %d: not valid JSON: %v", i, err)
		}
	}
}

// --- Run: malformed JSON emits a ParseError response and continues -----

func TestRun_MalformedJSON_EmitsParseError_ThenContinues(t *testing.T) {
	in := bytes.NewBufferString("{not valid json\n")
	in.Write(requestLine(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "after-bad",
		Method:  proto.MethodInfer,
	}))
	var out bytes.Buffer

	d := NewDispatcher(in, &out, io.Discard)
	d.Register(proto.MethodInfer, staticHandler([]proto.Frame{
		{Type: proto.KindComplete, Invocation: "true", Confidence: 1.0},
	}))

	if err := runWithTimeout(t, d, 2*time.Second); err != nil && err != io.EOF {
		t.Fatalf("Run returned error: %v", err)
	}

	lines := readLines(out.Bytes())
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 response lines (parse error + handler response), got %d: %q", len(lines), out.String())
	}

	bad := decodeResponse(t, lines[0])
	if bad.Error == nil {
		t.Fatalf("expected first response to carry an Error, got %+v", bad)
	}
	if bad.Error.Code != proto.CodeParseError {
		t.Errorf("expected error code %d (ParseError), got %d", proto.CodeParseError, bad.Error.Code)
	}

	good := decodeResponse(t, lines[len(lines)-1])
	if good.ID != "after-bad" {
		t.Errorf("expected last response to be for the second request (ID after-bad), got ID %q", good.ID)
	}
	if good.Result == nil {
		t.Errorf("expected last response to carry Result, got error: %+v", good.Error)
	}
}

// --- Run: unknown method emits MethodNotFound ---------------------------

func TestRun_UnknownMethod_EmitsMethodNotFound(t *testing.T) {
	in := bytes.NewBuffer(requestLine(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "no-handler",
		Method:  "nonexistent.method",
	}))
	var out bytes.Buffer

	d := NewDispatcher(in, &out, io.Discard)
	// Intentionally register no handler for "nonexistent.method".

	if err := runWithTimeout(t, d, 2*time.Second); err != nil && err != io.EOF {
		t.Fatalf("Run returned error: %v", err)
	}

	lines := readLines(out.Bytes())
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 response line, got %d: %q", len(lines), out.String())
	}
	r := decodeResponse(t, lines[0])
	if r.ID != "no-handler" {
		t.Errorf("expected response ID no-handler, got %q", r.ID)
	}
	if r.Error == nil {
		t.Fatalf("expected Error payload, got %+v", r)
	}
	if r.Error.Code != proto.CodeMethodNotFound {
		t.Errorf("expected error code %d (MethodNotFound), got %d", proto.CodeMethodNotFound, r.Error.Code)
	}
}

// --- Run: context cancellation drains the handler channel and returns --

func TestRun_ContextCancellation_ReturnsPromptly(t *testing.T) {
	// A handler that emits one frame then blocks until ctx done. We
	// cancel the dispatcher's parent ctx while it is mid-stream and
	// expect Run to return.
	started := make(chan struct{})
	hangingHandler := func(ctx context.Context, _ proto.InferParams) (<-chan proto.Frame, error) {
		ch := make(chan proto.Frame)
		go func() {
			defer close(ch)
			select {
			case ch <- proto.Frame{Type: proto.KindToken, Data: "first"}:
			case <-ctx.Done():
				return
			}
			close(started)
			<-ctx.Done() // park until cancellation propagates
		}()
		return ch, nil
	}

	// Use a never-ending reader so Run is not killed by EOF.
	pr, pw := io.Pipe()
	defer pw.Close()

	go func() {
		pw.Write(requestLine(t, proto.Request{
			JSONRPC: proto.Version,
			ID:      "cancel-me",
			Method:  proto.MethodInfer,
		}))
		// Do not close pw — keep the reader alive so Run only exits
		// because of ctx cancellation, not EOF.
	}()

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	d := NewDispatcher(pr, &out, io.Discard).WithContext(ctx)
	d.Register(proto.MethodInfer, hangingHandler)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not begin streaming within 2s")
	}

	cancel()

	select {
	case <-errCh:
		// Acceptable: Run returned (nil or an error tied to cancel/EOF).
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of ctx cancel")
	}
}

// --- Run: error path — missing handler does not crash on streaming -----

func TestRun_ErrorResponseSchema_ContainsCodeAndMessage(t *testing.T) {
	in := bytes.NewBuffer(requestLine(t, proto.Request{
		JSONRPC: proto.Version,
		ID:      "schema-check",
		Method:  "unknown",
	}))
	var out bytes.Buffer

	d := NewDispatcher(in, &out, io.Discard)
	if err := runWithTimeout(t, d, 2*time.Second); err != nil && err != io.EOF {
		t.Fatalf("Run returned error: %v", err)
	}

	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Fatal("expected at least one error response, got none")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &raw); err != nil {
		t.Fatalf("error response is not valid JSON: %v", err)
	}
	errObj, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatalf("error response missing 'error' object: %v", raw)
	}
	if _, ok := errObj["code"]; !ok {
		t.Error("error object missing 'code' field")
	}
	if msg, ok := errObj["message"].(string); !ok || msg == "" {
		t.Error("error object missing or empty 'message' field")
	}
}

// --- Concurrent Register/Lookup is not required, but we exercise basic --
// --- safety by parallel reads after a single-writer phase.             --

func TestRegister_ConcurrentReadsAfterWrite(t *testing.T) {
	d := NewDispatcher(strings.NewReader(""), io.Discard, io.Discard)
	d.Register(proto.MethodInfer, staticHandler(nil))
	d.Register(proto.MethodPing, staticHandler(nil))

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.Handlers()[proto.MethodInfer]
			_ = d.Handlers()[proto.MethodPing]
		}()
	}
	wg.Wait()
}
