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
// Cost-log rotation is intentionally out-of-scope for v0.1-3; the
// v0.1-5 telemetry epic will handle bounded growth.
package reliab

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RetryOptions configures WithRetries. The zero value is a valid set of
// defaults (3 attempts, 250ms→500ms→1s backoff, retry on 429/5xx).
type RetryOptions struct {
	// MaxAttempts is the total number of attempts (initial + retries).
	// Default 3 when zero or negative.
	MaxAttempts int
	// Backoff is the sequence of delays between attempts. Backoff[i] is
	// the delay between attempt i and attempt i+1. When nil, defaults
	// to {250ms, 500ms, 1s}. When non-nil but shorter than needed, the
	// last value is reused for subsequent gaps.
	Backoff []time.Duration
	// Retryable reports whether an error should be retried. When nil,
	// the default predicate retries on HTTPStatusError with status 429
	// or 5xx and on every non-status error (e.g. transport failures).
	Retryable func(error) bool
}

// defaultBackoff is the sequence used when RetryOptions.Backoff is nil.
var defaultBackoff = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
}

// HTTPStatusError is the typed error the default Retryable predicate
// inspects. Callers (the anthropic client) wrap non-2xx responses in
// this type so the retry layer can classify them without parsing
// strings.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

// Error implements error. The body is included for diagnostics but
// callers MUST NOT place secrets in it (Common.md §4).
func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Body)
}

// IsRetryable reports whether the status code is one the default
// predicate considers retryable: 429 (rate limit) or any 5xx.
func (e *HTTPStatusError) IsRetryable() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusTooManyRequests || (e.StatusCode >= 500 && e.StatusCode <= 599)
}

// defaultRetryable retries on 429/5xx HTTPStatusError and on any
// non-typed error (transport failure, decode error, etc.). Non-429
// 4xx responses are non-retryable.
func defaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	var hse *HTTPStatusError
	if errors.As(err, &hse) {
		return hse.IsRetryable()
	}
	return true
}

// WithRetries calls fn up to opts.MaxAttempts times, backing off
// between attempts per opts.Backoff. Stops early if ctx is cancelled or
// opts.Retryable returns false. Returns the value and error from the
// last attempt; on ctx cancellation returns the last error joined with
// ctx.Err().
func WithRetries[T any](ctx context.Context, fn func(context.Context) (T, error), opts RetryOptions) (T, error) {
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	backoff := opts.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}
	retryable := opts.Retryable
	if retryable == nil {
		retryable = defaultRetryable
	}

	var (
		zero    T
		result  T
		lastErr error
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Surface ctx cancellation before each attempt (including the
		// first, so a pre-cancelled ctx never invokes fn).
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return zero, errors.Join(lastErr, err)
			}
			return zero, err
		}

		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}
		lastErr = err
		result = v

		// Don't retry if predicate says no.
		if !retryable(err) {
			return result, err
		}
		// Don't sleep after the final attempt.
		if attempt == maxAttempts-1 {
			break
		}

		// Pick the gap. backoff[attempt] when available, otherwise the
		// final element (or 0 if backoff is empty).
		var gap time.Duration
		switch {
		case len(backoff) == 0:
			gap = 0
		case attempt < len(backoff):
			gap = backoff[attempt]
		default:
			gap = backoff[len(backoff)-1]
		}

		if gap > 0 {
			timer := time.NewTimer(gap)
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		} else {
			// Even with zero backoff, honor ctx cancellation between
			// attempts.
			select {
			case <-ctx.Done():
				return zero, errors.Join(lastErr, ctx.Err())
			default:
			}
		}
	}

	return result, lastErr
}

// WithTimeout is a thin wrapper over context.WithTimeout. It exists so
// callers in the cloud plugin import a single reliability surface; the
// returned cancel function MUST be invoked to release the timer (the
// stdlib guarantees no goroutine leak when it is).
func WithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// Cost records per-request cost telemetry. Construct with NewCost or
// NewCostDefault. Methods are safe for concurrent use; rows are written
// atomically (one Write call per row).
type Cost struct {
	mu sync.Mutex
	w  io.Writer
}

// NewCost constructs a Cost that appends JSONL rows to w. When w is
// nil, the constructor falls back to opening the default cost-log file
// under the user's home directory; tests inject a non-nil writer to
// avoid touching the developer's real log.
//
// If w is nil and no HOME/USERPROFILE is set, the returned Cost holds a
// no-op writer (io.Discard). Callers that need an explicit error on
// missing home should use NewCostDefault.
func NewCost(w io.Writer) *Cost {
	if w == nil {
		path := DefaultCostLogPath()
		if path == "" {
			return &Cost{w: io.Discard}
		}
		f, err := openCostFile(path)
		if err != nil {
			return &Cost{w: io.Discard}
		}
		return &Cost{w: f}
	}
	return &Cost{w: w}
}

