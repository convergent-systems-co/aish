package term

import "testing"

// TestBuffer_InsertRune — inserting a rune at the cursor advances both
// the buffer length and the cursor by one rune.
func TestBuffer_InsertRune(t *testing.T) {
	b := NewBuffer()
	b.InsertRune('h')
	b.InsertRune('i')
	if got := b.String(); got != "hi" {
		t.Fatalf("want %q, got %q", "hi", got)
	}
	if b.Cursor() != 2 {
		t.Fatalf("want cursor=2, got %d", b.Cursor())
	}
}

// TestBuffer_InsertInMiddle — insert at a non-end cursor splices in.
func TestBuffer_InsertInMiddle(t *testing.T) {
	b := NewBuffer()
	b.SetText("hi")
	b.SetCursor(1)
	b.InsertRune('!')
	if got := b.String(); got != "h!i" {
		t.Fatalf("want %q, got %q", "h!i", got)
	}
	if b.Cursor() != 2 {
		t.Fatalf("want cursor=2, got %d", b.Cursor())
	}
}

// TestBuffer_MultiByteRune — a multi-byte rune occupies ONE cursor
// position. Internal storage is rune-indexed.
func TestBuffer_MultiByteRune(t *testing.T) {
	b := NewBuffer()
	b.InsertRune('é')
	b.InsertRune('!')
	if got := b.String(); got != "é!" {
		t.Fatalf("want %q, got %q", "é!", got)
	}
	if b.Cursor() != 2 {
		t.Fatalf("want cursor=2 (runes, not bytes); got %d", b.Cursor())
	}
}

// TestBuffer_Backspace — deletes the rune at cursor-1; no-op at start.
func TestBuffer_Backspace(t *testing.T) {
	b := NewBuffer()
	b.SetText("hi")
	b.SetCursor(2)
	b.Backspace()
	if got := b.String(); got != "h" {
		t.Fatalf("want %q, got %q", "h", got)
	}
	if b.Cursor() != 1 {
		t.Fatalf("want cursor=1, got %d", b.Cursor())
	}
	b.SetCursor(0)
	b.Backspace() // no-op
	if got := b.String(); got != "h" {
		t.Fatalf("backspace at start should be a no-op; got %q", got)
	}
	if b.Cursor() != 0 {
		t.Fatalf("want cursor=0 after no-op backspace, got %d", b.Cursor())
	}
}

// TestBuffer_Delete — deletes the rune AT the cursor; no-op at end.
func TestBuffer_Delete(t *testing.T) {
	b := NewBuffer()
	b.SetText("hi")
	b.SetCursor(0)
	b.Delete()
	if got := b.String(); got != "i" {
		t.Fatalf("want %q, got %q", "i", got)
	}
	if b.Cursor() != 0 {
		t.Fatalf("want cursor=0, got %d", b.Cursor())
	}
	b.SetCursor(1)
	b.Delete() // no-op at end
	if got := b.String(); got != "i" {
		t.Fatalf("delete at end should be a no-op; got %q", got)
	}
}

// TestBuffer_MoveCursor — Move{Left,Right,Home,End} clamp at the
// boundaries and never go negative.
func TestBuffer_MoveCursor(t *testing.T) {
	b := NewBuffer()
	b.SetText("hello")
	b.End()
	if b.Cursor() != 5 {
		t.Fatalf("End() should land at len(runes); got %d", b.Cursor())
	}
	b.MoveRight() // already at end — no-op
	if b.Cursor() != 5 {
		t.Fatalf("MoveRight at end should be a no-op; got %d", b.Cursor())
	}
	b.Home()
	if b.Cursor() != 0 {
		t.Fatalf("Home() should land at 0; got %d", b.Cursor())
	}
	b.MoveLeft() // at 0 — no-op
	if b.Cursor() != 0 {
		t.Fatalf("MoveLeft at 0 should be a no-op; got %d", b.Cursor())
	}
	b.MoveRight()
	if b.Cursor() != 1 {
		t.Fatalf("MoveRight from 0 should advance to 1; got %d", b.Cursor())
	}
}

