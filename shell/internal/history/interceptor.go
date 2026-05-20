package history

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// History is the PreCommand/PostCommand interceptor the Shell calls
// before and after every dispatched external command. It composes a
// Store (event log) with a Snapshotter (pre-execution file copy) and
// hides the wiring from the Shell.
//
// Threading model: the Shell is single-goroutine for v0.1; History
// inherits that. No locking — if v0.2 introduces background work, the
// underlying Store already serializes via SQLite, and the Snapshotter
// is purely file-system I/O on caller-disjoint paths.
type History struct {
	store *Store
	sn    *Snapshotter
	cwd   func() string
	// pending tracks the in-flight event ID between Before and After.
	// Per call site (Shell.dispatch) the Before/After is strictly
	// paired, so a single field is sufficient.
	pending string
	// errSink is the stderr stream for soft notices (oversize skipped,
	// snapshot dir unwritable, …). When nil, notices are silently
	// dropped.
	errSink io.Writer
}

// NewHistory wires a Store and Snapshotter into a single interceptor.
// Either argument may be nil — a nil History silently no-ops every
// callback, so the Shell can call into it unconditionally even when
// ~/.aish is unwritable.
func NewHistory(store *Store, sn *Snapshotter) *History {
	if store == nil || sn == nil {
		return nil
	}
	return &History{store: store, sn: sn, errSink: os.Stderr}
}

// SetCwdFn binds a callback that returns the shell's current working
// directory at command time. The shell holds the authoritative cwd;
// the interceptor borrows it for the event row and for canonicalizing
// relative paths. nil-safe: an unbound History uses os.Getwd.
func (h *History) SetCwdFn(fn func() string) {
	if h == nil {
		return
	}
	h.cwd = fn
}

// SetErrSink redirects the soft-notice stream. Tests pass a bytes.Buffer
// to assert on the wire; main passes os.Stderr.
func (h *History) SetErrSink(w io.Writer) {
	if h == nil {
		return
	}
	h.errSink = w
}

// Close releases the underlying store. Idempotent.
func (h *History) Close() error {
	if h == nil || h.store == nil {
		return nil
	}
	return h.store.Close()
}

// Store exposes the underlying event store for the `undo` / `restore`
// built-ins to query (LatestRestorable / SnapshotsForPath). Returns
// nil when the History was never wired up.
func (h *History) Store() *Store {
	if h == nil {
		return nil
	}
	return h.store
}

// Before runs as the PreCommand step. It inspects the pipeline, and
// when IsDestructive returns true:
//  1. Resolves each target path against the shell cwd.
//  2. Calls SnapshotMany to copy bytes.
//  3. Writes one KindSnapshot event to the store.
//  4. Stashes the event ID so After can Finalize it.
//
// Non-destructive commands produce no event (logging every `ls` would
// explode the log). Snapshot failures degrade to OpSkipped rows;
// Before NEVER returns an error that would abort the destructive
// command. The rule is "snapshot is best-effort, command is mandatory."
func (h *History) Before(pl *parser.Pipeline, line string) error {
	if h == nil || pl == nil {
		return nil
	}
	if !IsDestructive(*pl) {
		// Non-destructive: no event row, no snapshot. Track empty
		// pending so After is a no-op.
		h.pending = ""
		return nil
	}
	paths := TargetPaths(*pl)
	canon := h.canonicalize(paths)
	recs, _ := h.sn.SnapshotMany(canon)

	for _, r := range recs {
		if r.Op == OpSkipped {
			h.notify("aish: snapshot skipped %s (%s)\n", r.Path, r.SkipReason)
		}
	}

	ev := Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   line,
		Cwd:       h.currentCwd(),
		Affected:  recs,
	}
	if err := h.store.Append(&ev); err != nil {
		h.notify("aish: history append failed: %v\n", err)
		h.pending = ""
		return nil
	}
	h.pending = ev.ID
	return nil
}

// After runs as the PostCommand step. It finalizes the event row that
// Before opened, recording the destructive command's exit code and
// the wall-clock duration. A pending == "" means Before was a no-op
// (non-destructive command); After is then a no-op too.
func (h *History) After(pl *parser.Pipeline, line string, exitCode int, dur time.Duration) {
	if h == nil || h.pending == "" {
		return
	}
	id := h.pending
	h.pending = ""
	if err := h.store.Finalize(id, exitCode, dur); err != nil {
		h.notify("aish: history finalize failed: %v\n", err)
	}
}

// RestoreEvent reads the affected list off ev and restores each
// OpDelete row via the Snapshotter. Errors on individual files are
// accumulated and joined; the call succeeds only when every restore
// returns nil.
func (h *History) RestoreEvent(ev *Event) error {
	if h == nil {
		return fmt.Errorf("history: not initialized")
	}
	if ev == nil {
		return fmt.Errorf("history: nil event")
	}
	// Restore directory markers FIRST (so their children land in
	// existing dirs), then files. Order by path length asc — a
	// shorter path is shallower, hence created first.
	dirs, files := splitDirsAndFiles(ev.Affected)
	var firstErr error
	for _, d := range dirs {
		if err := h.sn.Restore(d); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, f := range files {
		if err := h.sn.Restore(f); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RestorePath looks up the most-recent OpDelete snapshot of path and
// restores it. Returns an error when there is no matching snapshot.
func (h *History) RestorePath(path string) error {
	if h == nil || h.store == nil {
		return fmt.Errorf("history: not initialized")
	}
	abs, _ := filepath.Abs(path)
	candidates := []string{abs, path}
	for _, p := range candidates {
		rec, err := h.store.SnapshotsForPath(p)
		if err != nil {
			return err
		}
		if rec != nil {
			return h.sn.Restore(*rec)
		}
	}
	return fmt.Errorf("no snapshot recorded for %s", path)
}

// canonicalize converts every input path to its absolute form
// relative to the shell's cwd, so the snapshot row can be queried by
// either form in RestorePath. A path that filepath.Abs refuses to
// touch passes through unchanged — the snapshotter will return
// OpAbsent or an OpSkipped (stat-failed) record for it.
func (h *History) canonicalize(paths []string) []string {
	out := make([]string, 0, len(paths))
	cwd := h.currentCwd()
	for _, p := range paths {
		if filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
			continue
		}
		if cwd != "" {
			out = append(out, filepath.Clean(filepath.Join(cwd, p)))
			continue
		}
		abs, err := filepath.Abs(p)
		if err == nil {
			out = append(out, abs)
			continue
		}
		out = append(out, p)
	}
	return out
}

func (h *History) currentCwd() string {
	if h == nil {
		return ""
	}
	if h.cwd != nil {
		if c := h.cwd(); c != "" {
			return c
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func (h *History) notify(format string, args ...interface{}) {
	if h == nil || h.errSink == nil {
		return
	}
	fmt.Fprintf(h.errSink, format, args...)
}

// splitDirsAndFiles partitions an affected list into directory
// markers (SnapshotDir == "") and file snapshots. Used by
// RestoreEvent so dirs are recreated before their child files.
func splitDirsAndFiles(affected []Affected) (dirs, files []Affected) {
	for _, a := range affected {
		if a.Op != OpDelete {
			continue
		}
		if a.SnapshotDir == "" {
			dirs = append(dirs, a)
		} else {
			files = append(files, a)
		}
	}
	return dirs, files
}
