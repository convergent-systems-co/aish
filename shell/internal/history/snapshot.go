package history

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Snapshotter copies file bytes to ~/.aish/snapshots/<UTC>-<sha1(path)>/<basename>
// before a destructive command runs and restores them on demand.
//
// Storage shape (per the Alternatives Table in
// .artifacts/plans/v0.1-4.md): raw file copies — no CAS, no
// deduplication, no compression. The simplest restorable representation
// the file system offers.
type Snapshotter struct {
	// root is the snapshots directory — typically
	// $HOME/.aish/snapshots/. Created on first write.
	root string
	// maxBytes is the per-file limit (#34). Files larger than this
	// are recorded as OpSkipped with SkipReason == ReasonOversize.
	maxBytes int64
	// ignore is the gitignore-style filter (#37). nil-safe: a nil
	// matcher never skips anything.
	ignore *Matcher
	// nowFn is the time source. Overridable in tests; defaults to
	// time.Now.
	nowFn func() time.Time
}

// NewSnapshotter constructs a Snapshotter with the given root,
// per-file size limit, and ignore matcher. None of the arguments are
// validated up front — root is created lazily on first write so a
// read-only HOME does not block shell startup.
func NewSnapshotter(root string, maxBytes int64, ignore *Matcher) *Snapshotter {
	if maxBytes <= 0 {
		maxBytes = DefaultSnapshotMaxBytes
	}
	return &Snapshotter{
		root:     root,
		maxBytes: maxBytes,
		ignore:   ignore,
		nowFn:    time.Now,
	}
}

// Snapshot copies one path to the snapshot root and returns the
// Affected row describing the outcome.
//
// Behaviors:
//   - File does not exist  → OpAbsent.
//   - File is a directory  → call SnapshotMany internally; the
//     returned Affected is a "directory marker" with Op=OpDelete,
//     SnapshotDir empty, SHA256 empty (the children carry the data).
//     Callers that care about the per-file detail should use
//     SnapshotMany directly.
//   - Path is filtered     → OpSkipped, ReasonIgnored.
//   - File exceeds maxBytes → OpSkipped, ReasonOversize.
//   - Otherwise             → OpDelete with SnapshotDir populated.
//
// Errors are returned only for unrecoverable I/O on the snapshot root
// (e.g. cannot mkdir). Per-file errors degrade to OpSkipped so the
// destructive command is never aborted.
func (s *Snapshotter) Snapshot(path string) (Affected, error) {
	if s == nil {
		return Affected{}, errors.New("history: nil Snapshotter")
	}
	if s.ignore != nil && s.ignore.Match(path) {
		return Affected{Path: path, Op: OpSkipped, SkipReason: ReasonIgnored}, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Affected{Path: path, Op: OpAbsent}, nil
		}
		// Permission errors, etc.: degrade to OpSkipped rather than
		// aborting the destructive command.
		return Affected{Path: path, Op: OpSkipped, SkipReason: "stat-failed"}, nil
	}
	if st.IsDir() {
		// A directory snapshot is a marker row; the recursive
		// per-file work is owned by SnapshotMany.
		return Affected{Path: path, Op: OpDelete}, nil
	}
	if st.Size() > s.maxBytes {
		return Affected{Path: path, Op: OpSkipped, SkipReason: ReasonOversize, Bytes: st.Size()}, nil
	}
	dir, err := s.makeSnapshotDir(path)
	if err != nil {
		return Affected{Path: path, Op: OpSkipped, SkipReason: "mkdir-failed"}, nil
	}
	dest := filepath.Join(dir, filepath.Base(path))
	sum, n, err := copyAndDigest(path, dest)
	if err != nil {
		// Clean up the partial directory; degrade to skipped.
		_ = os.RemoveAll(dir)
		return Affected{Path: path, Op: OpSkipped, SkipReason: "copy-failed"}, nil
	}
	// Preserve mtime on the snapshot so an inspector can see the
	// original file's age, not the snapshot creation time.
	_ = os.Chtimes(dest, st.ModTime(), st.ModTime())
	return Affected{
		Path:        path,
		Op:          OpDelete,
		SnapshotDir: dir,
		SHA256:      sum,
		Bytes:       n,
	}, nil
}

