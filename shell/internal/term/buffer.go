package term

import "unicode"

// Buffer is a rune-indexed line buffer with a single cursor position.
// All offsets are rune offsets, never byte offsets — multi-byte UTF-8
// runes count as one cursor position.
//
// Buffer is not safe for concurrent use; the editor's ReadLine loop
// is single-threaded.
type Buffer struct {
	runes  []rune
	cursor int
}

// NewBuffer returns an empty buffer with the cursor at 0.
func NewBuffer() *Buffer { return &Buffer{} }

// String returns the buffer contents.
func (b *Buffer) String() string { return string(b.runes) }

// Runes returns the buffer as a rune slice. Callers MUST treat the
// returned slice as read-only.
func (b *Buffer) Runes() []rune { return b.runes }

// Cursor returns the cursor position (in runes).
func (b *Buffer) Cursor() int { return b.cursor }

// Len returns the number of runes in the buffer.
func (b *Buffer) Len() int { return len(b.runes) }

// SetText replaces the buffer contents. The cursor is clamped to the
// new length.
func (b *Buffer) SetText(s string) {
	b.runes = []rune(s)
	if b.cursor > len(b.runes) {
		b.cursor = len(b.runes)
	}
}

// SetCursor moves the cursor; clamps to [0, len(runes)].
func (b *Buffer) SetCursor(c int) {
	if c < 0 {
		c = 0
	}
	if c > len(b.runes) {
		c = len(b.runes)
	}
	b.cursor = c
}

// Clear empties the buffer and resets the cursor to 0.
func (b *Buffer) Clear() {
	b.runes = b.runes[:0]
	b.cursor = 0
}

// InsertRune inserts r at the cursor and advances the cursor by one.
func (b *Buffer) InsertRune(r rune) {
	if b.cursor == len(b.runes) {
		b.runes = append(b.runes, r)
		b.cursor++
		return
	}
	// Splice in.
	b.runes = append(b.runes, 0)
	copy(b.runes[b.cursor+1:], b.runes[b.cursor:])
	b.runes[b.cursor] = r
	b.cursor++
}

// Backspace deletes the rune at cursor-1; no-op when cursor == 0.
func (b *Buffer) Backspace() {
	if b.cursor == 0 {
		return
	}
	b.runes = append(b.runes[:b.cursor-1], b.runes[b.cursor:]...)
	b.cursor--
}

// Delete deletes the rune AT the cursor; no-op when cursor == len.
func (b *Buffer) Delete() {
	if b.cursor >= len(b.runes) {
		return
	}
	b.runes = append(b.runes[:b.cursor], b.runes[b.cursor+1:]...)
}

// MoveLeft decrements the cursor; no-op at 0.
func (b *Buffer) MoveLeft() {
	if b.cursor > 0 {
		b.cursor--
	}
}

// MoveRight increments the cursor; no-op at len.
func (b *Buffer) MoveRight() {
	if b.cursor < len(b.runes) {
		b.cursor++
	}
}

// Home moves the cursor to 0.
func (b *Buffer) Home() { b.cursor = 0 }

// End moves the cursor to len(runes).
func (b *Buffer) End() { b.cursor = len(b.runes) }

// KillToStart deletes everything before the cursor (Ctrl-U behavior
// when bound to "kill-to-start"). The cursor moves to 0.
func (b *Buffer) KillToStart() {
	b.runes = append([]rune{}, b.runes[b.cursor:]...)
	b.cursor = 0
}

// KillToEnd deletes everything from the cursor to the end of the
// buffer. The cursor stays.
func (b *Buffer) KillToEnd() {
	b.runes = b.runes[:b.cursor]
}

// KillWord deletes the previous whitespace-separated word (Ctrl-W
// semantics). Whitespace between the cursor and the word is NOT
// deleted — it remains as the boundary marker. When only whitespace
// precedes the cursor (no word), that whitespace IS deleted.
//
// Algorithm:
//
//  1. Walk left over whitespace; remember that position as `wsEnd`.
//  2. Walk left over non-whitespace; that's the word start.
//  3. If we found a word (start < wsEnd), delete [start, wsEnd) —
//     the word only, NOT the trailing whitespace.
//  4. If only whitespace was found (no word), delete [0, cursor) —
//     the leading whitespace itself.
func (b *Buffer) KillWord() {
	if b.cursor == 0 {
		return
	}
	end := b.cursor
	// Phase 1: skip trailing whitespace.
	wsEnd := end
	for wsEnd > 0 && unicode.IsSpace(b.runes[wsEnd-1]) {
		wsEnd--
	}
	// Phase 2: skip the word.
	start := wsEnd
	for start > 0 && !unicode.IsSpace(b.runes[start-1]) {
		start--
	}
	if start == wsEnd {
		// No word found — kill the leading whitespace instead.
		b.runes = append(b.runes[:start], b.runes[end:]...)
		b.cursor = start
		return
	}
	b.runes = append(b.runes[:start], b.runes[wsEnd:]...)
	b.cursor = start
}

// TokenAtCursor returns the whitespace-separated token the cursor is
// "on" — defined as the cursor being immediately after a
// non-whitespace rune (or in the empty-buffer case, cursor==0 returns
// the empty first token).
//
// Returns:
//   - the token text (empty if cursor is on whitespace),
//   - the rune index of the token's first rune (or the cursor itself
//     when no token),
//   - whether the token is the first non-whitespace token of the line.
//
// Examples:
//
//	"" cursor=0           → ("", 0, true)        empty first token
//	"ech" cursor=3        → ("ech", 0, true)     first token, mid-typing
//	"echo hel" cursor=8   → ("hel", 5, false)    second token
//	"echo hello" cursor=5 → ("", 5, false)       cursor on whitespace
func (b *Buffer) TokenAtCursor() (string, int, bool) {
	c := b.cursor
	// Empty buffer / cursor==0: the empty first token.
	if len(b.runes) == 0 {
		return "", 0, true
	}
	// Cursor must be immediately AFTER a non-whitespace rune to be
	// "on a token." Otherwise we're on whitespace.
	if c == 0 || unicode.IsSpace(b.runes[c-1]) {
		// Is the line all-whitespace so far? If yes, isFirst=true.
		isFirst := true
		for i := 0; i < c; i++ {
			if !unicode.IsSpace(b.runes[i]) {
				isFirst = false
				break
			}
		}
		return "", c, isFirst
	}
	// Walk back from cursor while we're on non-whitespace to find the
	// token's start.
	start := c
	for start > 0 && !unicode.IsSpace(b.runes[start-1]) {
		start--
	}
	// Walk forward from cursor while we're on non-whitespace to find
	// the token's end.
	end := c
	for end < len(b.runes) && !unicode.IsSpace(b.runes[end]) {
		end++
	}
	isFirst := true
	for i := 0; i < start; i++ {
		if !unicode.IsSpace(b.runes[i]) {
			isFirst = false
			break
		}
	}
	return string(b.runes[start:end]), start, isFirst
}
