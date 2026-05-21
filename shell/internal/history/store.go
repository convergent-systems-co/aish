package history

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	// modernc.org/sqlite is a pure-Go SQLite driver (no CGO), already
	// vendored for the v0.1-2 intent cache. Reusing it keeps the
	// single-binary build promise intact.
	_ "modernc.org/sqlite"
)

// vecUnavailableOnce guards the one-time "sqlite-vec extension
// unavailable" log line. Without it, every Open on a binary lacking
// the extension would emit the warning — once per test, once per
// shell start, once per CLI invocation. One process-wide warning is
// sufficient signal; the rest is noise.
var vecUnavailableOnce sync.Once

// Store is the SQLite-backed persistence layer for history events
// and the index of file snapshots. Concurrency posture mirrors
// shell/internal/cache.Store: one writer at a time via MaxOpenConns=1,
// WAL for non-blocking readers. The single-file DB at the caller-
// supplied path is canonical state.
//
// signer is the per-event Ed25519 signer. Nil-safe: a nil signer
// emits events with empty Signature / SignerID columns. Set via
// WithSigner; the production wire-up is shell.openHistory.
//
// embedder and vec are the v0.3-4 #112 semantic-search hooks. Both
// are nil-safe: a Store with embedder == nil performs no embed-on-
// write; a Store with vec == nil exposes no semantic-search surface.
// The pre-#112 behavior is recovered by leaving both fields nil. The
// concrete activations land in T3 (embed-on-write) and T4 (search
// surface) of the #112 wave; the seed only ships the shape so the
// surface is reviewable in isolation. Wired via WithEmbedder /
// WithVectorStore on the production Open path.
type Store struct {
	db       *sql.DB
	signer   Signer
	embedder EmbeddingProvider
	vec      VectorStore
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
	s := &Store{db: db}
	// Pre-DDL migrate: when a pre-v0.3-4 events table is present
	// without the v0.3-4 columns (signature, signer_id, name), the
	// post-#112 DDL's `CREATE INDEX … ON events(name)` fails at
	// parse time with "no such column: name" before any
	// ADD-COLUMN can run. Probing first and running migrate before
	// DDL re-application keeps the migration order correct: add
	// columns, then re-apply triggers/indexes that reference them.
	// On a fresh-install DB the probe sees no events table and
	// migrate is a no-op; the post-migrate DDL creates everything.
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history: pre-DDL migrate: %w", err)
	}
	if _, err := db.Exec(DDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history: apply DDL: %w", err)
	}
	if err := s.migrateVec(); err != nil {
		// Vec migration is best-effort. Semantic search degrades
		// gracefully when the sqlite-vec extension is unavailable
		// (AC10). A hard failure here would regress the FTS5 path,
		// which is unacceptable.
		_ = db.Close()
		return nil, fmt.Errorf("history: migrate vec sidecar: %w", err)
	}
	return s, nil
}

// migrateVec applies the #112 vector-table migration. The plain-SQLite
// sidecar (events_vec_meta) always succeeds — its DDL needs no
// extension. The vec0 virtual table needs the sqlite-vec extension
// loaded; on the current modernc.org/sqlite driver that extension is
// not present, so the CREATE returns "no such module: vec0" and we
// SKIP it silently (no log spam on every Open). The sidecar's
// presence on its own is enough to track "which events have been
// embedded" — Phase B implementations gate every vec-store call on
// `Store.vec != nil` so the seed is safe regardless of extension
// availability.
//
// Returning a non-nil error here is reserved for sidecar failures —
// those genuinely block correctness. vec0 failure is silent by
// design.
func (s *Store) migrateVec() error {
	if _, err := s.db.Exec(VecMetaDDL); err != nil {
		return fmt.Errorf("events_vec_meta DDL: %w", err)
	}
	if _, err := s.db.Exec(VecTableDDL); err != nil {
		// Expected on builds without the sqlite-vec extension. Log
		// once per process; do not surface as an error.
		vecUnavailableOnce.Do(func() {
			log.Printf("history: sqlite-vec extension unavailable, semantic search disabled: %v", err)
		})
	}
	return nil
}

