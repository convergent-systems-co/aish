// Package rpc is the NDJSON JSON-RPC dispatcher used by
// aish-inference-cloud. It reads `proto.Request` frames from an
// io.Reader (the plugin's stdin), routes to a registered Handler by
// Method name, and writes `proto.Response` frames to an io.Writer (the
// plugin's stdout).
//
// Streaming responses: a Handler returns a channel of frames. The
// dispatcher reads from the channel until close and emits one Response
// per frame, each tagged with the originating Request.ID.
//
// This package is the boundary between transport (stdin/stdout, NDJSON)
// and inference logic (the anthropic client lives in
// plugins/cloud/internal/anthropic). It deliberately knows nothing about
// the Anthropic API.
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// scannerBufMax is the maximum line length the dispatcher will accept on
// its input. 1 MiB accommodates very long intent strings and pasted
// payloads without truncating mid-request.
const scannerBufMax = 1 << 20 // 1 MiB

// Handler is the per-method dispatch target. Returns a channel that
// closes when the response stream ends. Non-streaming methods send one
// frame and close.
//
// The handler MUST honor ctx cancellation: when ctx.Done() fires, the
// handler closes its channel promptly (target: 100ms).
type Handler func(ctx context.Context, params proto.InferParams) (<-chan proto.Frame, error)

// Dispatcher reads requests, routes to Handlers, writes responses.
type Dispatcher struct {
	in       io.Reader
	out      io.Writer
	errOut   io.Writer
	handlers map[string]Handler
	ctx      context.Context
}

// NewDispatcher constructs a Dispatcher reading from in, writing
// responses to out, and writing internal errors (never API keys, per
// Common.md §4) to errOut.
func NewDispatcher(in io.Reader, out, errOut io.Writer) *Dispatcher {
	return &Dispatcher{
		in:       in,
		out:      out,
		errOut:   errOut,
		handlers: make(map[string]Handler),
	}
}

// Register binds a Handler to a method name. Returns the Dispatcher for
// chaining. Re-registering the same name overwrites the prior Handler.
func (d *Dispatcher) Register(method string, h Handler) *Dispatcher {
	d.handlers[method] = h
	return d
}

// WithContext sets the parent context used to derive per-request
// handler contexts and to drive Run-loop cancellation. When unset, Run
// uses context.Background(). Returns the Dispatcher for chaining.
func (d *Dispatcher) WithContext(ctx context.Context) *Dispatcher {
	d.ctx = ctx
	return d
}

// Handlers returns the registered handler map. Exposed for tests and
// internal introspection; the production code path is Register/Run only.
func (d *Dispatcher) Handlers() map[string]Handler {
	return d.handlers
}

// Run drives the dispatch loop until in returns io.EOF, an
// unrecoverable I/O error occurs, or the parent context is canceled.
//
// For each NDJSON line on d.in:
//   - On JSON parse failure, emit a Response with CodeParseError and
//     continue.
//   - When JSONRPC != "2.0" or Method is empty, emit CodeInvalidRequest.
//   - When no Handler is registered for the Method, emit CodeMethodNotFound.
//   - Otherwise invoke the Handler under a per-request context derived
//     from d.ctx and stream each emitted Frame back as a Response,
//     preserving Request.ID on every frame.
func (d *Dispatcher) Run() error {
	ctx := d.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	bw := bufio.NewWriter(d.out)
	enc := json.NewEncoder(bw)

	// Read lines on a goroutine so we can select on ctx.Done() and not
	// block forever on a stdin that never EOFs.
	type readMsg struct {
		line []byte
		err  error
	}
	lineCh := make(chan readMsg)
	// Capture the reader so the goroutine reads independently of the
	// outer loop; we don't try to interrupt the in-flight read on
	// cancellation — we simply stop consuming.
	go func() {
		scanner := bufio.NewScanner(d.in)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, scannerBufMax)
		for scanner.Scan() {
			// Copy because Scanner reuses its internal buffer between calls.
			b := scanner.Bytes()
			cp := make([]byte, len(b))
			copy(cp, b)
			select {
			case lineCh <- readMsg{line: cp}:
			case <-ctx.Done():
				return
			}
		}
		// EOF or scanner error: surface once and stop.
		err := scanner.Err()
		select {
		case lineCh <- readMsg{err: err}:
		case <-ctx.Done():
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Best-effort flush of whatever has accumulated on out.
			_ = bw.Flush()
			// context.Canceled is the expected cancellation signal; treat
			// it as a clean shutdown so main() does not log it as an
			// I/O failure. Deadline-exceeded etc. surface verbatim.
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case msg := <-lineCh:
			if msg.err != nil {
				_ = bw.Flush()
				return msg.err
			}
			if msg.line == nil {
				// Scanner exited without error (EOF).
				_ = bw.Flush()
				return nil
			}
			if len(msg.line) == 0 {
				// Skip blank lines silently — NDJSON tolerates them.
				continue
			}

			if err := d.handleLine(ctx, msg.line, enc, bw); err != nil {
				return err
			}
		}
	}
}