// SnapshotMany walks each input path. For files it delegates to
// Snapshot. For directories it walks the tree and snapshots every
// regular file — returning one Affected per file plus an OpDelete
// marker for the directory itself (so undo knows to recreate it).
//
// Errors are accumulated as OpSkipped records; the return error is
// reserved for catastrophic failures that block all paths.
func (s *Snapshotter) SnapshotMany(paths []string) ([]Affected, error) {
	var out []Affected
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			out = append(out, Affected{Path: p, Op: OpAbsent})
			continue
		}
		if !st.IsDir() {
			rec, _ := s.Snapshot(p)
			out = append(out, rec)
			continue
		}
		// Directory marker (so undo can mkdir it back if the rm -rf
		// removed an empty subdir).
		out = append(out, Affected{Path: p, Op: OpDelete})
		_ = filepath.WalkDir(p, func(child string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || child == p {
				return nil
			}
			if d.IsDir() {
				out = append(out, Affected{Path: child, Op: OpDelete})
				return nil
			}
			rec, _ := s.Snapshot(child)
			out = append(out, rec)
			return nil
		})
	}
	return out, nil
}

// SnapshotMove records the bytes touched by a `mv src dst` operation.
// Behavior:
//   - SRC bytes are snapshotted (OpRename, RenameTarget=DST).
//   - When DST already exists as a regular file, its bytes are
//     snapshotted too (OpModify) so a future restore can roll back
//     the overwrite separately from the rename.
//   - When DST is an existing directory, the actual move places SRC
//     inside it; the recorded DST is filepath.Join(DST, base(SRC))
//     so a later RestoreEvent moves the right bytes back.
//
// The function returns the slice of Affected rows. As with SnapshotMany,
// per-file failure degrades to OpSkipped — never an error that would
// abort the destructive command.
func (s *Snapshotter) SnapshotMove(src, dst string) []Affected {
	if s == nil {
		return nil
	}
	var out []Affected

	srcStat, srcErr := os.Stat(src)
	resolvedDst := dst
	if dstStat, err := os.Stat(dst); err == nil && dstStat.IsDir() {
		// `mv SRC DSTDIR` → effective target is DSTDIR/base(SRC).
		resolvedDst = filepath.Join(dst, filepath.Base(src))
	}

	if srcErr != nil {
		if errors.Is(srcErr, fs.ErrNotExist) {
			// Mv of a non-existent source — record the absent row;
			// mv itself will fail and the user sees the error.
			out = append(out, Affected{
				Path: src, Op: OpAbsent, RenameTarget: resolvedDst,
			})
		} else {
			out = append(out, Affected{
				Path: src, Op: OpSkipped, SkipReason: "stat-failed",
				RenameTarget: resolvedDst,
			})
		}
	} else if srcStat.IsDir() {
		// Directory rename: emit one marker for the dir itself, then
		// walk and snapshot each regular file inside. This mirrors
		// SnapshotMany's directory handling — the marker row drives
		// MkdirAll on restore, the children carry the bytes.
		out = append(out, Affected{Path: src, Op: OpRename, RenameTarget: resolvedDst})
		_ = filepath.WalkDir(src, func(child string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || child == src {
				return nil
			}
			if d.IsDir() {
				out = append(out, Affected{Path: child, Op: OpDelete})
				return nil
			}
			rec, _ := s.Snapshot(child)
			out = append(out, rec)
			return nil
		})
	} else {
		// Regular file: copy bytes, emit OpRename row.
		rec, _ := s.Snapshot(src)
		// Snapshot returns OpDelete on success; we promote to
		// OpRename and attach the target.
		if rec.Op == OpDelete {
			rec.Op = OpRename
			rec.RenameTarget = resolvedDst
		} else {
			// Preserve OpSkipped / OpAbsent; just carry the target.
			rec.RenameTarget = resolvedDst
		}
		out = append(out, rec)
	}

	// If the resolved destination already exists, snapshot its prior
	// bytes as an OpModify so a future restore can decide whether to
	// roll back just the overwrite. Mv of a directory-onto-directory
	// is a rare edge case POSIX rejects; we don't try to snapshot
	// the destination tree there.
	if dstStat, err := os.Stat(resolvedDst); err == nil && !dstStat.IsDir() {
		modRec, _ := s.Snapshot(resolvedDst)
		if modRec.Op == OpDelete {
			modRec.Op = OpModify
		}
		out = append(out, modRec)
	}

	return out
}

