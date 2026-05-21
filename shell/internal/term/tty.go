package term

import (
	"errors"
	"io"
	"os"

	"golang.org/x/term"
)

// IsTTY reports whether r is a terminal file descriptor. A non-*os.File
// reader (bytes.Buffer, strings.Reader, *os.Pipe from a script) returns
// false. A nil reader returns false without panic.
//
// This is the gate the shell uses to decide whether to enter the
// interactive line editor or fall through to the byte-by-byte readLine
// path that preserves issue #167's "cat stdin lines flow through to
// the child" behaviour.
func IsTTY(r io.Reader) bool {
	if r == nil {
		return false
	}
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// RawTerminal abstracts the termios state machine so the Editor can be
// tested with a fake. Production wiring uses NewRawTerminal which wraps
// golang.org/x/term's MakeRaw / Restore.
type RawTerminal interface {
	// Enter puts the terminal into raw mode. Calling Enter twice without
	// an intervening Restore is an error.
	Enter() error
	// Restore returns the terminal to its pre-Enter state. Safe to call
	// when Enter never succeeded — Restore is a no-op then.
	Restore() error
}

// NewRawTerminal builds a RawTerminal for an *os.File. Returns an error
// if f is not a TTY — the caller should fall back to non-TTY input.
func NewRawTerminal(f *os.File) (RawTerminal, error) {
	if f == nil {
		return nil, errors.New("term: nil file")
	}
	if !term.IsTerminal(int(f.Fd())) {
		return nil, errors.New("term: not a TTY")
	}
	return &fileRawTerm{fd: int(f.Fd())}, nil
}

type fileRawTerm struct {
	fd       int
	prev     *term.State
	inRawMod bool
}

func (t *fileRawTerm) Enter() error {
	if t.inRawMod {
		return errors.New("term: already in raw mode")
	}
	st, err := term.MakeRaw(t.fd)
	if err != nil {
		return err
	}
	t.prev = st
	t.inRawMod = true
	return nil
}

func (t *fileRawTerm) Restore() error {
	if !t.inRawMod {
		return nil
	}
	err := term.Restore(t.fd, t.prev)
	t.inRawMod = false
	t.prev = nil
	return err
}
