package env

import (
	"testing"
)

// TestSetGet verifies the simplest happy path: Set a var, Get it back.
// Gates sub-issue #8 (env vars).
func TestSetGet(t *testing.T) {
	e := New()
	if err := e.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set returned err: %v", err)
	}
	got, ok := e.Get("FOO")
	if !ok {
		t.Fatal("Get(FOO) returned ok=false after Set")
	}
	if got != "bar" {
		t.Errorf("Get(FOO) = %q, want %q", got, "bar")
	}
}

// TestSetOverwrites confirms that re-Setting a key replaces its value
// rather than appending a duplicate entry.
func TestSetOverwrites(t *testing.T) {
	e := New()
	_ = e.Set("FOO", "first")
	_ = e.Set("FOO", "second")
	got, ok := e.Get("FOO")
	if !ok || got != "second" {
		t.Errorf("after overwrite: Get(FOO) = (%q, %v), want (%q, true)", got, ok, "second")
	}
	// The backing slice should have exactly one entry for FOO.
	count := 0
	for _, kv := range e.Environ() {
		if len(kv) >= 4 && kv[:4] == "FOO=" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Environ() has %d entries for FOO, want 1 (no duplicates)", count)
	}
}

// TestGetUnset verifies the unset-var contract.
func TestGetUnset(t *testing.T) {
	e := New()
	got, ok := e.Get("NEVER_SET")
	if ok {
		t.Errorf("Get on unset var returned ok=true (value=%q), want false", got)
	}
	if got != "" {
		t.Errorf("Get on unset var returned %q, want \"\"", got)
	}
}

// TestUnset verifies removal.
func TestUnset(t *testing.T) {
	e := New()
	_ = e.Set("REMOVE_ME", "x")
	e.Unset("REMOVE_ME")
	if _, ok := e.Get("REMOVE_ME"); ok {
		t.Error("Get after Unset returned ok=true, want false")
	}
}

// TestFromSliceSeedsState confirms an os.Environ-shaped slice round-trips
// through FromSlice + Get correctly.
func TestFromSliceSeedsState(t *testing.T) {
	e := FromSlice([]string{"FOO=bar", "BAZ=qux"})
	if v, ok := e.Get("FOO"); !ok || v != "bar" {
		t.Errorf("Get(FOO) = (%q, %v), want (bar, true)", v, ok)
	}
	if v, ok := e.Get("BAZ"); !ok || v != "qux" {
		t.Errorf("Get(BAZ) = (%q, %v), want (qux, true)", v, ok)
	}
}

// TestEnvironShape asserts that Environ() returns os.Environ-style entries
// safe to pass to exec.Cmd.Env.
func TestEnvironShape(t *testing.T) {
	e := New()
	_ = e.Set("A", "1")
	_ = e.Set("B", "2")
	got := e.Environ()
	if len(got) != 2 {
		t.Fatalf("Environ() returned %d entries, want 2 (%v)", len(got), got)
	}
	// Verify each entry contains exactly one `=` between key and value.
	for _, kv := range got {
		hasEq := false
		for _, c := range kv {
			if c == '=' {
				hasEq = true
				break
			}
		}
		if !hasEq {
			t.Errorf("Environ entry %q missing `=` separator", kv)
		}
	}
}

// TestExpandTable covers the v0.1-1 substitution surface: $VAR, ${VAR},
// $?, ${?}, unset-var → empty, lone-$ left alone.
func TestExpandTable(t *testing.T) {
	tests := []struct {
		name     string
		setup    map[string]string
		input    string
		lastExit int
		want     string
	}{
		{
			name:  "no substitution",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "dollar VAR",
			setup: map[string]string{"FOO": "bar"},
			input: "$FOO",
			want:  "bar",
		},
		{
			name:  "braced VAR",
			setup: map[string]string{"FOO": "bar"},
			input: "${FOO}",
			want:  "bar",
		},
		{
			name:  "VAR inside text",
			setup: map[string]string{"USER": "tom"},
			input: "hi $USER!",
			want:  "hi tom!",
		},
		{
			name:  "braced VAR adjacent to text",
			setup: map[string]string{"PRE": "abc"},
			input: "${PRE}xyz",
			want:  "abcxyz",
		},
		{
			name:  "unset VAR expands to empty",
			input: "[$NOPE]",
			want:  "[]",
		},
		{
			name:     "exit code via dollar question",
			input:    "code=$?",
			lastExit: 42,
			want:     "code=42",
		},
		{
			name:     "exit code via braced question",
			input:    "code=${?}",
			lastExit: 7,
			want:     "code=7",
		},
		{
			name:  "lone dollar is preserved",
			input: "price: $",
			want:  "price: $",
		},
		{
			name:  "multiple substitutions",
			setup: map[string]string{"A": "1", "B": "2"},
			input: "$A and $B",
			want:  "1 and 2",
		},
		// --- Quote-aware expansion (issue #163) ---
		{
			name:  "single quotes suppress dollar expansion",
			setup: map[string]string{"X": "expanded"},
			input: "'literal $X'",
			want:  "'literal $X'",
		},
		{
			name:  "single quotes suppress braced expansion",
			setup: map[string]string{"X": "expanded"},
			input: "'literal ${X}'",
			want:  "'literal ${X}'",
		},
		{
			name:     "single quotes suppress dollar-question",
			input:    "'code=$?'",
			lastExit: 42,
			want:     "'code=$?'",
		},
		{
			name:  "double quotes still expand",
			setup: map[string]string{"X": "expanded"},
			input: `"value=$X"`,
			want:  `"value=expanded"`,
		},
		{
			name:  "double quote inside single is literal",
			setup: map[string]string{"X": "expanded"},
			input: `'has " inside $X'`,
			want:  `'has " inside $X'`,
		},
		{
			name:  "single quote inside double is literal",
			setup: map[string]string{"X": "expanded"},
			input: `"has ' inside $X"`,
			want:  `"has ' inside expanded"`,
		},
		{
			name:  "expansion outside quotes still works",
			setup: map[string]string{"X": "expanded"},
			input: "before $X 'middle $X' after $X",
			want:  "before expanded 'middle $X' after expanded",
		},
		{
			name:  "adjacent single-quoted segments",
			setup: map[string]string{"X": "expanded"},
			input: "'a''b'$X",
			want:  "'a''b'expanded",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			for k, v := range tc.setup {
				_ = e.Set(k, v)
			}
			got := e.Expand(tc.input, tc.lastExit)
			if got != tc.want {
				t.Errorf("Expand(%q, lastExit=%d) = %q, want %q",
					tc.input, tc.lastExit, got, tc.want)
			}
		})
	}
}

// TestSetEmptyNameRejected is the negative path: `export =foo` is an
// invalid Set call. Required by Code.md §1 — error paths exercised.
func TestSetEmptyNameRejected(t *testing.T) {
	e := New()
	if err := e.Set("", "value"); err == nil {
		t.Error("Set(\"\", value) returned nil err, want non-nil")
	}
}