// NewCostDefault constructs a Cost backed by the default cost-log file
// resolved from env (HOME or USERPROFILE). Returns ErrNoHomeDir when
// neither var is set, so callers can fail fast rather than silently
// dropping records.
func NewCostDefault(env []string) (*Cost, error) {
	path := costLogPathFromEnv(env)
	if path == "" {
		return nil, ErrNoHomeDir
	}
	f, err := openCostFile(path)
	if err != nil {
		return nil, err
	}
	return &Cost{w: f}, nil
}

// openCostFile creates the parent directory and opens the cost-log
// file for append.
func openCostFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// costRow is the on-disk schema. Field tags are stable; renaming any
// of them is a BREAKING change per Common.md §6 (Documentation).
type costRow struct {
	Chronon   string  `json:"chronon"`
	Model     string  `json:"model"`
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	USD       float64 `json:"usd"`
	ReqID     string  `json:"req_id"`
}

// Record appends one JSONL row to the underlying writer. Schema:
//
//	{"chronon":"<RFC3339Nano>","model":"<name>","tokens_in":N,"tokens_out":M,"usd":F,"req_id":"<uuid>"}
//
// model MUST be non-empty; tokensIn / tokensOut MUST be >= 0; usd MUST
// be >= 0. Negative or empty inputs return ErrInvalidCostRecord.
//
// req_id is minted via crypto/rand (RFC 4122 v4 layout) on every call.
// Upstream callers that want to thread a known request id through
// should record it themselves in a sibling log; v0.1-3 keeps the
// surface narrow.
func (c *Cost) Record(model string, tokensIn, tokensOut int, usd float64) error {
	if model == "" {
		return fmt.Errorf("%w: empty model", ErrInvalidCostRecord)
	}
	if tokensIn < 0 {
		return fmt.Errorf("%w: negative tokens_in=%d", ErrInvalidCostRecord, tokensIn)
	}
	if tokensOut < 0 {
		return fmt.Errorf("%w: negative tokens_out=%d", ErrInvalidCostRecord, tokensOut)
	}
	if usd < 0 {
		return fmt.Errorf("%w: negative usd=%g", ErrInvalidCostRecord, usd)
	}

	reqID, err := newReqID()
	if err != nil {
		return err
	}

	row := costRow{
		Chronon:   time.Now().UTC().Format(time.RFC3339Nano),
		Model:     model,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		USD:       usd,
		ReqID:     reqID,
	}

	buf, err := json.Marshal(row)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.w == nil {
		return nil
	}
	_, err = c.w.Write(buf)
	return err
}

// newReqID generates an RFC 4122 v4 UUID string from crypto/rand. No
// external dependency; the layout bits are set per the spec.
func newReqID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Version 4 (random) and variant (RFC 4122).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	), nil
}

// DefaultCostLogPath returns the platform-correct cost-log path:
// `$HOME/.aish/cost-log.jsonl` on POSIX, `$USERPROFILE\.aish\cost-log.jsonl`
// on Windows. Returns "" when neither env var is set — callers MUST
// surface an error rather than silently using a wrong path.
//
// Reads HOME / USERPROFILE via os.Getenv so t.Setenv-driven test
// overrides are respected.
func DefaultCostLogPath() string {
	home := homeDirLive()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".aish", "cost-log.jsonl")
}

// homeDirLive reads the live process environment for HOME, falling
// back to USERPROFILE for Windows parity. Returns "" when neither is
// set.
func homeDirLive() string {
	if v := os.Getenv("HOME"); v != "" {
		return v
	}
	if v := os.Getenv("USERPROFILE"); v != "" {
		return v
	}
	return ""
}

// homeDirFromEnv resolves the home directory from an explicit
// KEY=VALUE env slice. Re-implemented locally (rather than importing
// from shell/internal/shell) to avoid a cross-module dependency — the
// shell module is a peer in the workspace and its internal package is
// not exported. This is a deliberate 5-line duplication.
func homeDirFromEnv(env []string) string {
	const homePrefix = "HOME="
	const userProfilePrefix = "USERPROFILE="
	var fallback string
	for _, kv := range env {
		switch {
		case len(kv) > len(homePrefix) && kv[:len(homePrefix)] == homePrefix:
			if v := kv[len(homePrefix):]; v != "" {
				return v
			}
		case len(kv) > len(userProfilePrefix) && kv[:len(userProfilePrefix)] == userProfilePrefix:
			if v := kv[len(userProfilePrefix):]; v != "" {
				fallback = v
			}
		}
	}
	return fallback
}

// costLogPathFromEnv resolves the cost-log path from an explicit env
// slice. Used by NewCostDefault so callers can pin the lookup to an
// audit-controlled env rather than the live process state.
func costLogPathFromEnv(env []string) string {
	home := homeDirFromEnv(env)
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".aish", "cost-log.jsonl")
}

// Sentinel errors.
var (
	ErrInvalidCostRecord = errors.New("reliab: invalid cost record")
	ErrNoHomeDir         = errors.New("reliab: HOME / USERPROFILE not set")
)
