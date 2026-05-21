package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDestructive_MvCommandIsDestructive(t *testing.T) {
	pl := parse(t, "mv /a /b")
	if !IsDestructive(pl) {
		t.Fatalf("mv pipeline should be classified destructive in v0.3-4")
	}
}

func TestTargetPaths_MvEmitsSourcesOnly(t *testing.T) {
	pl := parse(t, "mv /tmp/a /tmp/b")
	got := TargetPaths(pl)
	if len(got) != 1 || got[0] != "/tmp/a" {
		t.Fatalf("TargetPaths(mv) = %v, want [/tmp/a]", got)
	}
}

func TestRenameTargets(t *testing.T) {
	cases := []struct {
		line string
		want [][2]string
	}{
		{"mv /a /b", [][2]string{{"/a", "/b"}}},
		{"mv /a /b /dst", [][2]string{{"/a", "/dst"}, {"/b", "/dst"}}},
		{"rm /a", nil},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got := RenameTargets(parse(t, tc.line))
			if len(got) != len(tc.want) {
				t.Fatalf("len(RenameTargets) = %d, want %d (got %v)", len(got), len(tc.want), got)
			}
			for i, pair := range tc.want {
				if got[i] != pair {
					t.Errorf("[%d] got %v want %v", i, got[i], pair)
				}
			}
		})
	}
}

func TestSnapshotMove_FileToNewPath_CapturesSourceBytes(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap")
	src := filepath.Join(root, "src.txt")
	dst := filepath.Join(root, "dst.txt")
	if err := os.WriteFile(src, []byte("source-bytes"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	sn := NewSnapshotter(snapRoot, DefaultSnapshotMaxBytes, nil)
	recs := sn.SnapshotMove(src, dst)
	// Expect exactly one row: OpRename for the source. dst doesn't
	// exist yet so no OpModify row is emitted.
	if len(recs) != 1 {
		t.Fatalf("SnapshotMove len = %d, want 1; recs=%+v", len(recs), recs)
	}
	if recs[0].Op != OpRename {
		t.Fatalf("recs[0].Op = %q, want %q", recs[0].Op, OpRename)
	}
	if recs[0].Path != src || recs[0].RenameTarget != dst {
		t.Fatalf("recs[0] = %+v, want path=%s target=%s", recs[0], src, dst)
	}
	if recs[0].SnapshotDir == "" || recs[0].SHA256 == "" {
		t.Fatalf("recs[0] missing snapshot dir / sha256: %+v", recs[0])
	}
}

func TestSnapshotMove_FileToExistingFile_EmitsModifyRow(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap")
	src := filepath.Join(root, "src.txt")
	dst := filepath.Join(root, "dst.txt")
	if err := os.WriteFile(src, []byte("new-bytes"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old-dst-bytes"), 0o600); err != nil {
		t.Fatalf("write dst: %v", err)
	}
	sn := NewSnapshotter(snapRoot, DefaultSnapshotMaxBytes, nil)
	recs := sn.SnapshotMove(src, dst)
	// Two rows: OpRename for src, OpModify for dst's prior bytes.
	if len(recs) != 2 {
		t.Fatalf("SnapshotMove len = %d, want 2; recs=%+v", len(recs), recs)
	}
	if recs[0].Op != OpRename {
		t.Fatalf("first row op = %q, want OpRename", recs[0].Op)
	}
	if recs[1].Op != OpModify {
		t.Fatalf("second row op = %q, want OpModify", recs[1].Op)
	}
	if recs[1].Path != dst {
		t.Fatalf("modify row path = %q, want %q", recs[1].Path, dst)
	}
}

func TestSnapshotMove_FileToExistingDirectory_ResolvesTarget(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap")
	src := filepath.Join(root, "src.txt")
	dstDir := filepath.Join(root, "dst")
	if err := os.WriteFile(src, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	sn := NewSnapshotter(snapRoot, DefaultSnapshotMaxBytes, nil)
	recs := sn.SnapshotMove(src, dstDir)
	if len(recs) != 1 {
		t.Fatalf("SnapshotMove len = %d, want 1; recs=%+v", len(recs), recs)
	}
	wantTarget := filepath.Join(dstDir, "src.txt")
	if recs[0].RenameTarget != wantTarget {
		t.Fatalf("rename target = %q, want %q", recs[0].RenameTarget, wantTarget)
	}
}

func TestRestore_OpRename_PutsBytesBackAtSource(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap")
	src := filepath.Join(root, "src.txt")
	dst := filepath.Join(root, "dst.txt")
	if err := os.WriteFile(src, []byte("source-bytes"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	sn := NewSnapshotter(snapRoot, DefaultSnapshotMaxBytes, nil)
	recs := sn.SnapshotMove(src, dst)

	// Simulate the actual mv: rename src to dst.
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Restore should put bytes back at src and remove dst (because dst
	// now holds the same bytes we just put back).
	if err := sn.Restore(recs[0]); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read src after restore: %v", err)
	}
	if string(got) != "source-bytes" {
		t.Fatalf("src bytes = %q, want %q", got, "source-bytes")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dst should be removed after rename-restore, stat err = %v", err)
	}
}

func TestRestore_OpModify_RollsBackDestinationBytes(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap")
	src := filepath.Join(root, "src.txt")
	dst := filepath.Join(root, "dst.txt")
	if err := os.WriteFile(src, []byte("new-content"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("original-dst"), 0o600); err != nil {
		t.Fatalf("write dst: %v", err)
	}
	sn := NewSnapshotter(snapRoot, DefaultSnapshotMaxBytes, nil)
	recs := sn.SnapshotMove(src, dst)

	// Find the OpModify row.
	var modRec *Affected
	for i := range recs {
		if recs[i].Op == OpModify {
			modRec = &recs[i]
			break
		}
	}
	if modRec == nil {
		t.Fatalf("no OpModify row produced; recs=%+v", recs)
	}

	// Simulate mv -f: dst now has src's bytes.
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Restoring the modify row should put dst's ORIGINAL bytes back.
	if err := sn.Restore(*modRec); err != nil {
		t.Fatalf("Restore OpModify: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "original-dst" {
		t.Fatalf("dst bytes after modify-restore = %q, want %q", got, "original-dst")
	}
}
