package term

import "testing"

// TestGhost_PrefixMatch — empty input → no suggestion. Non-empty
// input with a history match → the rest of the matched entry as the
// suffix.
func TestGhost_PrefixMatch(t *testing.T) {
	src := NewMemorySource([]string{"echo one", "echo two", "ls"})
	cases := []struct {
		name    string
		input   string
		wantSfx string
	}{
		{name: "empty input no suggestion", input: "", wantSfx: ""},
		{name: "prefix matches most-recent", input: "ec", wantSfx: "ho two"},
		{name: "prefix matches single entry", input: "l", wantSfx: "s"},
		{name: "exact match yields empty suffix", input: "ls", wantSfx: ""},
		{name: "no match", input: "xy", wantSfx: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(src, tc.input)
			if got != tc.wantSfx {
				t.Errorf("input=%q: want %q, got %q", tc.input, tc.wantSfx, got)
			}
		})
	}
}

// TestGhost_MostRecentWins — when two entries share a prefix, the
// suggestion is the one Appended most recently.
func TestGhost_MostRecentWins(t *testing.T) {
	src := NewMemorySource(nil)
	src.Append("echo old")
	src.Append("echo new")
	got := Suggest(src, "echo ")
	if got != "new" {
		t.Fatalf("want \"new\", got %q", got)
	}
}
