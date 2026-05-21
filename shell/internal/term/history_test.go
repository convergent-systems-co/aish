package term

import "testing"

// TestHistoryNav_EmptyHistory — with no history, Up/Down return the
// stashed pre-nav buffer and keep `Pending` empty.
func TestHistoryNav_EmptyHistory(t *testing.T) {
	src := NewMemorySource(nil)
	h := NewHistoryNav(src)
	h.Stash("partial")
	got, ok := h.Up()
	if ok {
		t.Fatalf("Up on empty history should return ok=false; got %q", got)
	}
	got, ok = h.Down()
	if ok {
		t.Fatalf("Down on empty history should return ok=false; got %q", got)
	}
}

// TestHistoryNav_UpDown — Up walks backward; Down walks forward;
// going past the bottom restores the stashed pre-nav buffer.
func TestHistoryNav_UpDown(t *testing.T) {
	src := NewMemorySource([]string{"first", "second", "third"})
	h := NewHistoryNav(src)
	h.Stash("partial")

	// First Up returns the most recent ("third").
	got, ok := h.Up()
	if !ok || got != "third" {
		t.Fatalf("first Up: want (\"third\", true); got (%q, %v)", got, ok)
	}
	// Second Up: "second".
	got, ok = h.Up()
	if !ok || got != "second" {
		t.Fatalf("second Up: want (\"second\", true); got (%q, %v)", got, ok)
	}
	// Third Up: "first".
	got, ok = h.Up()
	if !ok || got != "first" {
		t.Fatalf("third Up: want (\"first\", true); got (%q, %v)", got, ok)
	}
	// Fourth Up: clamped to oldest.
	got, ok = h.Up()
	if !ok || got != "first" {
		t.Fatalf("Up past oldest should clamp to \"first\"; got (%q, %v)", got, ok)
	}

	// Down: "second", "third", then the stash.
	got, ok = h.Down()
	if !ok || got != "second" {
		t.Fatalf("Down: want \"second\"; got (%q, %v)", got, ok)
	}
	got, ok = h.Down()
	if !ok || got != "third" {
		t.Fatalf("Down: want \"third\"; got (%q, %v)", got, ok)
	}
	got, ok = h.Down()
	if !ok || got != "partial" {
		t.Fatalf("Down past newest should restore stash; got (%q, %v)", got, ok)
	}
	// Past the stash: no-op.
	got, ok = h.Down()
	if ok {
		t.Fatalf("Down past stash should return ok=false; got %q", got)
	}
}

// TestHistoryNav_AppendAffectsNextSession — calling Append on the
// source MUST be visible to the next NewHistoryNav (sessions reset).
// This is the contract that lets the editor build a fresh nav state
// per ReadLine call.
func TestHistoryNav_AppendAffectsNextSession(t *testing.T) {
	src := NewMemorySource([]string{"a"})
	src.Append("b")
	h := NewHistoryNav(src)
	h.Stash("")
	got, _ := h.Up()
	if got != "b" {
		t.Fatalf("Up should see Appended entry; got %q", got)
	}
}

// TestMemorySource_Len — source length must reflect Appends.
func TestMemorySource_Len(t *testing.T) {
	src := NewMemorySource([]string{"a", "b"})
	if src.Len() != 2 {
		t.Fatalf("want 2, got %d", src.Len())
	}
	src.Append("c")
	if src.Len() != 3 {
		t.Fatalf("want 3, got %d", src.Len())
	}
}
