package term

import "testing"

// TestSearch_BasicMatch — incremental query narrows the match set.
func TestSearch_BasicMatch(t *testing.T) {
	src := NewMemorySource([]string{"echo one", "ls -la", "echo two", "cd /tmp"})
	s := NewSearch(src)
	s.AppendQuery('e')
	if got := s.Current(); got != "echo two" {
		t.Fatalf("first match: want \"echo two\", got %q", got)
	}
	s.AppendQuery('c')
	s.AppendQuery('h')
	if got := s.Current(); got != "echo two" {
		t.Fatalf("after \"ech\": want \"echo two\", got %q", got)
	}
	// Next match: should fall back to "echo one".
	if ok := s.Next(); !ok {
		t.Fatalf("Next should find another match")
	}
	if got := s.Current(); got != "echo one" {
		t.Fatalf("after Next: want \"echo one\", got %q", got)
	}
	// Next past oldest: ok=false.
	if ok := s.Next(); ok {
		t.Fatalf("Next past oldest should return false")
	}
}

// TestSearch_CaseInsensitive — case is ignored.
func TestSearch_CaseInsensitive(t *testing.T) {
	src := NewMemorySource([]string{"Echo One", "ls"})
	s := NewSearch(src)
	s.AppendQuery('e')
	s.AppendQuery('c')
	s.AppendQuery('h')
	s.AppendQuery('o')
	if got := s.Current(); got != "Echo One" {
		t.Fatalf("case-insensitive match: got %q", got)
	}
}

// TestSearch_Backspace — removing a char re-runs the match from the
// most-recent direction.
func TestSearch_Backspace(t *testing.T) {
	src := NewMemorySource([]string{"echo one", "echo two"})
	s := NewSearch(src)
	s.AppendQuery('e')
	s.AppendQuery('c')
	if got := s.Current(); got != "echo two" {
		t.Fatalf("want \"echo two\", got %q", got)
	}
	s.Backspace()
	s.Backspace()
	// Empty query — Current returns "" so the caller can leave the
	// search mode in a sane state.
	if got := s.Current(); got != "" {
		t.Fatalf("empty query should return empty; got %q", got)
	}
}

// TestSearch_NoMatch — Current returns "" when the query has no hit.
func TestSearch_NoMatch(t *testing.T) {
	src := NewMemorySource([]string{"echo"})
	s := NewSearch(src)
	s.AppendQuery('x')
	if got := s.Current(); got != "" {
		t.Fatalf("no-match query: want empty, got %q", got)
	}
}

// TestSearch_EmptyHistory — every query returns empty.
func TestSearch_EmptyHistory(t *testing.T) {
	src := NewMemorySource(nil)
	s := NewSearch(src)
	s.AppendQuery('x')
	if got := s.Current(); got != "" {
		t.Fatalf("empty history: want empty, got %q", got)
	}
	if ok := s.Next(); ok {
		t.Fatalf("Next on empty history should be false")
	}
}
