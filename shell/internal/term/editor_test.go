package term

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestEditor_ReadLine_BasicAcceptEnter — typing 'h' 'i' Enter returns
// "hi" with no error.
func TestEditor_ReadLine_BasicAcceptEnter(t *testing.T) {
	in := bytes.NewReader([]byte("hi\r"))
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "hi" {
		t.Fatalf("want \"hi\", got %q", got)
	}
}

// TestEditor_ReadLine_CtrlD_OnEmpty — Ctrl-D on an empty buffer
// returns io.EOF.
func TestEditor_ReadLine_CtrlD_OnEmpty(t *testing.T) {
	in := bytes.NewReader([]byte{0x04}) // Ctrl-D
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	_, err := ed.ReadLine(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF; got %v", err)
	}
}

// TestEditor_ReadLine_CtrlC_AbortsLine — Ctrl-C returns
// ErrInterrupt; the line is discarded; the caller can loop.
func TestEditor_ReadLine_CtrlC_AbortsLine(t *testing.T) {
	in := bytes.NewReader([]byte{'a', 'b', 0x03}) // ab^C
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if !errors.Is(err, ErrInterrupt) {
		t.Fatalf("expected ErrInterrupt; got %v", err)
	}
	if got != "" {
		t.Fatalf("Ctrl-C should discard the buffer; got %q", got)
	}
}

// TestEditor_ReadLine_BackspaceDeletes — type 'a' 'b' BS Enter →
// "a".
func TestEditor_ReadLine_BackspaceDeletes(t *testing.T) {
	in := bytes.NewReader([]byte{'a', 'b', 0x7f, '\r'})
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "a" {
		t.Fatalf("want \"a\", got %q", got)
	}
}

// TestEditor_ReadLine_HistoryUp — Up arrow recalls the most-recent
// history line.
func TestEditor_ReadLine_HistoryUp(t *testing.T) {
	src := NewMemorySource([]string{"first", "second"})
	in := bytes.NewReader([]byte("\x1b[A\r")) // Up Enter
	var out bytes.Buffer
	ed := newTestEditorWithHistory(in, &out, src)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "second" {
		t.Fatalf("want \"second\", got %q", got)
	}
}

// TestEditor_ReadLine_GhostAcceptedByRight — type "se" with history
// ["second"]; Right accepts the suggestion → "second".
func TestEditor_ReadLine_GhostAcceptedByRight(t *testing.T) {
	src := NewMemorySource([]string{"second"})
	// "se" then Right then Enter
	in := bytes.NewReader([]byte("se\x1b[C\r"))
	var out bytes.Buffer
	ed := newTestEditorWithHistory(in, &out, src)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "second" {
		t.Fatalf("want \"second\", got %q", got)
	}
}

// TestEditor_ReadLine_TabCyclesCompletions — given a completer that
// returns ["alpha", "beta"], successive Tabs cycle.
func TestEditor_ReadLine_TabCyclesCompletions(t *testing.T) {
	// Two tabs then Enter. The completer returns the same two on each
	// call; the editor cycles.
	in := bytes.NewReader([]byte("\t\t\r"))
	var out bytes.Buffer
	stubC := stubCompleter{matches: []string{"alpha", "beta"}}
	ed := newTestEditorWithCompleter(in, &out, stubC)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "beta" {
		t.Fatalf("two tabs should land on \"beta\"; got %q", got)
	}
}

// TestEditor_ReadLine_RestoresTerminalOnError — if the underlying
// reader errors, the editor's deferred Restore call MUST fire. We
// verify via the fakeTerm that Restore was called exactly once.
func TestEditor_ReadLine_RestoresTerminalOnError(t *testing.T) {
	in := strings.NewReader("") // EOF immediately
	var out bytes.Buffer
	ft := &fakeTerm{}
	ed := newTestEditorWithFakeTerm(in, &out, ft)
	_, _ = ed.ReadLine(context.Background())
	if ft.enterCalls != 1 {
		t.Errorf("Enter should be called once; got %d", ft.enterCalls)
	}
	if ft.restoreCalls != 1 {
		t.Errorf("Restore should be called once even on error; got %d", ft.restoreCalls)
	}
}

// TestEditor_ReadLine_HomeEndKeys — Home then 'x' Enter inserts 'x'
// at the start.
func TestEditor_ReadLine_HomeEndKeys(t *testing.T) {
	// "abc" then Home then 'x' then End then Enter → "xabc"
	in := bytes.NewReader([]byte("abc\x1b[Hx\x1b[F\r"))
	var out bytes.Buffer
	ed := newTestEditor(in, &out)
	got, err := ed.ReadLine(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "xabc" {
		t.Fatalf("want \"xabc\", got %q", got)
	}
}

// ----- test helpers -----

// fakeTerm satisfies the RawTerminal interface for tests. Records
// the number of Enter / Restore calls to detect leaks.
type fakeTerm struct {
	enterCalls   int
	restoreCalls int
}

func (f *fakeTerm) Enter() error {
	f.enterCalls++
	return nil
}

func (f *fakeTerm) Restore() error {
	f.restoreCalls++
	return nil
}

type stubCompleter struct{ matches []string }

func (s stubCompleter) Complete(ctx CompletionContext) ([]string, bool) {
	return s.matches, true
}

func newTestEditor(in io.Reader, out io.Writer) *Editor {
	return NewEditor(Config{
		Stdin:     in,
		Stdout:    out,
		Prompt:    func() string { return "$ " },
		History:   NewMemorySource(nil),
		Resolver:  stubResolver{},
		Completer: stubCompleter{},
		RawTerm:   &fakeTerm{},
	})
}

func newTestEditorWithHistory(in io.Reader, out io.Writer, src *MemorySource) *Editor {
	return NewEditor(Config{
		Stdin:     in,
		Stdout:    out,
		Prompt:    func() string { return "$ " },
		History:   src,
		Resolver:  stubResolver{},
		Completer: stubCompleter{},
		RawTerm:   &fakeTerm{},
	})
}

func newTestEditorWithCompleter(in io.Reader, out io.Writer, c Completer) *Editor {
	return NewEditor(Config{
		Stdin:     in,
		Stdout:    out,
		Prompt:    func() string { return "$ " },
		History:   NewMemorySource(nil),
		Resolver:  stubResolver{},
		Completer: c,
		RawTerm:   &fakeTerm{},
	})
}

func newTestEditorWithFakeTerm(in io.Reader, out io.Writer, ft *fakeTerm) *Editor {
	return NewEditor(Config{
		Stdin:     in,
		Stdout:    out,
		Prompt:    func() string { return "$ " },
		History:   NewMemorySource(nil),
		Resolver:  stubResolver{},
		Completer: stubCompleter{},
		RawTerm:   ft,
	})
}
