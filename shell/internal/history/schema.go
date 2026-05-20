package history

// DDL is the history-log schema. Applied idempotently on every Open.
//
// Two tables:
//
//   events    — one row per shell command that produced a recorded
//               event. `payload` is the JSON-encoded Event (so future
//               schema growth — new Affected fields, signature, etc. —
//               does not require an ALTER TABLE).
//   snapshots — one row per file actually copied to disk. The unique
//               index on (path, ts) gives SnapshotsForPath its
//               most-recent-wins behavior.
//
// Append-only by convention: the only UPDATE statement in store.go
// touches events.exit_code + events.duration_ms (the Finalize call
// after the destructive command returns). Nothing else mutates.
//
// PRAGMA journal_mode=WAL is set on Open so readers (a future
// `aish history` table renderer) do not block writers.
const DDL = `
CREATE TABLE IF NOT EXISTS events (
  id          TEXT      PRIMARY KEY,
  ts          TIMESTAMP NOT NULL,
  kind        TEXT      NOT NULL,
  command     TEXT      NOT NULL,
  cwd         TEXT      NOT NULL DEFAULT '',
  exit_code   INTEGER,
  duration_ms INTEGER,
  payload     TEXT      NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_ts   ON events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind);

CREATE TABLE IF NOT EXISTS snapshots (
  event_id     TEXT      NOT NULL,
  path         TEXT      NOT NULL,
  op           TEXT      NOT NULL,
  snapshot_dir TEXT      NOT NULL DEFAULT '',
  skip_reason  TEXT      NOT NULL DEFAULT '',
  sha256       TEXT      NOT NULL DEFAULT '',
  bytes        INTEGER   NOT NULL DEFAULT 0,
  ts           TIMESTAMP NOT NULL,
  PRIMARY KEY (event_id, path),
  FOREIGN KEY (event_id) REFERENCES events(id)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_path_ts ON snapshots(path, ts DESC);
`