// Restore copies the snapshotted bytes back to rec.Path. Returns an
// error when:
//   - the snapshot file has rotted (sha256 mismatch against rec.SHA256)
//   - rec.Path currently exists with different bytes (conflict —
//     the user has reused the path; clobbering is unsafe)
//
// The directory-marker case (Op=OpDelete with empty SnapshotDir) is
// handled by MkdirAll: it re-creates the empty directory.
//
// OpRename restores walk the bytes from the snapshot back to rec.Path,
// effectively "un-renaming" the file. If a RenameTarget exists on
// disk and its bytes match what we snapshotted, it is removed first
// — otherwise the user would end up with two copies. OpModify
// restores walk the snapshot back to rec.Path; the live bytes there
// (the post-mv content) get clobbered after a sha256 mismatch check
// — same conflict-guard logic as OpDelete.
func (s *Snapshotter) Restore(rec Affected) error {
	if rec.Op == OpRename {
		return s.restoreRename(rec)
	}
	if rec.Op == OpModify {
		// Modification snapshots carry pre-modification bytes and
		// put them back at rec.Path. The conflict guard inside
		// restoreFile is intentionally bypassed here: the live bytes
		// at rec.Path are EXPECTED to differ from the snapshot
		// (that's the modification we're undoing). The caller has
		// asked for the rollback explicitly.
		return s.restoreFileForceful(rec)
	}
	if rec.Op != OpDelete {
		return fmt.Errorf("history: Restore: cannot restore op=%s", rec.Op)
	}
	if rec.SnapshotDir == "" {
		// Directory marker — re-create the directory if it does not
		// exist. We do not error if it does; the children's restores
		// rely on it being there.
		return os.MkdirAll(rec.Path, 0o755)
	}
	return s.restoreFile(rec)
}

// restoreFile is the shared body for OpDelete restores: copy the
// snapshotted bytes back to rec.Path with the SHA256 verification +
// conflict-guard rules. The conflict guard refuses to clobber a path
// whose current bytes differ from rec.SHA256 — this is the right
// posture for OpDelete (the path was supposed to be gone; any
// content is the user actively reusing the path).
func (s *Snapshotter) restoreFile(rec Affected) error {
	return s.restoreFileImpl(rec, false)
}

// restoreFileForceful is the OpModify / OpRename-target variant. The
// caller has explicitly asked to roll back a modification, so the
// "live bytes differ from snapshot" case is EXPECTED, not a conflict
// — the guard is skipped. SHA256-rot detection on the snapshot
// itself is still applied so a corrupted snapshot cannot silently
// damage live data.
func (s *Snapshotter) restoreFileForceful(rec Affected) error {
	return s.restoreFileImpl(rec, true)
}

