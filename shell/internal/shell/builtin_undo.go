package shell

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/convergent-systems-co/aish/shell/internal/history"
)

// undoBuiltin implements `undo`. Reads the most-recent restorable
// event from the history store and replays each snapshotted path
// back to its original location. Returns a non-zero exit code on
// any failure (no event, restore conflict, snapshot rot).
//
// `undo N` (revert last N operations) is described in GOALS.md but
// out of scope for v0.1; an extra argument is currently rejected
// with a clear message so the user does not silently get a single-
// step undo when they typed `undo 3`.
func (s *Shell) undoBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "aish: undo: arguments not supported in v0.1 (got %v); see GOALS.md\n", args)
		return 2
	}
	if s.history == nil {
		fmt.Fprintln(stderr, "aish: undo: history not available")
		return 1
	}
	ev, err := s.history.Store().LatestRestorable()
	if err != nil {
		fmt.Fprintf(stderr, "aish: undo: %v\n", err)
		return 1
	}
	if ev == nil {
		fmt.Fprintln(stdout, "undo: nothing to undo")
		return 1
	}
	if err := s.history.RestoreEvent(ev); err != nil {
		fmt.Fprintf(stderr, "aish: undo: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "undo: restored %d path(s) from %q\n", countRestorablePaths(ev), ev.Command)
	return 0
}

// restoreBuiltin implements `restore <path>`. Resolves the path
// against the shell cwd, then asks the history store for its
// most-recent OpDelete snapshot, regardless of which destructive
// event it came from.
func (s *Shell) restoreBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "aish: restore: usage: restore <path>")
		return 2
	}
	if s.history == nil {
		fmt.Fprintln(stderr, "aish: restore: history not available")
		return 1
	}
	target := args[0]
	if !filepath.IsAbs(target) {
		target = filepath.Clean(filepath.Join(s.cwd, target))
	} else {
		target = filepath.Clean(target)
	}
	if err := s.history.RestorePath(target); err != nil {
		fmt.Fprintf(stderr, "aish: restore: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "restore: %s\n", target)
	return 0
}

// countRestorablePaths returns the number of records in an event
// that carry byte content — the user-visible "files restored" count.
// Directory markers (empty SnapshotDir) are excluded because they
// don't represent restored content.
//
// v0.3-4 includes OpRename and OpModify rows alongside OpDelete so
// `undo` reports the right count after a mv-overwrite.
func countRestorablePaths(ev *history.Event) int {
	if ev == nil {
		return 0
	}
	n := 0
	for _, a := range ev.Affected {
		switch a.Op {
		case history.OpDelete, history.OpRename, history.OpModify:
			if a.SnapshotDir != "" {
				n++
			}
		}
	}
	return n
}
