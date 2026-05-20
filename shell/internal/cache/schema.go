// Package cache implements the v0.1-2 Intent Cache L1.
//
// The cache compiles natural-language intents to shell-ready invocations
// at most once per (intent, OS) tuple. On a hit, aish skips the inference
// plugin entirely. On a miss, the orchestrator (Cache) asks an inference
// plugin (the PluginClient holds the spawned aish-inference-cloud child)
// and writes the result back to the SQLite store for next time.
//
// Layout of this package:
//
//	schema.go  — SQL DDL applied on every Open.
//	store.go   — SQLite Store: Open / Close / Lookup / Write / Stats / Clear.
//	plugin.go  — PluginClient: spawn child, JSON-RPC over stdin/stdout.
//	cache.go   — Cache orchestrator: Resolve = cache → plugin → write-back.
//
// The store path defaults to ~/.aish/cache.db (the caller is responsible
// for picking the path; this package takes whatever it is handed).
//
// Embedding columns are reserved in the schema but unused in v0.1-2; the
// embedding-similarity work (#16, #17) is deferred to v0.1-2-followup
// because Anthropic does not publish an embeddings model.
//
// See .artifacts/plans/v0.1-2.md and GOALS.md §"Intent Cache — The Flywheel".
package cache

// DDL is the cache schema. Applied idempotently on every Open. The three
// stats rows (hits / misses / total_queries) are seeded with INSERT OR
// IGNORE so a fresh DB starts at zero and an existing DB is untouched.
//
// PK = (intent_hash, os). Same intent on different OSes lives in
// distinct rows because the compiled invocation diverges per platform
// (e.g. `rm -rf` vs `Remove-Item -Recurse -Force`).
//
// The `embedding BLOB` column is reserved for #16/#17 — populated by a
// future embedding generator, queried by an ANN-style similarity lookup.
// For v0.1-2 it is always NULL.
const DDL = `
CREATE TABLE IF NOT EXISTS intents (
  intent_hash TEXT    NOT NULL,
  os          TEXT    NOT NULL,
  intent      TEXT    NOT NULL,
  invocation  TEXT    NOT NULL,
  confidence  REAL    NOT NULL DEFAULT 1.0,
  hit_count   INTEGER NOT NULL DEFAULT 0,
  embedding   BLOB,
  created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_used   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (intent_hash, os)
);

CREATE INDEX IF NOT EXISTS idx_intents_os ON intents(os);

CREATE TABLE IF NOT EXISTS stats (
  key   TEXT PRIMARY KEY,
  value INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO stats(key, value) VALUES ('hits', 0);
INSERT OR IGNORE INTO stats(key, value) VALUES ('misses', 0);
INSERT OR IGNORE INTO stats(key, value) VALUES ('total_queries', 0);
`
