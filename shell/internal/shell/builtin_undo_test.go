package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driveLines feeds each line into the shell's dispatch path with a
// FRESH empty stdin for child processes. This matches the interactive
// REPL contract (each typed command sees its own stdin state) while
// avoiding the multi-line-script edge case where a fast `rm` lets the
// os/exec stdin-copier goroutine drain bytes intended for the NEXT
// REPL iteration (the same hazard documented in issue #167; the
// readLine fix addresses cat-style readers but not scripted external
// pipelines).
func driveLines(t *testing.T, s *Shell, stdout, stderr *bytes.Buffer, lines ...string) {
	t.Helper()
	for _, ln := range lines {
		if err := s.dispatch(ln, strings.NewReader(""), stdout, stderr); err != nil {
			t.Fatalf("dispatch %q: %v (stderr=%q)", ln, err, stderr.String())
		}
	}
}

// chHome points $HOME at a writable tempdir and CDs the process into a
// separate working dir. Returns the working dir for path joins.
func chHome(t *testing.T) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return home, cwd
}

// TestBuiltinUndoRestoresLastDestructive is the unit-level acceptance
// for issue #35: `undo` restores the most-recent destructive op.
func TestBuiltinUndoRestoresLastDestructive(t *testing.T) {
	_, cwd := chHome(t)
	target := filepath.Join(cwd, "x.txt")
	if err := os.WriteFile(target, []byte("undo me"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "rm "+target, "undo")

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected restored file, got read err: %v (stdout=%q stderr=%q)",
			err, out.String(), errBuf.String())
	}
	if !bytes.Equal(got, []byte("undo me")) {
		t.Errorf("restored bytes mismatch: got %q", got)
	}
	if !strings.Contains(out.String(), "undo: restored") {
		t.Errorf("expected 'undo: restored' message, got stdout=%q stderr=%q",
			out.String(), errBuf.String())
	}
}

// TestBuiltinRestoreByPath verifies `restore <path>` (issue #36)
// restores a specific path even when later destructive ops have
// happened on other paths.
func TestBuiltinRestoreByPath(t *testing.T) {
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
		"restore "+a,
	)

	got, err := os.ReadFile(a)
	if err != nil {
		t.Fatalf("expected a restored, got err: %v (stderr=%q)", err, errBuf.String())
	}
	if !bytes.Equal(got, []byte("A")) {
		t.Errorf("a bytes mismatch: %q", got)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Errorf("b should still be gone, stat err: %v", err)
	}
}

// TestBuiltinUndoWithNothingToUndo verifies a graceful message when
// no restorable event exists. lastExit MUST be non-zero so a script
// can detect failure with $?.
func TestBuiltinUndoWithNothingToUndo(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "undo")

	combined := out.String() + errBuf.String()
	if !strings.Contains(strings.ToLower(combined), "nothing") {
		t.Errorf("expected 'nothing' message, got stdout=%q stderr=%q",
			out.String(), errBuf.String())
	}
	if s.LastExit() == 0 {
		t.Errorf("expected non-zero exit on empty undo, got 0")
	}
}

// TestBuiltinRestoreRejectsBadUsage verifies bare `restore` (no path)
// is a usage error.
func TestBuiltinRestoreRejectsBadUsage(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "restore foo bar")
	if !strings.Contains(errBuf.String(), "usage") {
		t.Errorf("expected usage message, got stderr=%q", errBuf.String())
	}
	if s.LastExit() == 0 {
		t.Errorf("expected non-zero exit, got 0")
	}
}
