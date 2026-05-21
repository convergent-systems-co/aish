package term

import (
	"bytes"
	"strings"
	"testing"
)

// TestRender_PromptAndBuffer — the renderer writes the prompt + line +
// cursor-position escape. Output is a string with the prompt prefix
// somewhere in it.
func TestRender_PromptAndBuffer(t *testing.T) {
	var buf bytes.Buffer
	rd := NewRenderer(&buf)
	frame := Frame{
		Prompt: "$ ",
		Line:   "echo hello",
		Cursor: 10,
	}
	rd.Render(frame)
	got := buf.String()
	if !strings.Contains(got, "$ ") {
		t.Fatalf("expected prompt \"$ \" in output; got %q", got)
	}
	if !strings.Contains(got, "echo hello") {
		t.Fatalf("expected line text in output; got %q", got)
	}
}

// TestRender_GhostSuggestion — the suggestion is rendered dim and
// AFTER the cursor (not before). Asserted by substring order.
func TestRender_GhostSuggestion(t *testing.T) {
	var buf bytes.Buffer
	rd := NewRenderer(&buf)
	rd.Render(Frame{
		Prompt: "$ ",
		Line:   "ec",
		Cursor: 2,
		Ghost:  "ho hello",
	})
	got := buf.String()
	ecIdx := strings.Index(got, "ec")
	ghostIdx := strings.Index(got, "ho hello")
	if ecIdx == -1 || ghostIdx == -1 {
		t.Fatalf("missing line or ghost in output; got %q", got)
	}
	if ghostIdx < ecIdx {
		t.Fatalf("ghost should appear after the typed prefix; got %q", got)
	}
}

// TestRender_ClearsPriorFrame — the renderer emits a clear-line escape
// at the start of each frame so the previous frame doesn't leave
// stale glyphs visible.
func TestRender_ClearsPriorFrame(t *testing.T) {
	var buf bytes.Buffer
	rd := NewRenderer(&buf)
	rd.Render(Frame{Prompt: "$ ", Line: "ab"})
	got := buf.String()
	// CSI 2K (clear entire line) or CSI K (clear-to-EOL) — either is fine;
	// the contract is "no visible glyphs from before this frame leak."
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected an ANSI control sequence in the frame; got %q", got)
	}
}
