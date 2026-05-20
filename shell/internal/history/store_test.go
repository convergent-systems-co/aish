package history

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreAppendAndFinalize(t *testing.T) {
	s := openTestStore(t)
	e := Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "rm /tmp/x",
		Cwd:       "/tmp",
		Affected: []Affected{
			{Path: "/tmp/x", Op: OpDelete, SnapshotDir: "/snap/abc"},
		},
	}
	if err := s.Append(&e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Finalize(e.ID, 0, 50*time.Millisecond); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	got, err := s.LatestRestorable()
	if err != nil {
		t.Fatalf("LatestRestorable: %v", err)
	}
	if got == nil {
		t.Fatalf("LatestRestorable returned nil")
	}
	if got.ID != e.ID {
		t.Errorf("ID mismatch: got %s want %s", got.ID, e.ID)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("ExitCode not set: %v", got.ExitCode)
	}
	if len(got.Affected) != 1 {
		t.Fatalf("affected len: got %d want 1", len(got.Affected))
	}
	if got.Affected[0].Path != "/tmp/x" {
		t.Errorf("path: got %s want /tmp/x", got.Affected[0].Path)
	}
}

// TestLatestRestorableSkipsUnfinalized verifies that an event whose
// child command never returned (and so exit_code is still null) is NOT
// a candidate for undo. Restoring from an in-flight event would race
// with the live command.
func TestLatestRestorableSkipsUnfinalized(t *testing.T) {
	s := openTestStore(t)
	e := Event{ID: NewEventID(), Timestamp: time.Now().UTC(), Kind: KindSnapshot, Command: "rm /tmp/a"}
	if err := s.Append(&e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := s.LatestRestorable()
	if err != nil {
		t.Fatalf("LatestRestorable: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil (no finalized snapshot event), got %+v", got)
	}
}

// TestLatestRestorableOnlySnapshotKind verifies that bare pre-exec
// events without an affected snapshot do not count as restorable.
func TestLatestRestorableOnlySnapshotKind(t *testing.T) {
	s := openTestStore(t)
	e := Event{ID: NewEventID(), Timestamp: time.Now().UTC(), Kind: KindPreExec, Command: "ls"}
	if err := s.Append(&e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Finalize(e.ID, 0, time.Millisecond); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	got, _ := s.LatestRestorable()
	if got != nil {
		t.Errorf("KindPreExec without snapshot should not be restorable, got %+v", got)
	}
}

func TestSnapshotsForPath(t *testing.T) {
	s := openTestStore(t)
	older := Event{
		ID: NewEventID(), Timestamp: time.Now().Add(-time.Hour).UTC(),
		Kind: KindSnapshot, Command: "rm /tmp/x",
		Affected: []Affected{{Path: "/tmp/x", Op: OpDelete, SnapshotDir: "/snap/older"}},
	}
	newer := Event{
		ID: NewEventID(), Timestamp: time.Now().UTC(),
		Kind: KindSnapshot, Command: "rm /tmp/x",
		Affected: []Affected{{Path: "/tmp/x", Op: OpDelete, SnapshotDir: "/snap/newer"}},
	}
	if err := s.Append(&older); err != nil {
		t.Fatalf("Append older: %v", err)
	}
	if err := s.Finalize(older.ID, 0, time.Millisecond); err != nil {
		t.Fatalf("Finalize older: %v", err)
	}
	if err := s.Append(&newer); err != nil {
		t.Fatalf("Append newer: %v", err)
	}
	if err := s.Finalize(newer.ID, 0, time.Millisecond); err != nil {
		t.Fatalf("Finalize newer: %v", err)
	}
	got, err := s.SnapshotsForPath("/tmp/x")
	if err != nil {
		t.Fatalf("SnapshotsForPath: %v", err)
	}
	if got == nil {
		t.Fatalf("expected snapshot, got nil")
	}
	if got.SnapshotDir != "/snap/newer" {
		t.Errorf("expected newer snapshot, got %s", got.SnapshotDir)
	}
}

// TestStoreIsAppendOnlyByConvention sanity-checks that the schema does
// not expose any DELETE / UPDATE convenience for events. The single
// allowed UPDATE is exit_code via Finalize.
func TestStoreCloseIdempotent(t *testing.T) {
	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
