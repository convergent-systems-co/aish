package secrets

import "sync"

// TaintedRegistry is the shell-layer per-line set of literal strings
// known to have originated from a secret. The shell populates it from
// `RunForCapture` whenever a captured sub-pipeline was a
// `secret get NAME` invocation; the post-Parse pipeline walker then
// looks up every parsed argument against the registry and flips the
// command's `Tainted` flag on an exact match.
//
// Design rationale and scope:
//
//   - **Exact-match only.** A tainted literal whose subsequent
//     shell-side transform (e.g. case-shift, padding, base64-encode)
//     produces a different string is no longer registered as tainted.
//     This is an explicitly-accepted MVP limitation per
//     v0.3-fu-secrets §Alternatives Table A. The follow-up TODO is
//     to thread Tainted through the tokenizer; that's the larger
//     parser surgery deferred from this PR.
//
//   - **Per-line, NOT per-session.** A new shell line MUST start with
//     an empty registry so a stale entry from a previous line cannot
//     poison the lookup. The shell's `runExternal` constructs a
//     fresh registry; that lifecycle is the contract.
//
//   - **Thread-safe.** `RunForCapture` may itself trigger nested
//     `$(...)` expansions (per the parser's recursion-with-depth-cap)
//     and the registry must be consistent across re-entry from the
//     parser layer. A `sync.RWMutex` is sufficient — Has() is the
//     hot path; Add() fires once per captured secret.
//
//   - **Empty values are ignored** (Add is a no-op). A secret value
//     of empty string would falsely taint every untainted empty
//     argument; the secret-set path rejects empty values upstream,
//     so this is belt-and-suspenders.
type TaintedRegistry struct {
	mu sync.RWMutex
	// set is the tainted-literal lookup table. Map-of-struct{} is
	// the canonical Go set type; it's allocation-free per entry
	// after the first map growth.
	set map[string]struct{}
}

// NewTaintedRegistry returns an empty registry. The shell creates one
// per `runExternal` call so the per-line lifecycle is enforced by
// construction.
func NewTaintedRegistry() *TaintedRegistry {
	return &TaintedRegistry{set: map[string]struct{}{}}
}

// Add records value as a tainted literal. A nil receiver, an empty
// value, or a value already present is a no-op. Add does not retain
// a reference to the input slice — the registry stores its own copy
// (the source buffer may be zeroed by the caller).
//
// We accept value as a string (already-immutable in Go) precisely
// because the caller has already paid the copy cost via
// `string(value)` at the capture boundary; insisting on a []byte
// here would double the allocation.
func (r *TaintedRegistry) Add(value string) {
	if r == nil || value == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.set == nil {
		r.set = map[string]struct{}{}
	}
	r.set[value] = struct{}{}
}

// Has reports whether value exactly matches a tainted literal in the
// registry. Nil-safe — a nil receiver returns false (the legacy
// no-taint path).
func (r *TaintedRegistry) Has(value string) bool {
	if r == nil || value == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.set[value]
	return ok
}

// Len returns the number of registered tainted literals. Exported
// for tests that want to assert on registry state without reaching
// into the set field.
func (r *TaintedRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.set)
}

// Clear removes every registered literal. Useful when reusing a
// registry across lines (rare — the shell preferred lifetime is one
// registry per line). The mutex makes this safe to call from any
// goroutine.
func (r *TaintedRegistry) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.set = map[string]struct{}{}
}

// RedactedTainted is the placeholder the history engine writes in
// place of a tainted command line. Exposed as a constant so consumers
// (history append, telemetry, audit) can match on it without
// re-deriving the literal.
const RedactedTainted = "[REDACTED:tainted]"
