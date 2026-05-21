package history

// DDL is the history-log schema. Applied idempotently on every Open.
//
// Two base tables, one FTS5 virtual table, three triggers.
//
//	events       — one row per shell command that produced a recorded
//	               event. `payload` is the JSON-encoded Event (so
//	               future schema growth — new Affected fields,
//	               additional signature schemes, etc. — does not
//	               require an ALTER TABLE).
//	snapshots    — one row per file actually copied to disk. The
//	               unique index on (path, ts) gives SnapshotsForPath
//	               its most-recent-wins behavior.
//	events_fts   — FTS5 virtual table indexing (command, name) so
//	               `aish history search` is index-backed. The three
//	               triggers (insert / update / delete) keep it in
//	               sync with events.
//
// Append-only by convention: the only UPDATE statements in store.go
// touch events.exit_code + events.duration_ms (the Finalize call
// after the destructive command returns) and the JSON payload mirror
// for the same row. Nothing else mutates.
//
// PRAGMA journal_mode=WAL is set on Open so readers do not block
// writers — crucial once `aish history` is a foreground built-in
// running while a background `rm` is mid-snapshot.
//
// New columns (v0.3-4) — signature, signer_id, name — are nullable
// so a migrated pre-v0.3-4 row reads as "unsigned, unnamed". migrate.go
// (Store.migrate) probes PRAGMA table_info and ADD COLUMNs the
// missing ones; the DDL below is the FRESH-INSTALL schema, not a
// migration path.
const DDL = `
CREATE TABLE IF NOT EXISTS events (
  id          TEXT      PRIMARY KEY,
  ts          TIMESTAMP NOT NULL,
  kind        TEXT      NOT NULL,
  command     TEXT      NOT NULL,
  cwd         TEXT      NOT NULL DEFAULT '',
  exit_code   INTEGER,
  duration_ms INTEGER,
  payload     TEXT      NOT NULL,
  signature   TEXT      NOT NULL DEFAULT '',
  signer_id   TEXT      NOT NULL DEFAULT '',
  name        TEXT      NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_events_ts   ON events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind);
CREATE INDEX IF NOT EXISTS idx_events_name ON events(name);

CREATE TABLE IF NOT EXISTS snapshots (
  event_id      TEXT      NOT NULL,
  path          TEXT      NOT NULL,
  op            TEXT      NOT NULL,
  snapshot_dir  TEXT      NOT NULL DEFAULT '',
  skip_reason   TEXT      NOT NULL DEFAULT '',
  sha256        TEXT      NOT NULL DEFAULT '',
  bytes         INTEGER   NOT NULL DEFAULT 0,
  ts            TIMESTAMP NOT NULL,
  rename_target TEXT      NOT NULL DEFAULT '',
  PRIMARY KEY (event_id, path),
  FOREIGN KEY (event_id) REFERENCES events(id)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_path_ts ON snapshots(path, ts DESC);

-- FTS5 index over the searchable columns of events. external-content
-- form: the FTS table is a pure index, not a separate copy of the
-- data. Triggers keep it in sync. v0.3-4: command + name only;
-- payload-blob search is left to the substring fallback inside
-- Search().
CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
  command,
  name,
  content='events',
  content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS events_fts_ai AFTER INSERT ON events BEGIN
  INSERT INTO events_fts(rowid, command, name) VALUES (new.rowid, new.command, new.name);
END;
CREATE TRIGGER IF NOT EXISTS events_fts_ad AFTER DELETE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, command, name) VALUES('delete', old.rowid, old.command, old.name);
END;
CREATE TRIGGER IF NOT EXISTS events_fts_au AFTER UPDATE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, command, name) VALUES('delete', old.rowid, old.command, old.name);
  INSERT INTO events_fts(rowid, command, name) VALUES (new.rowid, new.command, new.name);
END;
`

// migrationProbes is the list of ADD COLUMN statements that adapt a
// pre-v0.3-4 history.db to the v0.3-4 schema. Each entry is a
// (table, column, ddl-fragment) triple. store.migrate() walks the
// table_info pragma and applies only the columns that are missing,
// so a fresh-install DB (whose DDL already covers these) is untouched.
//
// Note: SQLite does not support `ADD COLUMN IF NOT EXISTS`, so the
// table_info probe is load-bearing.
var migrationProbes = []struct {
	Table        string
	Column       string
	AddColumnDDL string
}{
	{Table: "events", Column: "signature", AddColumnDDL: `ALTER TABLE events ADD COLUMN signature TEXT NOT NULL DEFAULT ''`},
	{Table: "events", Column: "signer_id", AddColumnDDL: `ALTER TABLE events ADD COLUMN signer_id TEXT NOT NULL DEFAULT ''`},
	{Table: "events", Column: "name", AddColumnDDL: `ALTER TABLE events ADD COLUMN name TEXT NOT NULL DEFAULT ''`},
	{Table: "snapshots", Column: "rename_target", AddColumnDDL: `ALTER TABLE snapshots ADD COLUMN rename_target TEXT NOT NULL DEFAULT ''`},
}
