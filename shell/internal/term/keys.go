package term

import "unicode/utf8"

// KeyType is the enum of decoded keys the editor recognizes. It is
// deliberately small — we add types only for keys we actually bind.
type KeyType int

const (
	// KeyRune is "the user typed a printable character." The Rune
	// field on Key carries the value.
	KeyRune KeyType = iota
	KeyCtrlA
	KeyCtrlB
	KeyCtrlC
	KeyCtrlD
	KeyCtrlE
	KeyCtrlF
	KeyCtrlR
	KeyCtrlU
	KeyCtrlW
	KeyTab
	KeyEnter
	KeyBackspace
	KeyDelete
	KeyHome
	KeyEnd
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEsc
	// KeyUnknown is a recognized but unbound escape sequence. The
	// caller MAY ignore it; the decoder consumed the bytes so they
	// don't leak into the buffer.
	KeyUnknown
)

// String returns a human-readable label for debugging.
func (k KeyType) String() string {
	switch k {
	case KeyRune:
		return "Rune"
	case KeyCtrlA:
		return "Ctrl-A"
	case KeyCtrlB:
		return "Ctrl-B"
	case KeyCtrlC:
		return "Ctrl-C"
	case KeyCtrlD:
		return "Ctrl-D"
	case KeyCtrlE:
		return "Ctrl-E"
	case KeyCtrlF:
		return "Ctrl-F"
	case KeyCtrlR:
		return "Ctrl-R"
	case KeyCtrlU:
		return "Ctrl-U"
	case KeyCtrlW:
		return "Ctrl-W"
	case KeyTab:
		return "Tab"
	case KeyEnter:
		return "Enter"
	case KeyBackspace:
		return "Backspace"
	case KeyDelete:
		return "Delete"
	case KeyHome:
		return "Home"
	case KeyEnd:
		return "End"
	case KeyUp:
		return "Up"
	case KeyDown:
		return "Down"
	case KeyLeft:
		return "Left"
	case KeyRight:
		return "Right"
	case KeyEsc:
		return "Esc"
	case KeyUnknown:
		return "Unknown"
	}
	return "KeyType(?)"
}

// Key is one decoded keystroke.
type Key struct {
	Type KeyType
	// Rune is populated only for Type == KeyRune.
	Rune rune
}

// DecodeKey reads one key from the head of buf. Returns:
//
//   - the decoded Key,
//   - the number of bytes consumed,
//   - ok = true if a full key was decoded; false when buf is empty
//     or holds only a partial multi-byte escape sequence (the caller
//     should wait for more bytes).
//
// Recognized prefixes:
//
//   - Bare control bytes (0x01..0x1f, 0x7f) → KeyCtrl*, KeyEnter,
//     KeyTab, KeyBackspace, KeyEsc.
//   - `ESC [ <final>` (CSI sequences without parameters) →
//     KeyUp/Down/Left/Right.
//   - `ESC [ <param> ~` (CSI ~-terminated) → KeyHome/End/Delete.
//   - `ESC O <final>` (SS3) → KeyHome/End/Up/Down/Left/Right.
//   - Any other complete escape sequence → KeyUnknown.
func DecodeKey(buf []byte) (Key, int, bool) {
	if len(buf) == 0 {
		return Key{}, 0, false
	}
	b := buf[0]
	// Control bytes
	switch b {
	case 0x01:
		return Key{Type: KeyCtrlA}, 1, true
	case 0x02:
		return Key{Type: KeyCtrlB}, 1, true
	case 0x03:
		return Key{Type: KeyCtrlC}, 1, true
	case 0x04:
		return Key{Type: KeyCtrlD}, 1, true
	case 0x05:
		return Key{Type: KeyCtrlE}, 1, true
	case 0x06:
		return Key{Type: KeyCtrlF}, 1, true
	case 0x08:
		return Key{Type: KeyBackspace}, 1, true
	case 0x09:
		return Key{Type: KeyTab}, 1, true
	case 0x0a, 0x0d:
		return Key{Type: KeyEnter}, 1, true
	case 0x12:
		return Key{Type: KeyCtrlR}, 1, true
	case 0x15:
		return Key{Type: KeyCtrlU}, 1, true
	case 0x17:
		return Key{Type: KeyCtrlW}, 1, true
	case 0x7f:
		return Key{Type: KeyBackspace}, 1, true
	case 0x1b:
		return decodeEscape(buf)
	}
	// UTF-8 rune
	r, size := utf8.DecodeRune(buf)
	if r == utf8.RuneError && size <= 1 {
		// Invalid byte. Consume it as KeyUnknown so it doesn't loop.
		return Key{Type: KeyUnknown}, 1, true
	}
	return Key{Type: KeyRune, Rune: r}, size, true
}

