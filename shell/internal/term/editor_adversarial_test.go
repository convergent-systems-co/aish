package term

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// Adversarial seatbelts for the v0.2-1 editor — bugs we deliberately
// don't ship. Each test names a specific failure mode and pins it
// closed.

// TestEditor_TabWithNoCompletions_NoOp — Tab when the completer
// returns nothing must not modify the buffer or hang.
func TestEditor_TabWithNoCompletions_NoOp(t *testing.T) {
	in := bytes.NewReader([]byte("ab\tcd\r")) // ab + Tab + cd + Enter
	var out bytes.Buffer
	ed := newTestEditorWithCompleter(in, &out, stubCompleter{matches: nil})
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "abcd" {
		t.Fatalf("Tab on no-match should be a no-op; got %q", got)
	}
}

// TestEditor_GhostNotConsumedByTab — when a ghost suggestion is
// showing, Tab must run the completer, NOT accept the ghost. (Right
// or Ctrl-F accepts the ghost; that's tested separately.)
func TestEditor_GhostNotConsumedByTab(t *testing.T) {
	src := NewMemorySource([]string{"alpha-suggested"})
	// Type "al", then Tab. The completer returns ["accepted-by-tab"].
	// If Tab consumed the ghost, we'd see "alpha-suggested";
	// if Tab ran the completer, we see "accepted-by-tab".
	in := bytes.NewReader([]byte("al\t\r"))
	var out bytes.Buffer
	ed := NewEditor(Config{
		Stdin:     in,
		Stdout:    &out,
		Prompt:    func() string { return "" },
		History:   src,
		Resolver:  stubResolver{},
		Completer: stubCompleter{matches: []string{"accepted-by-tab"}},
		RawTerm:   &fakeTerm{},
	})
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "accepted-by-tab" {
		t.Fatalf("Tab should run the completer, NOT accept ghost; got %q", got)
	}
}

// TestEditor_CtrlURemovesAllBeforeCursor — Ctrl-U bound to
// kill-to-start. Type "abc" + Ctrl-U + "x" + Enter → "x".
func TestEditor_CtrlURemovesAllBeforeCursor(t *testing.T) {
	in := bytes.NewReader([]byte{'a', 'b', 'c', 0x15, 'x', '\r'})
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "x" {
		t.Fatalf("Ctrl-U should kill-to-start; got %q", got)
	}
}

// TestEditor_CtrlWDeletesWord — Type "echo hello" + Ctrl-W + Enter
// → "echo " (with trailing space, per the buffer test contract).
func TestEditor_CtrlWDeletesWord(t *testing.T) {
	in := bytes.NewReader(append([]byte("echo hello"), 0x17, '\r'))
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "echo " {
		t.Fatalf("Ctrl-W on \"echo hello\" should leave \"echo \"; got %q", got)
	}
}

// TestEditor_MultipleArrowLeftThenInsert — Left arrows past the start
// must clamp (not panic / not corrupt the buffer).
func TestEditor_MultipleArrowLeftThenInsert(t *testing.T) {
	// Type "abc", then 5 x Left (only 3 should move; the rest clamp
	// at 0), then 'x', then Enter → "xabc".
	in := bytes.NewReader([]byte("abc\x1b[D\x1b[D\x1b[D\x1b[D\x1b[Dx\r"))
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "xabc" {
		t.Fatalf("excess Lefts must clamp at 0; got %q", got)
	}
}

// TestEditor_ReadLine_RestoresOnPanic — even if the consumer code
// panics, the deferred Restore in ReadLine runs. We simulate via a
// reader that returns an error.
func TestEditor_ReadLine_RestoresOnReadError(t *testing.T) {
	rd := errReader{}
	var out bytes.Buffer
	ft := &fakeTerm{}
	ed := newTestEditorWithFakeTerm(&rd, &out, ft)
	_, _ = ed.ReadLine(context.Background())
	if ft.enterCalls != 1 || ft.restoreCalls != 1 {
		t.Fatalf("Enter/Restore call counts: want (1,1); got (%d,%d)",
			ft.enterCalls, ft.restoreCalls)
	}
}

// TestEditor_NonTTYStubReader — a strings.Reader (definitely not a
// TTY) used as Stdin still drives the editor to a clean EOF. The
// TTY-gate is the caller's job (shell.Run); the editor itself must
// behave on any Reader.
func TestEditor_NonTTYStubReader(t *testing.T) {
	in := strings.NewReader("hello\r")
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "hello" {
		t.Fatalf("want \"hello\", got %q", got)
	}
}

// errReader is an io.Reader that always returns an error.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrClosedPipe }
