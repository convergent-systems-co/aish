package history

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestEventJSONShape locks the wire shape of an Event so the JSON keys
// can be consumed by future tooling (a `aish history` renderer, an
// external auditor) without code change. The acceptance criterion in
// .artifacts/plans/v0.1-4.md #30 specifies: id, ts, kind, command, cwd,
// exit_code, duration_ms, affected[].
func TestEventJSONShape(t *testing.T) {
	e := Event{
		ID:         "evt_test",
		Timestamp:  time.Date(2026, 5, 20, 14, 32, 11, 0, time.UTC),
		Kind:       KindSnapshot,
		Command:    "rm /tmp/x",
		Cwd:        "/tmp",
		ExitCode:   intPtr(0),
		DurationMS: 12,
		Affected: []Affected{
			{Path: "/tmp/x", Op: OpDelete, SnapshotDir: "/snap/abc"},
		},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	required := []string{`"id"`, `"ts"`, `"kind"`, `"command"`, `"cwd"`,
		`"exit_code"`, `"duration_ms"`, `"affected"`, `"path"`, `"op"`, `"snapshot_dir"`}
	for _, k := range required {
		if !strings.Contains(s, k) {
			t.Errorf("missing JSON key %s in %s", k, s)
		}
	}
}

// TestEventJSONNilExitCode confirms exit_code is emitted as JSON null
// before the command runs (the "still in flight" event state). undo's
// LatestRestorable query filters on exit_code != null; the encoding
// must round-trip nil correctly.
func TestEventJSONNilExitCode(t *testing.T) {
	e := Event{
		ID:        "evt_test",
		Timestamp: time.Now().UTC(),
		Kind:      KindPreExec,
		Command:   "rm /tmp/x",
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"exit_code":null`) {
		t.Errorf("expected exit_code:null, got %s", b)
	}
}

// TestNewEventIDIsUnique verifies the generator produces distinct IDs.
// Collision in this generator is fatal — undo relies on event IDs to
// identify the row to read back from.
func TestNewEventIDIsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		id := NewEventID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate event id at iteration %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

func intPtr(i int) *int { return &i }
