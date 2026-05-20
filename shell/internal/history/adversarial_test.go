package history

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// TestAdversarial_DetectionFiresBeforeExec verifies the snapshot is
// already on disk by the time IsDestructive returns true and Before
// completes. In production the rm runs immediately after Before
// returns; the snapshot MUST exist by then or undo loses data.
func TestAdversarial_DetectionFiresBeforeExec(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	if err := os.WriteFile(original, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := Open(filepath.Join(work, "history.db"))
	defer store.Close()
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)
	pl, _ := parser.Parse("rm " + original)

	if err := h.Before(&pl, "rm "+original); err != nil {
		t.Fatalf("Before: %v", err)
	}
	// At this point, the original is still on disk (rm hasn't run yet)
	// AND a snapshot copy must already exist somewhere under snaps/.
	entries, err := os.ReadDir(filepath.Join(work, "snaps"))
	if err != nil {
		t.Fatalf("snap dir missing: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("Before returned but no snapshot was created")
	}
	// Verify the snapshot contains the original bytes — i.e., the
	// snapshot is real, not just a touched empty marker.
	for _, e := range entries {
		dir := filepath.Join(work, "snaps", e.Name())
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				t.Errorf("read snapshot %s: %v", f.Name(), err)
				continue
			}
			if !bytes.Equal(data, []byte("hello")) {
				t.Errorf("snapshot bytes mismatch: %q", data)
			}
		}
	}
}

// TestAdversarial_SnapshotDeterminism verifies that snapshotting the
// same file twice with the same content produces snapshots whose
// SHA256 is identical — i.e., the SHA256 column is reliable for
// integrity checks.
func TestAdversarial_SnapshotDeterminism(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	if err := os.WriteFile(original, []byte("constant content"), 0o644); err != nil {
		t.Fatal(err)
	}
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	r1, err := sn.Snapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := sn.Snapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	if r1.SHA256 != r2.SHA256 {
		t.Errorf("sha256 should match for identical content: %s vs %s", r1.SHA256, r2.SHA256)
	}
	if r1.SHA256 == "" {
		t.Errorf("sha256 must not be empty for OpDelete")
	}
	// The snapshot directories MUST differ — most-recent-wins semantics
	// rely on distinct directories per event.
	if r1.SnapshotDir == r2.SnapshotDir {
		t.Errorf("snapshot dirs collided: %s", r1.SnapshotDir)
	}
}

// TestAdversarial_UndoActuallyRestoresBytes verifies the full
// roundtrip with a non-trivial payload. "Just shuffle bytes" would
// only show up as a SHA256 column changing; assert the live file
// bytes match.
func TestAdversarial_UndoActuallyRestoresBytes(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "payload.bin")
	payload := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 1024) // 4 KiB
	if err := os.WriteFile(original, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := Open(filepath.Join(work, "history.db"))
	defer store.Close()
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)
	pl, _ := parser.Parse("rm " + original)
	if err := h.Before(&pl, "rm "+original); err != nil {
		t.Fatal(err)
	}
	// Simulate exec.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	h.After(&pl, "rm "+original, 0, time.Millisecond)
	ev, _ := store.LatestRestorable()
	if ev == nil {
		t.Fatal("no restorable event")
	}
	if err := h.RestoreEvent(ev); err != nil {
		t.Fatalf("RestoreEvent: %v", err)
	}
	got, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch after undo (len=%d/%d)", len(got), len(payload))
	}
}