// handleLine parses one NDJSON line, validates it, dispatches to the
// appropriate handler, and streams the resulting frames back to out.
// Returns a non-nil error only for unrecoverable write failures; every
// per-request fault (parse, invalid request, unknown method, handler
// error) is surfaced inline as a Response.Error and the loop continues.
func (d *Dispatcher) handleLine(parent context.Context, line []byte, enc *json.Encoder, bw *bufio.Writer) error {
	var req proto.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return writeErr(enc, bw, "", proto.CodeParseError, "parse error: "+err.Error())
	}

	if req.JSONRPC != proto.Version || req.Method == "" {
		return writeErr(enc, bw, req.ID, proto.CodeInvalidRequest, "invalid request: jsonrpc must be \"2.0\" and method must be set")
	}

	handler, ok := d.handlers[req.Method]
	if !ok {
		return writeErr(enc, bw, req.ID, proto.CodeMethodNotFound, fmt.Sprintf("method not found: %q", req.Method))
	}

	reqCtx, cancel := context.WithCancel(parent)
	defer cancel()

	ch, err := handler(reqCtx, req.Params)
	if err != nil {
		return writeErr(enc, bw, req.ID, proto.CodeInternal, "handler error: "+err.Error())
	}
	if ch == nil {
		// A handler that returns (nil, nil) has nothing to stream; treat
		// it as an internal error rather than silently emitting nothing.
		return writeErr(enc, bw, req.ID, proto.CodeInternal, "handler returned nil channel")
	}

	for {
		select {
		case <-parent.Done():
			// Parent cancellation: signal the handler and drain its
			// channel so the goroutine is not left blocked on send. The
			// deferred cancel() above has already fired via the parent
			// chain; this loop just consumes until the handler closes.
			cancel()
			for range ch {
			}
			return nil
		case frame, open := <-ch:
			if !open {
				return nil
			}
			f := frame // capture before taking address; loop var would alias
			resp := proto.Response{
				JSONRPC: proto.Version,
				ID:      req.ID,
				Result:  &f,
			}
			if err := enc.Encode(&resp); err != nil {
				return fmt.Errorf("rpc: encode response: %w", err)
			}
			if err := bw.Flush(); err != nil {
				return fmt.Errorf("rpc: flush response: %w", err)
			}
		}
	}
}

// writeErr emits a single Response carrying an Error payload and flushes.
// Returns a non-nil error only on a true write failure.
func writeErr(enc *json.Encoder, bw *bufio.Writer, id string, code int, msg string) error {
	resp := proto.Response{
		JSONRPC: proto.Version,
		ID:      id,
		Error: &proto.Error{
			Code:    code,
			Message: msg,
		},
	}
	if err := enc.Encode(&resp); err != nil {
		return fmt.Errorf("rpc: encode error response: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("rpc: flush error response: %w", err)
	}
	return nil
}
