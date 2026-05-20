package history

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// TestInterceptorEndToEnd is the unit-level integration of the History
// interceptor: parse line -> detector -> snapshotter -> store, then
// undo path: store.LatestRestorable -> snapshotter.Restore.
func TestInterceptorEndToEnd(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	content := []byte("interceptor roundtrip")
	if err := os.WriteFile(original, content, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(work, "history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	cfg := Config{SnapshotMaxBytes: DefaultSnapshotMaxBytes}
	h := NewHistory(store, NewSnapshotter(filepath.Join(work, "snaps"), cfg.SnapshotMaxBytes, DefaultIgnoreMatcher()))

	pl, err := parser.Parse("rm " + original)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Before(&pl, "rm "+original); err != nil {
		t.Fatalf("Before: %v", err)
	}
	// Simulate the destructive command.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	h.After(&pl, "rm "+original, 0, 5*time.Millisecond)

	// Undo path — pull the latest restorable event and restore.
	ev, err := store.LatestRestorable()
	if err != nil {
		t.Fatalf("LatestRestorable: %v", err)
	}
	if ev == nil {
		t.Fatalf("no restorable event recorded")
	}
	if err := h.RestoreEvent(ev); err != nil {
		t.Fatalf("RestoreEvent: %v", err)
	}
	got, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("roundtrip mismatch: got %q want %q", got, content)
	}
}

// TestInterceptorSkipsNonDestructive verifies the Before path is a
// no-op (no snapshot, no event) for harmless commands. Logging every
// `ls` would explode the event log.
func TestInterceptorSkipsNonDestructive(t *testing.T) {
	work := t.TempDir()
	store, err := Open(filepath.Join(work, "history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	h := NewHistory(store, NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher()))
	pl, _ := parser.Parse("ls -la")
	if err := h.Before(&pl, "ls -la"); err != nil {
		t.Fatalf("Before: %v", err)
	}
	h.After(&pl, "ls -la", 0, time.Millisecond)
	ev, _ := store.LatestRestorable()
	if ev != nil {
		t.Errorf("non-destructive command should not produce a restorable event, got %+v", ev)
	}
}

// TestInterceptorTolerantOfSnapshotFailure verifies that a snapshot
// error (e.g. permission denied on the snapshot dir) does NOT abort
// the destructive command — the rule is "snapshot is best-effort,
// command is mandatory." The event row records the failure.
func TestInterceptorTolerantOfSnapshotFailure(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	if err := os.WriteFile(original, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(work, "history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	// Point snapshot root at a path we cannot create (a regular file).
	blocker := filepath.Join(work, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sn := NewSnapshotter(blocker, DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)
	pl, _ := parser.Parse("rm " + original)
	// Before MUST NOT return an error that aborts the shell.
	if err := h.Before(&pl, "rm "+original); err != nil {
		t.Errorf("Before should not error on snapshot failure, got %v", err)
	}
}