// WithSigner attaches a Signer; subsequent Append / Checkpoint calls
// will populate Event.Signature + Event.SignerID. Idempotent — calling
// twice replaces the prior signer. Nil signer disables signing.
func (s *Store) WithSigner(signer Signer) *Store {
	if s == nil {
		return nil
	}
	s.signer = signer
	return s
}

// Signer returns the currently-attached signer. Nil when no signing
// is configured. Exposed so the shell can route the same key into
// future audit consumers (e.g. the v0.3-LOGIN audit stream).
func (s *Store) Signer() Signer {
	if s == nil {
		return nil
	}
	return s.signer
}

// WithEmbedder attaches an EmbeddingProvider used by the Append path
// (T3) to embed non-tainted events at write time and by the search
// surface (T4) to embed query strings. Nil-safe: passing nil disables
// embedding (recovers pre-#112 behavior). Idempotent — calling twice
// replaces the prior embedder. Returns the receiver so the production
// wire-up at shell.openHistory can chain WithSigner / WithEmbedder /
// WithVectorStore on a single line.
func (s *Store) WithEmbedder(e EmbeddingProvider) *Store {
	if s == nil {
		return nil
	}
	s.embedder = e
	return s
}

// Embedder returns the currently-attached embedding provider, or nil
// when none is configured. Exposed so the search surface and the
// reindex command can both reach the same provider that the Append
// path uses — single source of truth for the model id persisted
// alongside each vector row (AC7).
func (s *Store) Embedder() EmbeddingProvider {
	if s == nil {
		return nil
	}
	return s.embedder
}

// WithVectorStore attaches a VectorStore used by the Append path to
// upsert per-event embeddings and by the search surface to query
// cosine-similarity matches. Nil-safe: passing nil disables the
// vector path (the FTS5 + LIKE surface from #113 continues to serve).
// Idempotent. Returns the receiver for fluent chaining.
func (s *Store) WithVectorStore(v VectorStore) *Store {
	if s == nil {
		return nil
	}
	s.vec = v
	return s
}

// VectorStore returns the currently-attached vector store, or nil when
// none is configured. Used by the search surface to decide whether the
// `--mode=semantic` and `--mode=hybrid` paths are reachable; when nil,
// `aish history search` degrades to keyword-only without error
// (AC10's migration-safety contract).
func (s *Store) VectorStore() VectorStore {
	if s == nil {
		return nil
	}
	return s.vec
}

// migrate walks migrationProbes and ALTER TABLE … ADD COLUMNs the
// columns missing from a pre-v0.3-4 events / snapshots table. A
// fresh-install DB hits no work — the table doesn't exist yet, the
// probe sees no candidate to ADD against, and the post-DDL pass
// creates the full schema. A pre-v0.3-4 DB hits real work: ADD
// COLUMN for signature, signer_id, name, rename_target before the
// DDL's `CREATE INDEX … ON events(name)` runs, which would
// otherwise fail at parse time on the missing column.
//
// The FTS5 trigger pair recreated in DDL is idempotent because
// every CREATE there is `IF NOT EXISTS`. The FTS index itself is
// rebuilt by a one-shot `INSERT INTO events_fts(events_fts) VALUES
// ('rebuild')` so a migrated DB with pre-existing event rows ends up
// indexed without forcing the user to re-write every command.
func (s *Store) migrate() error {
	for _, p := range migrationProbes {
		exists, err := s.tableExists(p.Table)
		if err != nil {
			return err
		}
		if !exists {
			// Fresh-install path: the post-migrate DDL pass will
			// create the table with all v0.3-4 columns. Skipping
			// the ADD COLUMN here avoids a `no such table` error
			// on the very first Open.
			continue
		}
		have, err := s.columnExists(p.Table, p.Column)
		if err != nil {
			return err
		}
		if have {
			continue
		}
		if _, err := s.db.Exec(p.AddColumnDDL); err != nil {
			return fmt.Errorf("ADD COLUMN %s.%s: %w", p.Table, p.Column, err)
		}
	}
	// FTS5 rebuild so migrated rows are searchable. Pre-existing
	// triggers (re-)created by DDL only index rows inserted AFTER
	// the trigger exists; a rebuild covers the pre-trigger backlog.
	// Best-effort: an error here doesn't block the open — search
	// degrades to "no results" rather than crashing the shell. This
	// also no-ops cleanly when events_fts does not yet exist (the
	// fresh-install path before the post-migrate DDL pass).
	_, _ = s.db.Exec(`INSERT INTO events_fts(events_fts) VALUES('rebuild')`)
	return nil
}

