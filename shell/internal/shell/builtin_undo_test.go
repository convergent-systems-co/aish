package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinUndoRestoresLastDestructive sets up a Shell with a tempdir
// HOME and a tempdir CWD, deletes a real file via the REPL, then runs
// `undo` and asserts the bytes are back.
func TestBuiltinUndoRestoresLastDestructive(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cwd, "x.txt")
	if err := os.WriteFile(target, []byte("undo me"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	// Drive the REPL with a script that deletes then undoes.
	in := strings.NewReader("rm " + target + "\nundo\n")
	if err := s.Run(in, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, errBuf.String())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected restored file, got read err: %v (stderr=%q)", err, errBuf.String())
	}
	if !bytes.Equal(got, []byte("undo me")) {
		t.Errorf("restored bytes mismatch: got %q", got)
	}
}

// TestBuiltinRestoreByPath verifies `aish restore <path>` restores a
// specific path, even when later destructive ops have happened on
// other paths.
func TestBuiltinRestoreByPath(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
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
	in := strings.NewReader("rm " + a + "\nrm " + b + "\nrestore " + a + "\n")
	if err := s.Run(in, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, errBuf.String())
	}
	// `a` should be back; `b` should still be gone (we only restored a).
	got, err := os.ReadFile(a)
	if err != nil {
		t.Fatalf("expected a restored, got err: %v", err)
	}
	if !bytes.Equal(got, []byte("A")) {
		t.Errorf("a bytes mismatch: %q", got)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Errorf("b should still be gone, stat err: %v", err)
	}
}

// TestBuiltinUndoWithNothingToUndo verifies a graceful message when
// no restorable event exists.
func TestBuiltinUndoWithNothingToUndo(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	in := strings.NewReader("undo\n")
	if err := s.Run(in, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Either stdout or stderr should mention "nothing"; lastExit should be non-zero.
	combined := out.String() + errBuf.String()
	if !strings.Contains(strings.ToLower(combined), "nothing") {
		t.Errorf("expected 'nothing' message, got stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}
