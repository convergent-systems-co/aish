// Package reliab provides reliability helpers used by the
// aish-inference-cloud plugin: retry-with-exponential-backoff for
// upstream HTTP calls, and a per-request cost recorder that appends
// JSONL rows to a configurable writer.
//
// The cost log defaults to a file under the user's home directory
// (`~/.aish/cost-log.jsonl` on POSIX; `%USERPROFILE%\.aish\cost-log.jsonl`
// on Windows) per the same homeDir semantics as the shell module.
// Tests inject a custom io.Writer so they never touch the developer's
// real log.
//
// Per Common.md §4, this package MUST NOT log API keys, tokens, or
// other secrets. It deals in costs and counts only.
//
// v0.1-3 SEED: type and signature stubs only. Bodies are filled by the
// T3 coder.
package reliab

import (
	"context"
	"errors"
	"io"
	"time"
)

// RetryOptions configures WithRetries. The zero value is a valid set of
// defaults (3 attempts, 250ms→500ms→1s backoff, all errors retryable).
type RetryOptions struct {
	// MaxAttempts is the total number of attempts (initial + retries).
	// Default 3.
	MaxAttempts int
	// Backoff is the sequence of delays between attempts. backoff[i] is
	// the delay between attempt i and attempt i+1. When nil, defaults
	// to {250ms, 500ms, 1s}.
	Backoff []time.Duration
	// Retryable reports whether an error should be retried. When nil,
	// every non-nil error is retryable.
	Retryable func(error) bool
}

// WithRetries calls fn up to opts.MaxAttempts times, backing off
// between attempts per opts.Backoff. Stops early if ctx is cancelled or
// opts.Retryable returns false. Returns the value+error of the last
// attempt.
func WithRetries[T any](ctx context.Context, fn func(context.Context) (T, error), opts RetryOptions) (T, error) {
	var zero T
	_ = ctx
	_ = fn
	_ = opts
	return zero, ErrNotImplemented
}

// Cost records per-request cost telemetry. Construct with NewCost.
type Cost struct {
	w io.Writer
}

// NewCost constructs a Cost that appends JSONL rows to w. When w is
// nil, the constructor SHOULD open the default cost-log file under the
// user's home directory; tests inject a non-nil writer to avoid that.
func NewCost(w io.Writer) *Cost {
	return &Cost{w: w}
}

// Record appends one JSONL row to the underlying writer. Schema:
//
//	{"chronon":"<RFC3339>","model":"<name>","tokens_in":N,"tokens_out":M,"usd":F,"req_id":"<uuid>"}
//
// model MUST be non-empty; tokensIn / tokensOut MUST be >= 0; usd MUST
// be >= 0. Negative or empty inputs return ErrInvalidCostRecord.
func (c *Cost) Record(model string, tokensIn, tokensOut int, usd float64) error {
	_ = model
	_ = tokensIn
	_ = tokensOut
	_ = usd
	return ErrNotImplemented
}

// DefaultCostLogPath returns the platform-correct cost-log path:
// `$HOME/.aish/cost-log.jsonl` on POSIX, `$USERPROFILE\.aish\cost-log.jsonl`
// on Windows. Returns "" when neither env var is set — callers MUST
// surface an error rather than silently using a wrong path.
func DefaultCostLogPath() string {
	return ""
}

// Sentinel errors. The T3 coder MUST remove every reference to
// ErrNotImplemented from the production code before tests pass.
var (
	ErrNotImplemented     = errors.New("reliab: not yet implemented (seed stub)")
	ErrInvalidCostRecord  = errors.New("reliab: invalid cost record")
	ErrNoHomeDir          = errors.New("reliab: HOME / USERPROFILE not set")
)