// TestBuffer_KillToEnd — Ctrl-U analog (kill-to-start-of-line). The
// editor binds Ctrl-U to "clear whole line" by aliasing to
// KillToStart on a Cursor==len buffer; we test the primitive.
func TestBuffer_KillToStart(t *testing.T) {
	b := NewBuffer()
	b.SetText("hello world")
	b.SetCursor(6) // before 'w'
	b.KillToStart()
	if got := b.String(); got != "world" {
		t.Fatalf("want %q, got %q", "world", got)
	}
	if b.Cursor() != 0 {
		t.Fatalf("want cursor=0, got %d", b.Cursor())
	}
}

// TestBuffer_KillWord — Ctrl-W: delete the previous whitespace-
// separated word, including trailing-space.
func TestBuffer_KillWord(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		cursor int
		want   string
		wantC  int
	}{
		{
			name:   "mid-line word",
			in:     "echo hello world",
			cursor: 11, // after 'hello'
			want:   "echo  world",
			wantC:  5,
		},
		{
			name:   "at-end of line",
			in:     "echo hello",
			cursor: 10,
			want:   "echo ",
			wantC:  5,
		},
		{
			name:   "all whitespace before cursor",
			in:     "   foo",
			cursor: 3,
			want:   "foo",
			wantC:  0,
		},
		{
			name:   "empty buffer",
			in:     "",
			cursor: 0,
			want:   "",
			wantC:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuffer()
			b.SetText(tc.in)
			b.SetCursor(tc.cursor)
			b.KillWord()
			if got := b.String(); got != tc.want {
				t.Errorf("text: want %q, got %q", tc.want, got)
			}
			if b.Cursor() != tc.wantC {
				t.Errorf("cursor: want %d, got %d", tc.wantC, b.Cursor())
			}
		})
	}
}

// TestBuffer_SetText — SetText replaces the buffer and clamps the
// cursor to len(runes).
func TestBuffer_SetText(t *testing.T) {
	b := NewBuffer()
	b.SetText("hello")
	b.SetCursor(5)
	b.SetText("hi")
	if b.Cursor() > 2 {
		t.Fatalf("SetText must clamp cursor; got %d", b.Cursor())
	}
	if got := b.String(); got != "hi" {
		t.Fatalf("want %q, got %q", "hi", got)
	}
}

// TestBuffer_TokenAtCursor — returns the whitespace-separated token
// the cursor is inside, the index of the first rune of that token,
// and whether the cursor is on the first token. Used by the completer.
func TestBuffer_TokenAtCursor(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		cursor      int
		wantTok     string
		wantStart   int
		wantIsFirst bool
	}{
		{
			name:        "first token of an empty buffer",
			in:          "",
			cursor:      0,
			wantTok:     "",
			wantStart:   0,
			wantIsFirst: true,
		},
		{
			name:        "first token mid-typing",
			in:          "ech",
			cursor:      3,
			wantTok:     "ech",
			wantStart:   0,
			wantIsFirst: true,
		},
		{
			name:        "second token",
			in:          "echo hel",
			cursor:      8,
			wantTok:     "hel",
			wantStart:   5,
			wantIsFirst: false,
		},
		{
			name:        "cursor in whitespace between tokens",
			in:          "echo hello",
			cursor:      5, // on the space
			wantTok:     "",
			wantStart:   5,
			wantIsFirst: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuffer()
			b.SetText(tc.in)
			b.SetCursor(tc.cursor)
			tok, start, isFirst := b.TokenAtCursor()
			if tok != tc.wantTok {
				t.Errorf("tok: want %q, got %q", tc.wantTok, tok)
			}
			if start != tc.wantStart {
				t.Errorf("start: want %d, got %d", tc.wantStart, start)
			}
			if isFirst != tc.wantIsFirst {
				t.Errorf("isFirst: want %v, got %v", tc.wantIsFirst, isFirst)
			}
		})
	}
}