// TestAdversarial_IgnoredPathNotRestorable verifies that an ignored
// file path returns no result on RestorePath — the gitignore filter
// is honored end-to-end, not just on the write side.
func TestAdversarial_IgnoredPathNotRestorable(t *testing.T) {
	work := t.TempDir()
	nmDir := filepath.Join(work, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(nmDir, "x.js")
	if err := os.WriteFile(ignored, []byte("library code"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := Open(filepath.Join(work, "history.db"))
	defer store.Close()
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)

	pl, _ := parser.Parse("rm " + ignored)
	if err := h.Before(&pl, "rm "+ignored); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(ignored)
	h.After(&pl, "rm "+ignored, 0, time.Millisecond)

	// LatestRestorable: must return nil because the only event has
	// no OpDelete-with-bytes Affected rows.
	ev, _ := store.LatestRestorable()
	if ev != nil {
		t.Errorf("ignored path should not produce a restorable event, got %+v", ev)
	}
	// SnapshotsForPath: must return nil — there's no OpDelete row.
	rec, _ := store.SnapshotsForPath(ignored)
	if rec != nil {
		t.Errorf("ignored path should not be findable by path, got %+v", rec)
	}
}

// TestAdversarial_OversizeBlocksSnapshotButNotCommand verifies #34:
// the 100MB default (here tightened to 64 bytes) prevents the
// snapshot but does not abort the destructive command's caller from
// proceeding. The event row records OpSkipped/oversize.
func TestAdversarial_OversizeBlocksSnapshotButNotCommand(t *testing.T) {
	work := t.TempDir()
	big := filepath.Join(work, "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte("X"), 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := Open(filepath.Join(work, "history.db"))
	defer store.Close()
	sn := NewSnapshotter(filepath.Join(work, "snaps"), 64, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)
	pl, _ := parser.Parse("rm " + big)
	if err := h.Before(&pl, "rm "+big); err != nil {
		t.Fatal(err)
	}
	// Confirm no snapshot file was written.
	entries, _ := os.ReadDir(filepath.Join(work, "snaps"))
	for _, e := range entries {
		files, _ := os.ReadDir(filepath.Join(work, "snaps", e.Name()))
		if len(files) > 0 {
			t.Errorf("oversize file should not have been copied: %v", files)
		}
	}
}

// TestAdversarial_RestoreVerifiesSHA256Integrity verifies that a
// hand-corrupted snapshot (someone edited the bytes in ~/.aish/snapshots/)
// is detected by Restore and rejected with an error — not silently
// clobbering the live path with bad data.
func TestAdversarial_RestoreVerifiesSHA256Integrity(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	if err := os.WriteFile(original, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	rec, err := sn.Snapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the snapshot file in place.
	snapFile := filepath.Join(rec.SnapshotDir, filepath.Base(original))
	if err := os.WriteFile(snapFile, []byte("CORRUPT"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Delete original.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	// Restore MUST fail — would otherwise put 'CORRUPT' on disk.
	if err := sn.Restore(rec); err == nil {
		t.Errorf("expected SHA mismatch error, got nil")
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Errorf("Restore should not have created the file after a SHA mismatch")
	}
}

// TestAdversarial_DestructiveInPipelineStillSnapshots verifies the
// `ls | rm` edge case — destructive at a non-leading pipeline stage.
// Snapshotting MUST still occur.
func TestAdversarial_DestructiveInPipelineStillSnapshots(t *testing.T) {
	work := t.TempDir()
	original := filepath.Join(work, "x.txt")
	if err := os.WriteFile(original, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := Open(filepath.Join(work, "history.db"))
	defer store.Close()
	sn := NewSnapshotter(filepath.Join(work, "snaps"), DefaultSnapshotMaxBytes, DefaultIgnoreMatcher())
	h := NewHistory(store, sn)
	// Synthesize a pipeline where rm is in stage 2.
	pl := parser.Pipeline{Commands: []parser.Command{
		{Name: "ls"},
		{Name: "rm", Args: []string{original}},
	}}
	if err := h.Before(&pl, "ls | rm "+original); err != nil {
		t.Fatal(err)
	}
	ev, _ := store.LatestRestorable()
	// Pre-Finalize the event so LatestRestorable sees it.
	if ev != nil {
		t.Errorf("event should not be restorable yet (not finalized)")
	}
	h.After(&pl, "ls | rm "+original, 0, time.Millisecond)
	ev, _ = store.LatestRestorable()
	if ev == nil {
		t.Errorf("pipelined destructive should produce restorable event")
	}
}