// restoreFileImpl is the shared body parameterized by forceClobber.
func (s *Snapshotter) restoreFileImpl(rec Affected, forceClobber bool) error {
	if rec.SnapshotDir == "" {
		return fmt.Errorf("history: Restore: %s missing snapshot_dir", rec.Path)
	}
	src := filepath.Join(rec.SnapshotDir, filepath.Base(rec.Path))
	if rec.SHA256 != "" {
		live, _, err := copyAndDigest(src, "")
		if err != nil {
			return fmt.Errorf("history: Restore: read snapshot: %w", err)
		}
		if live != rec.SHA256 {
			return fmt.Errorf("history: Restore: snapshot digest mismatch (rotted)")
		}
	}
	if !forceClobber {
		if _, err := os.Stat(rec.Path); err == nil {
			liveSum, _, err := copyAndDigest(rec.Path, "")
			if err == nil && liveSum != rec.SHA256 {
				return fmt.Errorf("history: Restore: %s exists with different bytes", rec.Path)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(rec.Path), 0o755); err != nil {
		return fmt.Errorf("history: Restore: mkdir parent: %w", err)
	}
	if _, _, err := copyAndDigest(src, rec.Path); err != nil {
		return fmt.Errorf("history: Restore: copy back: %w", err)
	}
	return nil
}

// restoreRename undoes a `mv SRC DST` by putting the snapshotted bytes
// back at SRC (rec.Path) and clearing DST when its content matches
// what we put there. If DST has been further modified after the mv,
// it stays in place — the modify chain restore would put its prior
// version back.
//
// The directory-marker case (Op=OpRename with empty SnapshotDir) is
// handled by MkdirAll on rec.Path, mirroring OpDelete's marker
// behavior.
func (s *Snapshotter) restoreRename(rec Affected) error {
	if rec.SnapshotDir == "" {
		// Directory rename marker: ensure rec.Path exists. The
		// children carried by sibling rows will re-populate it.
		if err := os.MkdirAll(rec.Path, 0o755); err != nil {
			return fmt.Errorf("history: Restore: mkdir rename src: %w", err)
		}
		// Best-effort: remove the post-rename target if it is now an
		// empty directory.
		if rec.RenameTarget != "" {
			_ = os.Remove(rec.RenameTarget)
		}
		return nil
	}
	if err := s.restoreFile(rec); err != nil {
		return err
	}
	// Tidy: if the post-rename target still holds the exact bytes we
	// just put back at rec.Path, the user has effectively two copies
	// — keep the restored source, remove the duplicate target.
	if rec.RenameTarget != "" && rec.SHA256 != "" {
		if _, err := os.Stat(rec.RenameTarget); err == nil {
			liveSum, _, err := copyAndDigest(rec.RenameTarget, "")
			if err == nil && liveSum == rec.SHA256 {
				_ = os.Remove(rec.RenameTarget)
			}
		}
	}
	return nil
}

// makeSnapshotDir returns a fresh per-snapshot directory under root.
// Layout: <root>/<UTC-ISO-8601-with-nanos>-<sha1(path)[:12]>. Flat —
// no nested mirroring of the original tree — so the snapshot store
// stays inspectable with a single `ls`.
//
// Timestamp resolution is nanoseconds (not seconds) so two snapshots
// of the same path in the same wall-clock second do not collide
// inside a single directory. Without nanosecond precision the second
// snapshot's bytes would overwrite the first — losing the older
// version that an `aish restore <path>` might want to walk back to.
// (Adversarial finding caught by TestAdversarial_SnapshotDeterminism.)
func (s *Snapshotter) makeSnapshotDir(origPath string) (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", err
	}
	now := s.nowFn().UTC()
	ts := now.Format("20060102T150405.000000000Z")
	h := sha1.Sum([]byte(origPath))
	name := ts + "-" + hex.EncodeToString(h[:6]) // 12-char short hash
	dir := filepath.Join(s.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// copyAndDigest reads src and (if dest != "") writes dest, returning
// the sha256 of the bytes and the byte count. When dest is empty
// (verification path), nothing is written. Errors from Open / io.Copy
// surface to the caller.
func copyAndDigest(src, dest string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	h := sha256.New()
	var out io.Writer = h
	var df *os.File
	if dest != "" {
		df, err = os.Create(dest)
		if err != nil {
			return "", 0, err
		}
		defer df.Close()
		out = io.MultiWriter(h, df)
	}
	n, err := io.Copy(out, in)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
