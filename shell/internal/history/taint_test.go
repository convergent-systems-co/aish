package history

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// TestRecordCommand_RedactsTainted asserts that a Pipeline with
// Tainted=true causes recordCommand to substitute the redaction
// placeholder for the raw command line. The placeholder MUST match
// the package constant.
func TestRecordCommand_RedactsTainted(t *testing.T) {
	pl := &parser.Pipeline{Tainted: true}
	const raw = "secret get TAINT_TEST | cat"
	got := recordCommand(pl, raw)
	if got != RedactedTainted {
		t.Fatalf("recordCommand on tainted pipeline = %q, want %q", got, RedactedTainted)
	}
	if strings.Contains(got, "TAINT_TEST") {
		t.Fatalf("recordCommand output contains the original name; redaction is incomplete")
	}
}

// TestRecordCommand_UntaintedPassesThrough asserts the no-op path
// preserves the raw command line verbatim.
func TestRecordCommand_UntaintedPassesThrough(t *testing.T) {
	pl := &parser.Pipeline{Tainted: false}
	const raw = "ls -la"
	got := recordCommand(pl, raw)
	if got != raw {
		t.Fatalf("recordCommand on untainted pipeline = %q, want %q", got, raw)
	}
}

// TestRecordCommand_NilPipelinePassesThrough — defensive: a nil
// pipeline must not flip the redaction (the interceptor short-circuits
// earlier on nil but tests assert the function's own contract).
func TestRecordCommand_NilPipelinePassesThrough(t *testing.T) {
	got := recordCommand(nil, "echo hi")
	if got != "echo hi" {
		t.Fatalf("recordCommand on nil pipeline = %q, want passthrough", got)
	}
}

// TestHistory_TaintedPipeline_LandsRedactedInStore is the
// end-to-end assertion that a tainted destructive pipeline writes
// the placeholder, NOT the raw line, into the events.command column.
// This is the adversarial-grep test: a downstream search for the
// secret name MUST NOT hit the events table.
func TestHistory_TaintedPipeline_LandsRedactedInStore(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// rm is in the destructive set, so Before will write an event.
	// We pre-flag the pipeline as Tainted (the shell's tagging pass
	// would do this in production) and assert the recorded command
	// is the placeholder.
	sn := NewSnapshotter(filepath.Join(tmp, "snap"), 1<<20, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)
	if h == nil {
		t.Fatalf("NewHistory returned nil")
	}
	const sentinel = "[REDACTED:test-value-do-not-log]"
	pl := parser.Pipeline{
		Commands: []parser.Command{
			{Name: "rm", Args: []string{sentinel}},
		},
		Tainted: true,
	}
	rawLine := "rm " + sentinel
	if err := h.Before(&pl, rawLine); err != nil {
		t.Fatalf("Before: %v", err)
	}
	h.After(&pl, rawLine, 0, 0)

	// Adversarial scan: every persisted event's `command` column MUST
	// NOT contain the sentinel literal.
	rows, err := store.db.Query("SELECT command FROM events")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	saw := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		saw = append(saw, c)
		if strings.Contains(c, sentinel) {
			t.Fatalf("events.command row %q contains the tainted sentinel — redaction failed", c)
		}
	}
	if len(saw) == 0 {
		t.Fatalf("expected at least one event row, got 0")
	}
	foundRedacted := false
	for _, c := range saw {
		if c == RedactedTainted {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Fatalf("no event row carried the redaction placeholder; saw %v", saw)
	}
}
