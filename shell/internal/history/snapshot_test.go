package history

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotRoundtrip is the central acceptance test for #33 + #35
// at the unit level: snapshot bytes, delete original, restore from
// snapshot, compare bytes.
func TestSnapshotRoundtrip(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	content := []byte("hello reversibility")
	if err := os.WriteFile(original, content, 0o644); err != nil {
		t.Fatal(err)
	}
	snapRoot := filepath.Join(work, "snaps")
	sn := NewSnapshotter(snapRoot, DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())

	rec, err := sn.Snapshot(original)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec.Op != OpDelete {
		t.Errorf("op = %v, want %v", rec.Op, OpDelete)
	}
	if rec.SnapshotDir == "" {
		t.Fatalf("SnapshotDir empty")
	}

	// Simulate the destructive command.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("original still exists after Remove")
	}

	// Restore.
	if err := sn.Restore(rec); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("restored bytes mismatch: got %q want %q", got, content)
	}
}

// TestSnapshotSkipsOversize verifies #34: files above the configured
// limit are recorded as skipped and not copied.
func TestSnapshotSkipsOversize(t *testing.T) {
	work := t.TempDir()
	big := filepath.Join(work, "big")
	if err := os.WriteFile(big, bytes.Repeat([]byte("X"), 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	snapRoot := filepath.Join(work, "snaps")
	sn := NewSnapshotter(snapRoot, 128, DefaultIgnoreMatcher())
	rec, err := sn.Snapshot(big)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec.Op != OpSkipped {
		t.Errorf("expected OpSkipped, got %v", rec.Op)
	}
	if rec.SkipReason != ReasonOversize {
		t.Errorf("SkipReason = %q, want %q", rec.SkipReason, ReasonOversize)
	}
	if rec.SnapshotDir != "" {
		t.Errorf("oversize file should not have a SnapshotDir, got %q", rec.SnapshotDir)
	}
}

// TestSnapshotSkipsIgnored verifies #37: gitignore-style filter
// applied at write-time.
func TestSnapshotSkipsIgnored(t *testing.T) {
	work := t.TempDir()
	nm := filepath.Join(work, "node_modules", "foo.js")
	if err := os.MkdirAll(filepath.Dir(nm), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nm, []byte("noop"), 0o644); err != nil {
		t.Fatal(err)
	}
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	rec, err := sn.Snapshot(nm)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec.Op != OpSkipped {
		t.Errorf("expected OpSkipped, got %v", rec.Op)
	}
	if rec.SkipReason != ReasonIgnored {
		t.Errorf("SkipReason = %q, want %q", rec.SkipReason, ReasonIgnored)
	}
}

// TestSnapshotMissingFile verifies a snapshot of a non-existent file
// records OpAbsent and does not error — the user may have typed `rm`
// on a path that doesn't exist; we still want the event logged.
func TestSnapshotMissingFile(t *testing.T) {
	work := t.TempDir()
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	rec, err := sn.Snapshot(filepath.Join(work, "does-not-exist"))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if rec.Op != OpAbsent {
		t.Errorf("expected OpAbsent, got %v", rec.Op)
	}
}

// TestSnapshotWalksDirectory verifies that snapshotting a directory
// path produces one record per regular file beneath it.
func TestSnapshotWalksDirectory(t *testing.T) {
	work := t.TempDir()
	d := filepath.Join(work, "tree")
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	recs, err := sn.SnapshotMany([]string{d})
	if err != nil {
		t.Fatalf("SnapshotMany: %v", err)
	}
	// Two files, plus the directory marker itself.
	want := 2
	count := 0
	for _, r := range recs {
		if r.Op == OpDelete {
			count++
		}
	}
	if count != want {
		t.Errorf("got %d snapshotted files, want %d (recs=%+v)", count, want, recs)
	}
}

// TestRestoreConflict verifies that Restore refuses when the target
// path currently exists with different bytes (the user has re-created
// the file with new content; undoing would clobber).
func TestRestoreConflict(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	if err := os.WriteFile(original, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	rec, err := sn.Snapshot(original)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Re-create with different bytes (simulates user typing past the deletion).
	if err := os.WriteFile(original, []byte("BRAND NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sn.Restore(rec); err == nil {
		t.Errorf("expected conflict error, got nil")
	}
}
