// Package history implements the v0.1-4 Basic Reversibility engine:
// the structured event log, pre-execution snapshotter, gitignore-style
// filter, and the `undo` / `restore` semantics that the shell layer
// exposes as built-ins.
//
// Surface (callers in shell/internal/shell):
//
//	NewHistory(store *Store, sn *Snapshotter) *History
//	  -- a PreCommand interceptor; registered on the Shell.
//	store.LatestRestorable() (*Event, error)
//	store.SnapshotsForPath(path string) (*Affected, error)
//	History.RestoreEvent(ev *Event) error
//	History.RestorePath(path string) error
//
// Layout (one file per acceptance area):
//
//	event.go       — Event + Affected types; JSON tags; ID generator.
//	schema.go      — SQL DDL applied on every Open.
//	store.go       — SQLite Store: Open/Close/Append/Finalize/LatestRestorable.
//	detect.go      — IsDestructive(pl) / TargetPaths(pl) on parser.Pipeline.
//	ignore.go      — gitignore-style matcher; default v0.1 pattern set.
//	config.go      — TOML reader for history.snapshot_max_bytes.
//	snapshot.go    — Snapshotter: Snapshot, SnapshotMany, Restore.
//	interceptor.go — History: composes Store + Snapshotter into Before/After.
//
// Acceptance source: .artifacts/plans/v0.1-4.md.
// GOALS section: §"Epic v0.1-4 — Basic Reversibility" + §5 "History Engine".
package history

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Kind is the discriminator on an event row. Each command produces at
// most one event; the event's Kind tells later consumers whether the
// row is a snapshotted destructive (eligible for undo) or a bare
// pre-exec note (eligible only for audit).
type Kind string

const (
	// KindPreExec is a recorded command that produced no snapshot —
	// either because IsDestructive returned false, or because every
	// candidate path was ignored / oversized / absent.
	KindPreExec Kind = "pre-exec"
	// KindSnapshot is a destructive command whose targets were copied
	// to ~/.aish/snapshots/ before execution. `aish undo` reads from
	// these rows only.
	KindSnapshot Kind = "snapshot"
)

// Op is the per-affected-path verb. v0.1 scope is delete only;
// modifications (mv, > redirect overwrite) land in v0.2.
type Op string

const (
	// OpDelete is the "this file existed and will be removed" case.
	// The snapshot row carries the bytes needed to undo it.
	OpDelete Op = "delete"
	// OpSkipped means the path matched a snapshot bypass rule:
	// oversize, ignored, or absent. SkipReason carries the detail.
	OpSkipped Op = "skipped"
	// OpAbsent means the path did not exist at snapshot time. The
	// destructive command will likely fail (`rm: no such file`); the
	// event is still recorded so the audit trail is complete.
	OpAbsent Op = "absent"
)

// SkipReason is the populated subtype when Op == OpSkipped.
const (
	// ReasonOversize fires when the file exceeded SnapshotMaxBytes.
	ReasonOversize = "oversize"
	// ReasonIgnored fires when the path matched the gitignore-style
	// filter (node_modules, .git, *.log, …).
	ReasonIgnored = "ignored"
)

// Affected is one row in an event's affected[] list — one path that
// the destructive command touches. `op` discriminates whether the
// snapshot succeeded; `snapshot_dir` is populated only for OpDelete.
type Affected struct {
	Path        string `json:"path"`
	Op          Op     `json:"op"`
	SnapshotDir string `json:"snapshot_dir,omitempty"`
	// SkipReason is populated when Op == OpSkipped; one of
	// ReasonOversize or ReasonIgnored. Empty otherwise.
	SkipReason string `json:"skip_reason,omitempty"`
	// SHA256 is the hex digest of the original bytes, taken at
	// snapshot time. Used by Restore to verify the on-disk snapshot
	// has not bit-rotted before clobbering the current path.
	SHA256 string `json:"sha256,omitempty"`
	// Bytes is the original file size in bytes. Informational; not
	// used to gate restore. Populated for OpDelete rows only.
	Bytes int64 `json:"bytes,omitempty"`
}

// Event is the on-wire shape of a single history row. Encoded as JSON
// in the events.payload column; the SQL columns mirror id, timestamp,
// kind so queries can filter without parsing the JSON blob.
type Event struct {
	ID         string     `json:"id"`
	Timestamp  time.Time  `json:"ts"`
	Kind       Kind       `json:"kind"`
	Command    string     `json:"command"`
	Cwd        string     `json:"cwd,omitempty"`
	ExitCode   *int       `json:"exit_code"`
	DurationMS int64      `json:"duration_ms,omitempty"`
	Affected   []Affected `json:"affected,omitempty"`
}

// NewEventID returns a fresh event identifier. v0.1 uses 96 bits of
// crypto/rand wrapped as `evt_<hex>`. Collision probability across the
// lifetime of any single user's history file is negligible (~2^-48 for
// a billion events). The ULID-style scheme described in GOALS.md is
// out of scope for v0.1.
func NewEventID() string {
	var b [12]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns a partial read
	return "evt_" + hex.EncodeToString(b[:])
}
