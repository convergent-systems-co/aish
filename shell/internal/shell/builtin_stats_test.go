package shell

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestStatsBuiltin_EmptyWindow exercises the zero-state path: a fresh
// HOME with no completed sessions yet renders the header + the
// in-flight session row, never an error and never "NaN%".
func TestStatsBuiltin_EmptyWindow(t *testing.T) {
	_, _ = chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "stats")

	body := out.String()
	if !strings.Contains(body, "session") || !strings.Contains(body, "cmds") {
		t.Errorf("stats: missing header columns; got=%q stderr=%q", body, errBuf.String())
	}
	if strings.Contains(body, "NaN") {
		t.Errorf("stats: NaN leaked into output: %q", body)
	}
	if s.LastExit() != 0 {
		t.Errorf("stats exit = %d, want 0", s.LastExit())
	}
}

// TestStatsBuiltin_ShowsCommandCount drives two no-op commands, then
// checks that `stats` prints the in-flight row with cmds >= 2. This
// is the smoke-test signal DevOps verifies in the live REPL gate.
func TestStatsBuiltin_ShowsCommandCount(t *testing.T) {
	_, _ = chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "echo hi", "echo bye")
	out.Reset()
	errBuf.Reset()
	driveLines(t, s, &out, &errBuf, "stats")

	body := out.String()
	// The in-flight session row will report cmds=2 (the two echos).
	// Format is "session  cmds  ..." so we look for a "  2  " column.
	if !strings.Contains(body, "  2  ") {
		t.Errorf("stats body did not show cmds=2 row: %q", body)
	}
}

// TestStatsBuiltin_UsageError covers the bad-arg path: `stats foo`
// should exit 2 with a Usage line to stderr.
func TestStatsBuiltin_UsageError(t *testing.T) {
	_, _ = chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "stats foo")

	if s.LastExit() != 2 {
		t.Errorf("stats foo exit = %d, want 2", s.LastExit())
	}
	if !strings.Contains(errBuf.String(), "Usage") {
		t.Errorf("expected Usage hint on stderr; got %q", errBuf.String())
	}
}

// TestStatsBuiltin_NoTelemetry checks the degraded path: when
// telemetry never opened (e.g. HOME unwritable), `stats` reports
// "telemetry not available" + exit 1. We simulate by clearing HOME
// AND USERPROFILE before constructing the Shell — openTelemetry
// then bails on the empty homeDir check.
func TestStatsBuiltin_NoTelemetry(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	// chdir to a tempdir so the shell starts in a known place.
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	s := New()
	defer s.Close()
	if s.telemetry != nil {
		t.Fatal("precondition: telemetry should have failed to open with no HOME")
	}

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "stats")
	if s.LastExit() != 1 {
		t.Errorf("stats with no telemetry exit = %d, want 1", s.LastExit())
	}
	if !strings.Contains(errBuf.String(), "not available") {
		t.Errorf("expected 'not available' on stderr; got %q", errBuf.String())
	}
}

// TestStatsBuiltin_RendersHistoricalRow seeds a completed session
// file under HOME/.aish/sessions/ and verifies `stats` picks it up.
func TestStatsBuiltin_RendersHistoricalRow(t *testing.T) {
	home, _ := chHome(t)
	seedSessionFile(t, home, "abcdef12-3456-4789-89ab-cdef01234567",
		`{"id":"abcdef12-3456-4789-89ab-cdef01234567","started_at":"2026-05-19T10:00:00Z","ended_at":"2026-05-19T11:00:00Z","counters":{"commands":42,"cache_hits":18,"cache_misses":24,"inference_calls":24,"failed_commands":3,"wall_time_ms":5000},"costs":{"total_usd":0.0421,"total_calls":24,"per_model":[{"model":"claude-sonnet","calls":24,"tokens_in":12000,"tokens_out":3000,"usd":0.0421}]},"schema_version":1}`)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "stats")
	body := out.String()
	if !strings.Contains(body, "abcdef12") {
		t.Errorf("historical session id not rendered: %q", body)
	}
	if !strings.Contains(body, "42") { // cmds=42
		t.Errorf("historical cmds=42 not rendered: %q", body)
	}
	if !strings.Contains(body, "$") {
		t.Errorf("cost column missing $ marker: %q", body)
	}
}

func seedSessionFile(t *testing.T, home, id, jsonBody string) {
	t.Helper()
	dir := home + "/.aish/sessions"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dir+"/"+id+".json", []byte(jsonBody), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
}
