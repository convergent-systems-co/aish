package history

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	// modernc.org/sqlite is a pure-Go SQLite driver (no CGO), already
	// vendored for the v0.1-2 intent cache. Reusing it keeps the
	// single-binary build promise intact.
	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed persistence layer for history events
// and the index of file snapshots. Concurrency posture mirrors
// shell/internal/cache.Store: one writer at a time via MaxOpenConns=1,
// WAL for non-blocking readers. The single-file DB at the caller-
// supplied path is canonical state.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and applies the
// DDL idempotently. WAL is enabled so a future `aish history`
// table-renderer can read while a destructive command's Append is
// in flight. busy_timeout keeps transient lock contention from
// surfacing as "database is locked" errors during a hot loop of
// destructive commands.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("history: Open: path is empty")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history: pragma: %w", err)
	}
	if _, err := db.Exec(DDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history: apply DDL: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle. Idempotent; a second Close on
// an already-closed Store is a no-op.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Append writes a fresh event row and the per-affected snapshot rows.
// The event arrives with ExitCode == nil ("still in flight"); the
// matching Finalize call after the destructive command returns fills
// exit_code + duration_ms.
//
// The whole insertion is one transaction. If the snapshot table write
// fails, the event row rolls back so we never end up with an event
// pointing at non-existent snapshot rows (the symmetric failure mode
// — snapshots pointing at no event — is structurally impossible).
func (s *Store) Append(e *Event) error {
	if s == nil || s.db == nil {
		return errors.New("history: Append: store is closed")
	}
	if e.ID == "" {
		return errors.New("history: Append: event has empty ID")
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("history: marshal event: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("history: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO events(id, ts, kind, command, cwd, exit_code, duration_ms, payload)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL, ?)`,
		e.ID, e.Timestamp.UTC(), string(e.Kind), e.Command, e.Cwd, string(payload),
	); err != nil {
		return fmt.Errorf("history: insert event: %w", err)
	}
	for _, a := range e.Affected {
		if _, err := tx.Exec(
			`INSERT INTO snapshots(event_id, path, op, snapshot_dir, skip_reason, sha256, bytes, ts)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, a.Path, string(a.Op), a.SnapshotDir, a.SkipReason, a.SHA256, a.Bytes, e.Timestamp.UTC(),
		); err != nil {
			return fmt.Errorf("history: insert snapshot: %w", err)
		}
	}
	return tx.Commit()
}

// Finalize writes back the exit_code and duration after the
// destructive command returns. This is the only UPDATE the store
// ever performs against the events table — every other write path is
// pure INSERT.
//
// The duration is stored in whole milliseconds; sub-millisecond
// precision is not interesting at shell-command granularity.
func (s *Store) Finalize(id string, exitCode int, dur time.Duration) error {
	if s == nil || s.db == nil {
		return errors.New("history: Finalize: store is closed")
	}
	ms := dur.Milliseconds()
	res, err := s.db.Exec(
		`UPDATE events SET exit_code = ?, duration_ms = ? WHERE id = ?`,
		exitCode, ms, id,
	)
	if err != nil {
		return fmt.Errorf("history: finalize: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("history: finalize: no row for %s", id)
	}
	// Also patch the JSON payload so consumers reading the blob see
	// the same exit_code as the column. Reading the row first
	// preserves any future-added fields we don't know about here.
	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM events WHERE id = ?`, id).Scan(&payload); err != nil {
		return nil // best-effort; column truth wins
	}
	var ev Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return nil
	}
	ev.ExitCode = &exitCode
	ev.DurationMS = ms
	patched, err := json.Marshal(&ev)
	if err != nil {
		return nil
	}
	_, _ = s.db.Exec(`UPDATE events SET payload = ? WHERE id = ?`, string(patched), id)
	return nil
}

// LatestRestorable returns the most-recent event whose kind ==
// KindSnapshot AND exit_code != NULL. Returns (nil, nil) when no
// candidate exists.
//
// The exit_code filter is load-bearing: an event still in flight
// (Append happened, Finalize did not) is racing with a live destructive
// command. Restoring during the race would put bytes back under the
// foreground process's feet.
func (s *Store) LatestRestorable() (*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: LatestRestorable: store is closed")
	}
	row := s.db.QueryRow(
		`SELECT payload FROM events
		   WHERE kind = ? AND exit_code IS NOT NULL
		   ORDER BY ts DESC, id DESC LIMIT 1`,
		string(KindSnapshot),
	)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: LatestRestorable scan: %w", err)
	}
	var ev Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return nil, fmt.Errorf("history: LatestRestorable unmarshal: %w", err)
	}
	// A restorable event needs at least one OpDelete in its affected
	// list, otherwise there is nothing to restore (all paths were
	// skipped or absent). Filter here so the caller does not see a
	// useless event.
	hasDelete := false
	for _, a := range ev.Affected {
		if a.Op == OpDelete && a.SnapshotDir != "" {
			hasDelete = true
			break
		}
	}
	if !hasDelete {
		return nil, nil
	}
	return &ev, nil
}

// SnapshotsForPath returns the most-recent OpDelete snapshot whose
// path == the requested path. Returns (nil, nil) when no candidate
// exists. The matching event's exit_code must be set; in-flight
// commands are not restore candidates (same rationale as
// LatestRestorable).
func (s *Store) SnapshotsForPath(path string) (*Affected, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: SnapshotsForPath: store is closed")
	}
	row := s.db.QueryRow(
		`SELECT s.path, s.op, s.snapshot_dir, s.skip_reason, s.sha256, s.bytes
		   FROM snapshots s
		   JOIN events e ON e.id = s.event_id
		  WHERE s.path = ?
		    AND s.op = ?
		    AND e.exit_code IS NOT NULL
		  ORDER BY s.ts DESC LIMIT 1`,
		path, string(OpDelete),
	)
	var a Affected
	var op string
	if err := row.Scan(&a.Path, &op, &a.SnapshotDir, &a.SkipReason, &a.SHA256, &a.Bytes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: SnapshotsForPath: %w", err)
	}
	a.Op = Op(op)
	return &a, nil
}
