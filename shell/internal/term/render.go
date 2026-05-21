package term

import (
	"fmt"
	"io"
	"strings"
)

// Frame is the input to one Render call: the current state the editor
// wants drawn.
//
// The editor builds a Frame on every keystroke; Renderer.Render does
// no diffing — every call emits a clear-line + redraw. This is the
// "simple-and-correct" branch from the v0.2-1 plan; if profiling shows
// it's too noisy, diffing comes in v0.2-1.1.
type Frame struct {
	Prompt string
	Line   string
	// Cursor is the rune index inside Line where the caret sits.
	Cursor int
	// Ghost is the suffix-after-Line to render dimly after the cursor.
	// May be empty.
	Ghost string
	// Spans is the highlighted breakdown of Line. When nil, Line is
	// rendered with RoleDefault.
	Spans []Span
	// Search is the optional reverse-search annotation. When
	// non-empty, the prompt is replaced by the search overlay.
	Search *SearchOverlay
}

// SearchOverlay carries the search prompt + the current match for
// rendering during Ctrl-R mode.
type SearchOverlay struct {
	Query string
	Match string
}

// Renderer writes Frames to an io.Writer using ANSI control sequences.
// Each Render call clears the current line, moves the cursor to
// column 0, writes the prompt + content, and finally positions the
// caret at the requested rune offset.
type Renderer struct {
	w io.Writer
}

// NewRenderer constructs a Renderer over w.
func NewRenderer(w io.Writer) *Renderer { return &Renderer{w: w} }

// Render emits the bytes for one frame.
func (r *Renderer) Render(f Frame) {
	var b strings.Builder
	// Carriage return + clear line.
	b.WriteString("\r\x1b[2K")
	if f.Search != nil {
		b.WriteString("(reverse-i-search)`")
		b.WriteString(f.Search.Query)
		b.WriteString("': ")
		b.WriteString(f.Search.Match)
		// caret at end of match.
		_, _ = io.WriteString(r.w, b.String())
		return
	}
	b.WriteString(f.Prompt)
	if len(f.Spans) > 0 {
		for _, sp := range f.Spans {
			b.WriteString(applyRole(sp.Role, sp.Text))
		}
	} else {
		b.WriteString(f.Line)
	}
	if f.Ghost != "" {
		b.WriteString(applyRole(RoleGhost, f.Ghost))
	}
	// Move the caret back from the end-of-line to the cursor
	// position. ANSI CUB N moves N columns left.
	lineRunes := []rune(f.Line)
	tail := len(lineRunes) - f.Cursor
	ghostLen := len([]rune(f.Ghost))
	totalTail := tail + ghostLen
	if totalTail > 0 {
		fmt.Fprintf(&b, "\x1b[%dD", totalTail)
	}
	_, _ = io.WriteString(r.w, b.String())
}

// applyRole wraps text in the ANSI escapes for `role`. The v0.2-1
// implementation hard-codes a minimal palette so the term package
// does not depend on shell/internal/theme; a future change can pivot
// this to read theme.Active().ColorFor(role) so theme switches recolor
// the editor.
func applyRole(role Role, text string) string {
	if text == "" {
		return text
	}
	switch role {
	case RoleAccent:
		return "\x1b[33m" + text + "\x1b[0m" // yellow
	case RoleString:
		return "\x1b[32m" + text + "\x1b[0m" // green
	case RoleAITierLocal:
		return "\x1b[32m" + text + "\x1b[0m" // green
	case RoleAITierCloud:
		return "\x1b[34m" + text + "\x1b[0m" // blue
	case RoleGhost:
		return "\x1b[2m" + text + "\x1b[0m" // dim
	case RoleSearchPrompt:
		return "\x1b[36m" + text + "\x1b[0m" // cyan
	}
	return text
}
