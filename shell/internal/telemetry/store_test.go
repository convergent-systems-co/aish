package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSessionRow_RoundTrip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	row := SessionRow{
		ID:            "test-id-1",
		StartedAt:     "2026-05-20T10:00:00Z",
		EndedAt:       "2026-05-20T10:30:00Z",
		Counters:      Counters{Commands: 7, CacheHits: 5, CacheMisses: 2},
		Costs:         SessionCosts{TotalUSD: 0.42, TotalCalls: 2},
		SchemaVersion: CurrentSchemaVersion,
	}
	if err := WriteSessionRow(home, row); err != nil {
		t.Fatalf("WriteSessionRow: %v", err)
	}
	rows, err := ListSessions(home, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.ID != row.ID || got.Counters.Commands != 7 || got.Costs.TotalUSD != 0.42 {
		t.Errorf("round trip mismatch:\n got:  %+v\n want: %+v", got, row)
	}
}

func TestWriteSessionRow_RejectsEmptyID(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteSessionRow(home, SessionRow{}); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestWritePending_OnlyWhenAggregateOptIn(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	row := SessionRow{ID: "p-1", SchemaVersion: CurrentSchemaVersion}
	if err := WritePending(home, row); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	pending := filepath.Join(home, SessionsDirName, PendingDirName, "p-1.json")
	if _, err := os.Stat(pending); err != nil {
		t.Errorf("pending file not created: %v", err)
	}
}

func TestListSessions_OrderingNewestFirst(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	rows := []SessionRow{
		{ID: "a", StartedAt: "2026-05-20T10:00:00Z", SchemaVersion: CurrentSchemaVersion},
		{ID: "b", StartedAt: "2026-05-20T11:00:00Z", SchemaVersion: CurrentSchemaVersion},
		{ID: "c", StartedAt: "2026-05-20T09:00:00Z", SchemaVersion: CurrentSchemaVersion},
	}
	for _, r := range rows {
		if err := WriteSessionRow(home, r); err != nil {
			t.Fatalf("WriteSessionRow %s: %v", r.ID, err)
		}
	}
	got, err := ListSessions(home, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	want := []string{"b", "a", "c"}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("rows[%d].ID = %q, want %q (order)", i, got[i].ID, want[i])
		}
	}
}

func TestListSessions_LimitApplied(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for i := 0; i < 5; i++ {
		r := SessionRow{
			ID:            "s-" + string(rune('a'+i)),
			StartedAt:     "2026-05-20T0" + string(rune('0'+i)) + ":00:00Z",
			SchemaVersion: CurrentSchemaVersion,
		}
		if err := WriteSessionRow(home, r); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := ListSessions(home, 2)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (limit)", len(got))
	}
}

func TestListSessions_MissingDir(t *testing.T) {
	t.Parallel()
	rows, err := ListSessions(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len = %d, want 0", len(rows))
	}
}

func TestListSessions_SkipsPendingSubdir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteSessionRow(home, SessionRow{ID: "main", StartedAt: "2026-05-20T10:00:00Z", SchemaVersion: CurrentSchemaVersion}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WritePending(home, SessionRow{ID: "queued", SchemaVersion: CurrentSchemaVersion}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	rows, err := ListSessions(home, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "main" {
		t.Errorf("ListSessions saw pending subdir: %+v", rows)
	}
}

func TestListSessions_TolerantOfBadFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, SessionsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(`{"id":"g","started_at":"2026-05-20T10:00:00Z","schema_version":1}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leftover.tmp"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}
	rows, err := ListSessions(home, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "g" {
		t.Errorf("ListSessions = %+v, want one row id=g", rows)
	}
}

func TestSessionRow_NoSensitiveFields(t *testing.T) {
	t.Parallel()
	// Privacy assertion: the JSON shape of a session row carries
	// counters and timing, never command lines, paths, env, or keys.
	// This test is a tripwire against future drift.
	row := SessionRow{
		ID:            "test",
		Counters:      Counters{Commands: 1},
		SchemaVersion: CurrentSchemaVersion,
	}
	home := t.TempDir()
	if err := WriteSessionRow(home, row); err != nil {
		t.Fatalf("WriteSessionRow: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, SessionsDirName, "test.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := strings.ToLower(string(data))
	for _, forbidden := range []string{"api_key", "token", "password", "command_line", "/home/", "/users/", "anthropic_api_key"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("session row contains forbidden substring %q:\n%s", forbidden, string(data))
		}
	}
}
