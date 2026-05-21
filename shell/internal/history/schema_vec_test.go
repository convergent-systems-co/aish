// Seed-level tests for the #112 schema migration. These cover the
// migration probe that creates events_vec (sqlite-vec virtual table,
// optional — degrades gracefully when the extension is absent) and
// the events_vec_meta sidecar (plain SQLite, always created).
//
// Acceptance criteria covered (from .artifacts/plans/112.md):
//   - AC2 (partial): additive schema; FTS5 unaffected.
//   - AC10: pre-#112 history.db opens cleanly; FTS5 keeps working;
//     "vec table absent" is a soft state, not a crash.
//
// These tests run with the SEED commit only — they do NOT depend on
// any T1..T5 code under the phase_b build tag.

package history

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMigrateVec_SidecarAlwaysCreated asserts that the
// events_vec_meta sidecar table exists after Open on a fresh DB. The
// sidecar is plain SQLite — its DDL never needs an extension — so a
// missing sidecar after Open is a defect.
func TestMigrateVec_SidecarAlwaysCreated(t *testing.T) {
	s := openTestStore(t)

	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master
		   WHERE type = 'table' AND name = 'events_vec_meta'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("events_vec_meta sidecar not found after Open: %v", err)
	}
	if name != "events_vec_meta" {
		t.Errorf("table name: got %q want events_vec_meta", name)
	}
}

// TestMigrateVec_SidecarHasExpectedColumns pins the sidecar's column
// set. Reindex and write-path code (Phase B T3 / T5) will write
// (event_id, model_id, ts) rows here; a future column rename would
// be a silent break otherwise.
func TestMigrateVec_SidecarHasExpectedColumns(t *testing.T) {
	s := openTestStore(t)

	rows, err := s.db.Query(`PRAGMA table_info(events_vec_meta)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var cid int
		var n, ty string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &n, &ty, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[n] = ty
	}
	for _, want := range []string{"event_id", "model_id", "ts"} {
		if _, ok := got[want]; !ok {
			t.Errorf("events_vec_meta missing column %q (have: %v)", want, got)
		}
	}
}

// TestMigrateVec_VecTableSoftFailureDoesNotBreakOpen captures AC10's
// migration-safety contract: when the sqlite-vec extension is not
// loaded (the case on the current modernc.org/sqlite driver), Open
// MUST still succeed. The events_vec virtual table is allowed to be
// absent — semantic search is gracefully off — but FTS5 and the rest
// of the schema MUST be intact.
func TestMigrateVec_VecTableSoftFailureDoesNotBreakOpen(t *testing.T) {
	s := openTestStore(t)

	// Open succeeded (openTestStore would have t.Fatalf'd otherwise).
	// FTS5 must still be queryable.
	if _, err := s.db.Exec(
		`INSERT INTO events_fts(events_fts) VALUES ('rebuild')`,
	); err != nil {
		t.Fatalf("FTS5 rebuild after vec migration: %v", err)
	}

	// The vec table may or may not exist depending on whether the
	// sqlite-vec extension is loaded in this build. Either is
	// acceptable; what is NOT acceptable is "Open returned nil".
	if s == nil {
		t.Fatal("Open returned nil Store on vec-table soft-failure path")
	}
}

// TestMigrateVec_PreExistingDB_OpensCleanly walks through the AC10
// scenario directly: create a DB, write an event with the pre-#112
// surface, close, re-open with the new code path. The re-Open must
// (a) succeed, (b) leave the existing event intact, (c) leave FTS5
// queryable on the existing data.
func TestMigrateVec_PreExistingDB_OpensCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.db")

	// First open — simulates a pre-#112 history.db. (The Open we have
	// IS the post-#112 code, but the sidecar / vec table additions
	// are additive — the events / snapshots / events_fts surface is
	// unchanged, so this is a fair stand-in for the migration
	// scenario.)
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	ev := Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "echo hello world",
		Cwd:       "/tmp",
	}
	if err := s1.Append(&ev); err != nil {
		t.Fatalf("Append on first open: %v", err)
	}
	if err := s1.Finalize(ev.ID, 0, 10*time.Millisecond); err != nil {
		t.Fatalf("Finalize on first open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close first open: %v", err)
	}

	// Second open — must succeed and the prior event must still be
	// retrievable via List + Search (FTS5 path).
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open after migration: %v", err)
	}
	defer s2.Close()

	listed, err := s2.List(10)
	if err != nil {
		t.Fatalf("List after re-open: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List len: got %d want 1", len(listed))
	}
	if listed[0].ID != ev.ID {
		t.Errorf("retrieved event id: got %s want %s", listed[0].ID, ev.ID)
	}

	hits, err := s2.Search("hello", 10)
	if err != nil {
		t.Fatalf("Search after re-open: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("FTS5 lost prior event after vec migration")
	}
}

// TestMigrateVec_Idempotent runs migrateVec a second time on the same
// Store and asserts no error. The sidecar's `CREATE TABLE IF NOT
// EXISTS` and the vec0 `CREATE VIRTUAL TABLE IF NOT EXISTS` must both
// be safe to re-run.
func TestMigrateVec_Idempotent(t *testing.T) {
	s := openTestStore(t)

	// Direct re-invocation. In production this is reached via
	// repeated Open(); the test calls migrateVec directly to keep
	// scope tight.
	if err := s.migrateVec(); err != nil {
		t.Fatalf("second migrateVec: %v", err)
	}
	if err := s.migrateVec(); err != nil {
		t.Fatalf("third migrateVec: %v", err)
	}
}

// TestVecTableDDL_Has384DimEmbedding pins the 384-dim choice the
// fastembed-go + bge-small-en-v1.5 pairing requires (see plan
// §Alternatives Table). A future model swap (e.g., to a 768-dim
// model) must update this DDL and this test together; an unguarded
// change would silently break every existing vector row.
func TestVecTableDDL_Has384DimEmbedding(t *testing.T) {
	// String-match is the right granularity: the DDL is human-
	// authored, the extension parses it, the test pins the dimension
	// that downstream Phase B code (T1, T2) commits to.
	want := "FLOAT[384]"
	if !strings.Contains(VecTableDDL, want) {
		t.Errorf("VecTableDDL missing %q (model dimension mismatch?)", want)
	}
}

// TestVecMetaDDL_HasExpectedColumns is the same kind of pin for the
// sidecar. event_id is the PK, model_id is what AC7's mixed-model
// query rejection relies on.
func TestVecMetaDDL_HasExpectedColumns(t *testing.T) {
	for _, want := range []string{"event_id", "model_id"} {
		if !strings.Contains(VecMetaDDL, want) {
			t.Errorf("VecMetaDDL missing column reference %q", want)
		}
	}
}