// tableExists returns whether a table with the given name is
// present in sqlite_master. Used by migrate to distinguish the
// fresh-install path (no events table yet → skip ADD COLUMN, let
// DDL create it) from the pre-v0.3-4 path (events table exists,
// missing some columns → ADD COLUMN before DDL re-applies triggers
// and indexes that reference those columns).
func (s *Store) tableExists(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		name,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("sqlite_master probe %s: %w", name, err)
	}
	return n > 0, nil
}

func (s *Store) columnExists(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
// When a Signer is attached (WithSigner) the event is signed before
// persistence: canonicalSigningMsg blanks Signature + SignerID so
// they cannot themselves be part of what gets signed; the resulting
// signature is base64-encoded and stored on both the SQL column and
// the JSON payload mirror. A nil signer is silent — Signature stays
// empty and the row is still persisted (degradation per the v0.3-4
// plan: signing is best-effort, persistence is mandatory).
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
	if s.signer != nil {
		// Blank the carrier fields just in case the caller pre-set
		// them; canonicalSigningMsg does this on its own clone but we
		// also want the persisted event to carry the right values.
		e.Signature = ""
		e.SignerID = ""
		msg, err := canonicalSigningMsg(e)
		if err != nil {
			return fmt.Errorf("history: canonicalize for signing: %w", err)
		}
		sig, err := s.signer.Sign(msg)
		if err != nil {
			return fmt.Errorf("history: sign: %w", err)
		}
		e.Signature = base64.StdEncoding.EncodeToString(sig)
		e.SignerID = s.signer.SignerID()
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
		`INSERT INTO events(id, ts, kind, command, cwd, exit_code, duration_ms, payload, signature, signer_id, name)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?)`,
		e.ID, e.Timestamp.UTC(), string(e.Kind), e.Command, e.Cwd, string(payload),
		e.Signature, e.SignerID, e.Name,
	); err != nil {
		return fmt.Errorf("history: insert event: %w", err)
	}
	for _, a := range e.Affected {
		if _, err := tx.Exec(
			`INSERT INTO snapshots(event_id, path, op, snapshot_dir, skip_reason, sha256, bytes, ts, rename_target)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, a.Path, string(a.Op), a.SnapshotDir, a.SkipReason, a.SHA256, a.Bytes, e.Timestamp.UTC(), a.RenameTarget,
		); err != nil {
			return fmt.Errorf("history: insert snapshot: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// #112 T3: post-commit embed-on-write hook. Best-effort —
	// embedder/vec failure does NOT propagate. Skip-tainted policy
	// lives inside embedAndStore (AC4). Nil-safe when either field
	// is not attached (recovers pre-#112 behavior).
	s.embedAndStore(context.Background(), e)
	return nil
}

// Checkpoint writes a KindCheckpoint event named `name`. The event
// has an empty affected list — checkpoints are pure markers; the
// rollback path queries events newer than the checkpoint and restores
// from their snapshots.
//
// An empty `name` is rejected — the rollback API addresses checkpoints
// by name, and an unnamed checkpoint would be unaddressable.
func (s *Store) Checkpoint(name string) (*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: Checkpoint: store is closed")
	}
	if name == "" {
		return nil, errors.New("history: Checkpoint: name is empty")
	}
	exit := 0
	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindCheckpoint,
		Command:   "checkpoint " + name,
		Name:      name,
		ExitCode:  &exit,
	}
	if err := s.Append(ev); err != nil {
		return nil, err
	}
	// Checkpoints are "born finalized" — no destructive command to
	// race with. Mirror that by setting exit_code immediately so the
	// row shows finished in queries that filter on exit_code IS NOT
	// NULL (the same filter undo / restore use).
	if _, err := s.db.Exec(
		`UPDATE events SET exit_code = 0, duration_ms = 0 WHERE id = ?`,
		ev.ID,
	); err != nil {
		return ev, fmt.Errorf("history: Checkpoint finalize: %w", err)
	}
	return ev, nil
}

// CheckpointByName returns the most-recent KindCheckpoint event whose
// name == `name`. Returns (nil, nil) when no such checkpoint exists.
func (s *Store) CheckpointByName(name string) (*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: CheckpointByName: store is closed")
	}
	if name == "" {
		return nil, errors.New("history: CheckpointByName: name is empty")
	}
	row := s.db.QueryRow(
		`SELECT payload FROM events
		   WHERE kind = ? AND name = ?
		   ORDER BY ts DESC, id DESC LIMIT 1`,
		string(KindCheckpoint), name,
	)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: CheckpointByName scan: %w", err)
	}
	var ev Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return nil, fmt.Errorf("history: CheckpointByName unmarshal: %w", err)
	}
	return &ev, nil
}

// EventsSinceCheckpoint returns every KindSnapshot event with
// timestamp > checkpoint.Timestamp, newest first. Used by the
// rollback flow: walk the returned events oldest-to-newest, calling
// RestoreEvent on each to bring the filesystem back to the
// checkpoint state.
//
// The newest-first ordering is the natural SQL ORDER BY direction;
// the caller is expected to reverse the slice before applying
// restores so older events restore first.
func (s *Store) EventsSinceCheckpoint(cp *Event) ([]*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: EventsSinceCheckpoint: store is closed")
	}
	if cp == nil {
		return nil, errors.New("history: EventsSinceCheckpoint: nil checkpoint")
	}
	rows, err := s.db.Query(
		`SELECT payload FROM events
		   WHERE kind = ?
		     AND ts > ?
		     AND exit_code IS NOT NULL
		   ORDER BY ts DESC, id DESC`,
		string(KindSnapshot), cp.Timestamp.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("history: EventsSinceCheckpoint: %w", err)
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
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
	// A restorable event needs at least one row with byte content
	// (OpDelete, OpRename, or OpModify with a SnapshotDir). Filter
	// here so the caller does not see a useless event.
	hasContent := false
	for _, a := range ev.Affected {
		switch a.Op {
		case OpDelete, OpRename, OpModify:
			if a.SnapshotDir != "" {
				hasContent = true
			}
		}
		if hasContent {
			break
		}
	}
	if !hasContent {
		return nil, nil
	}
	return &ev, nil
}

// SnapshotsForPath returns the most-recent restorable snapshot whose
// path == the requested path. Returns (nil, nil) when no candidate
// exists. The matching event's exit_code must be set; in-flight
// commands are not restore candidates (same rationale as
// LatestRestorable).
//
// v0.3-4: also matches OpRename and OpModify rows so `restore <path>`
// can roll back a rename or a modification, not just a delete.
func (s *Store) SnapshotsForPath(path string) (*Affected, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: SnapshotsForPath: store is closed")
	}
	row := s.db.QueryRow(
		`SELECT s.path, s.op, s.snapshot_dir, s.skip_reason, s.sha256, s.bytes, s.rename_target
		   FROM snapshots s
		   JOIN events e ON e.id = s.event_id
		  WHERE s.path = ?
		    AND s.op IN (?, ?, ?)
		    AND s.snapshot_dir != ''
		    AND e.exit_code IS NOT NULL
		  ORDER BY s.ts DESC LIMIT 1`,
		path, string(OpDelete), string(OpRename), string(OpModify),
	)
	var a Affected
	var op string
	if err := row.Scan(&a.Path, &op, &a.SnapshotDir, &a.SkipReason, &a.SHA256, &a.Bytes, &a.RenameTarget); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: SnapshotsForPath: %w", err)
	}
	a.Op = Op(op)
	return &a, nil
}
