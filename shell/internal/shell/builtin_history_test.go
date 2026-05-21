package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHistoryList shows recent events newest-first.
func TestHistoryList(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "a.txt")
	b := filepath.Join(cwd, "b.txt")
	if err := os.WriteFile(a, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"rm "+b,
		"history list 5",
	)

	got := out.String()
	if !strings.Contains(got, "rm "+a) || !strings.Contains(got, "rm "+b) {
		t.Errorf("history list missing rm events: %q", got)
	}
	// Newest first: b's rm should appear before a's in the listing.
	if strings.Index(got, "rm "+b) > strings.Index(got, "rm "+a) {
		t.Errorf("history list order wrong (expected newest-first): %q", got)
	}
}

func TestHistorySearch_MatchesCommandSubstring(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "snapshot-target.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"history search snapshot",
	)
	if !strings.Contains(out.String(), "snapshot-target.txt") {
		t.Errorf("history search did not return the snapshot row: stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

func TestHistorySearch_NoMatchExitsNonZero(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "history search nothing-matches")
	if s.LastExit() == 0 {
		t.Errorf("expected non-zero exit on no-match; lastExit=0, stdout=%q", out.String())
	}
}

func TestHistoryShow_PrintsEventDetail(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "x.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "rm "+a)
	// Pull the event ID from `history list 1`.
	out.Reset()
	driveLines(t, s, &out, &errBuf, "history list 1")
	line := strings.TrimSpace(out.String())
	if line == "" {
		t.Fatalf("empty history list, stderr=%q", errBuf.String())
	}
	// Format: "<ts>  <kind>  <id>  <command>"
	fields := strings.Fields(line)
	var id string
	for _, f := range fields {
		if strings.HasPrefix(f, "evt_") {
			id = f
			break
		}
	}
	if id == "" {
		t.Fatalf("could not find event id in: %q", line)
	}
	out.Reset()
	driveLines(t, s, &out, &errBuf, "history show "+id)
	shown := out.String()
	if !strings.Contains(shown, "id:") || !strings.Contains(shown, id) {
		t.Errorf("history show missing id field; output=%q", shown)
	}
	if !strings.Contains(shown, "signature:") {
		t.Errorf("history show missing signature line; output=%q", shown)
	}
}

func TestHistoryCheckpointAndRollback(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "before.txt")
	b := filepath.Join(cwd, "after.txt")
	if err := os.WriteFile(a, []byte("A-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("B-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"history checkpoint pre-cleanup",
		"rm "+b,
		"history rollback pre-cleanup",
	)

	// `b` should be restored (rollback walked back the rm after the
	// checkpoint). `a` was rm'd before the checkpoint so it stays
	// removed.
	got, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("expected b restored after rollback, stat err: %v (stderr=%q)", err, errBuf.String())
	}
	if !bytes.Equal(got, []byte("B-content")) {
		t.Errorf("b bytes after rollback = %q, want B-content", got)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Errorf("a should remain removed (rm pre-checkpoint), stat err: %v", err)
	}
}

func TestHistoryRollback_UnknownCheckpoint(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "history rollback does-not-exist")
	if !strings.Contains(errBuf.String(), "no checkpoint") {
		t.Errorf("expected 'no checkpoint' message, got stderr=%q", errBuf.String())
	}
	if s.LastExit() == 0 {
		t.Errorf("expected non-zero exit on unknown checkpoint")
	}
}

func TestHistoryPurge(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "p.txt")
	if err := os.WriteFile(a, []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "rm "+a)

	// Purge everything older than "now + 1s" — should sweep the rm
	// event but leave no panic / parse error.
	cutoff := time.Now().Add(time.Second).Format(time.RFC3339)
	out.Reset()
	driveLines(t, s, &out, &errBuf, "history purge --before "+cutoff)
	if !strings.Contains(out.String(), "purged") {
		t.Errorf("expected 'purged' message, got stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

func TestHistoryMvRecordsRenameAndRestoresOnUndo(t *testing.T) {
	_, cwd := chHome(t)
	src := filepath.Join(cwd, "src.txt")
	dst := filepath.Join(cwd, "dst.txt")
	if err := os.WriteFile(src, []byte("mv-source"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "mv "+src+" "+dst, "undo")

	// After undo, the bytes should be back at src; dst should not
	// exist (the rename-restore tidied it).
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("src not restored: %v (stderr=%q)", err, errBuf.String())
	}
	if string(got) != "mv-source" {
		t.Errorf("src after undo = %q, want %q", got, "mv-source")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dst should be cleared after undo, stat err: %v", err)
	}
}

func TestHistoryUsageOnUnknownSubcommand(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "history bogus")
	if !strings.Contains(errBuf.String(), "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' message, got stderr=%q", errBuf.String())
	}
	if s.LastExit() == 0 {
		t.Errorf("expected non-zero exit, got 0")
	}
}
