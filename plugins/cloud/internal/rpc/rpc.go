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
	"context"
	"errors"
	"io"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// Handler is the per-method dispatch target. Returns a channel that
// closes when the response stream ends. Non-streaming methods send one
// frame and close.
//
// The handler MUST honor ctx cancellation: when ctx.Done() fires, the
// handler closes its channel promptly (target: 100ms).
type Handler func(ctx context.Context, params proto.InferParams) (<-chan proto.Frame, error)

// Dispatcher reads requests, routes to Handlers, writes responses.
//
// v0.1-3 SEED: types and constructor only. Handler registration and
// the Run loop are implementations the T1 coder fills in. Methods on
// the seed return placeholder errors so the package compiles and the
// binary builds.
type Dispatcher struct {
	in       io.Reader
	out      io.Writer
	errOut   io.Writer
	handlers map[string]Handler
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

// Run drives the dispatch loop until in returns io.EOF or an
// unrecoverable I/O error.
//
// v0.1-3 SEED: returns ErrNotImplemented. The T1 coder fills this with
// the actual NDJSON read / dispatch / write loop per the TL plan.
func (d *Dispatcher) Run() error {
	return ErrNotImplemented
}

// ErrNotImplemented is returned by seed-stub methods. The T1 coder MUST
// remove every reference to this constant from the production code
// before tests pass.
var ErrNotImplemented = errors.New("rpc: dispatcher not yet implemented (seed stub)")
