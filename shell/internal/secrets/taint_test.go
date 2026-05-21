package secrets

import "testing"

// TestTaintedRegistry_AddHas covers the basic add-then-lookup path.
// We use the §4-conformant placeholder pattern for the registered
// value so a repository grep over secret-looking literals is clean.
func TestTaintedRegistry_AddHas(t *testing.T) {
	r := NewTaintedRegistry()
	const sentinel = "[REDACTED:test-value-registry]"
	r.Add(sentinel)
	if !r.Has(sentinel) {
		t.Fatalf("Has(%q) = false after Add; want true", sentinel)
	}
	if r.Has("[REDACTED:other]") {
		t.Fatalf("Has on un-added value returned true")
	}
}

// TestTaintedRegistry_NilSafe asserts the nil-receiver methods are
// no-ops + false. The shell layer relies on this so a Registry
// that's not yet wired during early shell startup never panics.
func TestTaintedRegistry_NilSafe(t *testing.T) {
	var r *TaintedRegistry
	r.Add("ignored")
	if r.Has("ignored") {
		t.Fatalf("nil receiver Has returned true")
	}
	if got := r.Len(); got != 0 {
		t.Fatalf("nil Len = %d, want 0", got)
	}
	r.Clear() // must not panic
}

// TestTaintedRegistry_EmptyValueRejected pins the contract that an
// empty string is never recorded as tainted (and never matches).
// The vault.Set path rejects empty values upstream; this is the
// belt-and-suspenders backstop.
func TestTaintedRegistry_EmptyValueRejected(t *testing.T) {
	r := NewTaintedRegistry()
	r.Add("")
	if r.Len() != 0 {
		t.Fatalf("Add(\"\") added an entry; Len = %d", r.Len())
	}
	if r.Has("") {
		t.Fatalf("Has(\"\") returned true on empty registry")
	}
}

// TestTaintedRegistry_Clear resets the registry without re-allocation
// surprises.
func TestTaintedRegistry_Clear(t *testing.T) {
	r := NewTaintedRegistry()
	r.Add("[REDACTED:one]")
	r.Add("[REDACTED:two]")
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want 2", r.Len())
	}
	r.Clear()
	if r.Len() != 0 {
		t.Fatalf("Len after Clear = %d, want 0", r.Len())
	}
	if r.Has("[REDACTED:one]") {
		t.Fatalf("Has returned true after Clear")
	}
}

// TestRedactedTainted_Constant pins the value so a downstream test
// matching on this string fails fast if someone changes the literal.
func TestRedactedTainted_Constant(t *testing.T) {
	if RedactedTainted != "[REDACTED:tainted]" {
		t.Fatalf("RedactedTainted = %q, want [REDACTED:tainted]", RedactedTainted)
	}
}