// decodeEscape handles a buffer that starts with ESC (0x1b).
//
// The decoder is intentionally minimal — only the prefixes we bind.
// Anything else after a complete prefix is reported as KeyUnknown.
// A bare ESC (or ESC-prefix that's still pending) returns ok=false so
// the caller can wait for more bytes.
func decodeEscape(buf []byte) (Key, int, bool) {
	if len(buf) < 2 {
		return Key{}, 0, false
	}
	switch buf[1] {
	case '[':
		return decodeCSI(buf)
	case 'O':
		return decodeSS3(buf)
	}
	// `ESC <anything-else>` — treat as KeyEsc + a separate event.
	// We only consume the ESC; the next byte gets re-decoded on the
	// next call.
	return Key{Type: KeyEsc}, 1, true
}

// decodeCSI handles `ESC [ ...`. The minimum length is 3 bytes; the
// maximum we care about is 4 (`ESC [ <param> ~`).
func decodeCSI(buf []byte) (Key, int, bool) {
	if len(buf) < 3 {
		return Key{}, 0, false
	}
	switch buf[2] {
	case 'A':
		return Key{Type: KeyUp}, 3, true
	case 'B':
		return Key{Type: KeyDown}, 3, true
	case 'C':
		return Key{Type: KeyRight}, 3, true
	case 'D':
		return Key{Type: KeyLeft}, 3, true
	case 'H':
		return Key{Type: KeyHome}, 3, true
	case 'F':
		return Key{Type: KeyEnd}, 3, true
	}
	// param-then-tilde form: ESC [ <digits> ~
	if buf[2] >= '0' && buf[2] <= '9' {
		// scan until '~' or unknown
		for i := 3; i < len(buf); i++ {
			if buf[i] == '~' {
				switch buf[2] {
				case '1', '7':
					return Key{Type: KeyHome}, i + 1, true
				case '4', '8':
					return Key{Type: KeyEnd}, i + 1, true
				case '3':
					return Key{Type: KeyDelete}, i + 1, true
				}
				return Key{Type: KeyUnknown}, i + 1, true
			}
			if buf[i] < '0' || buf[i] > '9' {
				// Unknown CSI with non-digit, non-tilde — consume up
				// to and including this byte.
				return Key{Type: KeyUnknown}, i + 1, true
			}
		}
		// Still waiting for the tilde.
		return Key{}, 0, false
	}
	// Any other single trailer is a recognized-but-unbound CSI.
	return Key{Type: KeyUnknown}, 3, true
}

// decodeSS3 handles `ESC O <final>` (used by some terminals for the
// arrow + Home/End keys in application keypad mode).
func decodeSS3(buf []byte) (Key, int, bool) {
	if len(buf) < 3 {
		return Key{}, 0, false
	}
	switch buf[2] {
	case 'A':
		return Key{Type: KeyUp}, 3, true
	case 'B':
		return Key{Type: KeyDown}, 3, true
	case 'C':
		return Key{Type: KeyRight}, 3, true
	case 'D':
		return Key{Type: KeyLeft}, 3, true
	case 'H':
		return Key{Type: KeyHome}, 3, true
	case 'F':
		return Key{Type: KeyEnd}, 3, true
	}
	return Key{Type: KeyUnknown}, 3, true
}
