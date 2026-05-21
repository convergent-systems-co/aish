package telemetry

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fakeCache implements CacheStatsReader by reading from in-memory
// counters. Used so the recorder tests don't pull in a SQLite store.
type fakeCache struct {
	hits, misses int64
	err          error
}

func (f *fakeCache) StatsSnapshot() (int64, int64, error) {
	return f.hits, f.misses, f.err
}

func TestRecorder_New_RequiresDotAishDir(t *testing.T) {
	t.Parallel()
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error for empty DotAishDir")
	}
}

func TestRecorder_AfterIncrementsCommands(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	r, err := New(Config{DotAishDir: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.After(nil, "echo hi", 0, 50*time.Millisecond)
	r.After(nil, "false", 1, 20*time.Millisecond)
	r.After(nil, "echo bye", 0, 30*time.Millisecond)
	c := r.CurrentCounters()
	if c.Commands != 3 {
		t.Errorf("Commands = %d, want 3", c.Commands)
	}
	if c.FailedCommands != 1 {
		t.Errorf("FailedCommands = %d, want 1", c.FailedCommands)
	}
	if c.WallTimeMs != 100 {
		t.Errorf("WallTimeMs = %d, want 100", c.WallTimeMs)
	}
}

func TestRecorder_CacheDelta(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := &fakeCache{hits: 5, misses: 3} // baseline at session start
	r, err := New(Config{DotAishDir: home, CacheReader: cache})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// One command, cache had a hit and a miss during it.
	cache.hits = 7
	cache.misses = 4
	r.After(nil, "ls", 0, 10*time.Millisecond)

	c := r.CurrentCounters()
	if c.CacheHits != 2 {
		t.Errorf("CacheHits = %d, want 2 (7-5)", c.CacheHits)
	}
	if c.CacheMisses != 1 {
		t.Errorf("CacheMisses = %d, want 1 (4-3)", c.CacheMisses)
	}
	// InferenceCalls stays at 0 because PluginActive was nil.
	if c.InferenceCalls != 0 {
		t.Errorf("InferenceCalls = %d, want 0 (no plugin)", c.InferenceCalls)
	}
}

func TestRecorder_InferenceCalls_OnlyWhenPluginActive(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := &fakeCache{hits: 0, misses: 0}
	active := true
	r, err := New(Config{
		DotAishDir:   home,
		CacheReader:  cache,
		PluginActive: func() bool { return active },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache.misses = 2
	r.After(nil, "x", 0, 0)
	if got := r.CurrentCounters().InferenceCalls; got != 2 {
		t.Errorf("InferenceCalls with plugin active = %d, want 2", got)
	}

	active = false
	cache.misses = 5
	r.After(nil, "y", 0, 0)
	c := r.CurrentCounters()
	// CacheMisses keeps growing, but InferenceCalls stays at 2 — the
	// new misses happened with the plugin inactive.
	if c.CacheMisses != 5 {
		t.Errorf("CacheMisses = %d, want 5", c.CacheMisses)
	}
	if c.InferenceCalls != 2 {
		t.Errorf("InferenceCalls = %d, want 2 (plugin went inactive)", c.InferenceCalls)
	}
}

func TestRecorder_CacheStatsError_NoPanic(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := &fakeCache{err: errors.New("db gone")}
	r, err := New(Config{DotAishDir: home, CacheReader: cache})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.After(nil, "ls", 0, 0) // must not panic
	if r.CurrentCounters().Commands != 1 {
		t.Errorf("Commands didn't tick even though we tolerated a cache error")
	}
}

func TestRecorder_NilCacheReader_TicksCommandsOnly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	r, err := New(Config{DotAishDir: home}) // CacheReader is nil
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.After(nil, "ls", 0, 5*time.Millisecond)
	c := r.CurrentCounters()
	if c.Commands != 1 {
		t.Errorf("Commands = %d, want 1", c.Commands)
	}
	if c.CacheHits != 0 || c.CacheMisses != 0 || c.InferenceCalls != 0 {
		t.Errorf("cache counters non-zero with nil reader: %+v", c)
	}
}

func TestRecorder_Close_WritesSessionFileWhenLocalOptedIn(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	r, err := New(Config{DotAishDir: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.After(nil, "echo hi", 0, 10*time.Millisecond)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rows, err := ListSessions(home, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ID != r.SessionID() {
		t.Errorf("row.ID = %q, want %q", rows[0].ID, r.SessionID())
	}
	if rows[0].Counters.Commands != 1 {
		t.Errorf("row.Commands = %d, want 1", rows[0].Counters.Commands)
	}
}

func TestRecorder_Close_NoPendingByDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	r, err := New(Config{DotAishDir: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	pendingDir := filepath.Join(home, SessionsDirName, PendingDirName)
	// Default consent: aggregate off. The pending dir MUST NOT exist
	// — Close never writes to it.
	if _, err := readDirOrEmpty(pendingDir); err == nil {
		// Existing means we wrote — fail loud.
		entries, _ := readDirOrEmpty(pendingDir)
		if len(entries) > 0 {
			t.Fatalf("default opt-out: pending file written anyway: %v", entries)
		}
	}
}

func TestRecorder_Close_PendingWhenAggregateOptIn(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Pre-write a consent file with aggregate on, so New sees it.
	consentFile := filepath.Join(home, ConsentFilename)
	if err := writeFile(consentFile, "[telemetry]\nopt_in_local = true\nopt_in_aggregate = true\n"); err != nil {
		t.Fatalf("seed consent: %v", err)
	}
	r, err := New(Config{DotAishDir: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	pendingDir := filepath.Join(home, SessionsDirName, PendingDirName)
	entries, err := readDirOrEmpty(pendingDir)
	if err != nil {
		t.Fatalf("read pending dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("pending entries = %d, want 1", len(entries))
	}
}

func TestRecorder_Close_Idempotent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	r, err := New(Config{DotAishDir: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}
	rows, _ := ListSessions(home, 10)
	if len(rows) != 1 {
		t.Errorf("idempotent Close wrote %d rows, want 1", len(rows))
	}
}

func TestRecorder_Close_NoLocalFileWhenLocalOptOut(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Pre-seed consent with local opt-out.
	consentFile := filepath.Join(home, ConsentFilename)
	if err := writeFile(consentFile, "[telemetry]\nopt_in_local = false\nopt_in_aggregate = false\n"); err != nil {
		t.Fatalf("seed consent: %v", err)
	}
	r, err := New(Config{DotAishDir: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Counters still tick in memory.
	r.After(nil, "echo hi", 0, 10*time.Millisecond)
	if r.CurrentCounters().Commands != 1 {
		t.Errorf("Commands didn't tick with local opt-out")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// No session file persisted.
	rows, _ := ListSessions(home, 10)
	if len(rows) != 0 {
		t.Errorf("local opt-out wrote %d rows, want 0", len(rows))
	}
}

func TestRecorder_NilSafe(t *testing.T) {
	t.Parallel()
	var r *Recorder
	r.After(nil, "x", 0, 0) // must not panic
	if err := r.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
	if id := r.SessionID(); id != "" {
		t.Errorf("nil SessionID = %q, want empty", id)
	}
	if c := r.Consent(); c != DefaultConsent() {
		t.Errorf("nil Consent = %+v, want defaults", c)
	}
	if c := r.CurrentCounters(); c != (Counters{}) {
		t.Errorf("nil CurrentCounters = %+v, want zero", c)
	}
}
