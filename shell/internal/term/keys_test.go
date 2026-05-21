package term

import (
	"strings"
	"testing"
)

// TestDecodeKey_PlainRune — a single ASCII byte decodes as a rune key.
func TestDecodeKey_PlainRune(t *testing.T) {
	k, consumed, ok := DecodeKey([]byte("a"))
	if !ok {
		t.Fatalf("expected decode of 'a' to succeed")
	}
	if k.Type != KeyRune || k.Rune != 'a' {
		t.Fatalf("expected KeyRune('a'); got %+v", k)
	}
	if consumed != 1 {
		t.Fatalf("expected consumed=1; got %d", consumed)
	}
}

// TestDecodeKey_MultiByteRune — a 2-byte UTF-8 rune (`é`) decodes as
// one KeyRune consuming both bytes.
func TestDecodeKey_MultiByteRune(t *testing.T) {
	in := []byte("é") // 0xc3 0xa9
	k, consumed, ok := DecodeKey(in)
	if !ok {
		t.Fatalf("expected decode of 'é' to succeed; in=%q", in)
	}
	if k.Type != KeyRune || k.Rune != 'é' {
		t.Fatalf("expected KeyRune('é'); got %+v", k)
	}
	if consumed != 2 {
		t.Fatalf("expected consumed=2; got %d", consumed)
	}
}

// TestDecodeKey_ControlCodes — every Ctrl-* and named-control byte we
// rely on decodes to the right KeyType.
func TestDecodeKey_ControlCodes(t *testing.T) {
	cases := []struct {
		name  string
		in    byte
		want  KeyType
		wantR rune
	}{
		{"Ctrl-A", 0x01, KeyCtrlA, 0},
		{"Ctrl-B", 0x02, KeyCtrlB, 0},
		{"Ctrl-C", 0x03, KeyCtrlC, 0},
		{"Ctrl-D", 0x04, KeyCtrlD, 0},
		{"Ctrl-E", 0x05, KeyCtrlE, 0},
		{"Ctrl-F", 0x06, KeyCtrlF, 0},
		{"Tab", 0x09, KeyTab, 0},
		{"Enter (LF)", 0x0a, KeyEnter, 0},
		{"Enter (CR)", 0x0d, KeyEnter, 0},
		{"Ctrl-R", 0x12, KeyCtrlR, 0},
		{"Ctrl-U", 0x15, KeyCtrlU, 0},
		{"Ctrl-W", 0x17, KeyCtrlW, 0},
		{"Backspace (DEL)", 0x7f, KeyBackspace, 0},
		{"Backspace (BS)", 0x08, KeyBackspace, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, consumed, ok := DecodeKey([]byte{tc.in})
			if !ok {
				t.Fatalf("decode failed for byte 0x%02x", tc.in)
			}
			if k.Type != tc.want {
				t.Fatalf("byte 0x%02x: want %v, got %v", tc.in, tc.want, k.Type)
			}
			if consumed != 1 {
				t.Fatalf("byte 0x%02x: expected consumed=1, got %d", tc.in, consumed)
			}
		})
	}
}

// TestDecodeKey_CSIArrows — the four arrow keys arrive as the CSI
// sequences `ESC [ A/B/C/D`. The decoder MUST consume all three
// bytes and report the right KeyType.
func TestDecodeKey_CSIArrows(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want KeyType
	}{
		{"Up", "\x1b[A", KeyUp},
		{"Down", "\x1b[B", KeyDown},
		{"Right", "\x1b[C", KeyRight},
		{"Left", "\x1b[D", KeyLeft},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, consumed, ok := DecodeKey([]byte(tc.in))
			if !ok {
				t.Fatalf("decode failed for %q", tc.in)
			}
			if k.Type != tc.want {
				t.Fatalf("want %v, got %v", tc.want, k.Type)
			}
			if consumed != len(tc.in) {
				t.Fatalf("expected consumed=%d, got %d", len(tc.in), consumed)
			}
		})
	}
}

// TestDecodeKey_HomeEnd — Home / End come in two flavors: the legacy
// CSI~ form (`ESC [ 1 ~` / `ESC [ 4 ~`) and the SS3 form (`ESC O H` /
// `ESC O F`). Both must decode.
func TestDecodeKey_HomeEnd(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want KeyType
	}{
		{"Home CSI~", "\x1b[1~", KeyHome},
		{"Home SS3", "\x1bOH", KeyHome},
		{"End CSI~", "\x1b[4~", KeyEnd},
		{"End SS3", "\x1bOF", KeyEnd},
		{"Delete", "\x1b[3~", KeyDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, consumed, ok := DecodeKey([]byte(tc.in))
			if !ok {
				t.Fatalf("decode failed for %q", tc.in)
			}
			if k.Type != tc.want {
				t.Fatalf("want %v, got %v", tc.want, k.Type)
			}
			if consumed != len(tc.in) {
				t.Fatalf("expected consumed=%d, got %d", len(tc.in), consumed)
			}
		})
	}
}

// TestDecodeKey_PartialSequence — when the input is only a bare ESC,
// the decoder returns ok=false so the caller can wait for more bytes
// or time out into a KeyEsc.
func TestDecodeKey_PartialSequence(t *testing.T) {
	_, _, ok := DecodeKey([]byte{0x1b})
	if ok {
		t.Fatalf("bare ESC should return ok=false (waiting for more)")
	}
	_, _, ok = DecodeKey([]byte{0x1b, '['})
	if ok {
		t.Fatalf("ESC [ should return ok=false (waiting for the final byte)")
	}
}

// TestDecodeKey_UnknownEscape — a CSI sequence with a trailing byte
// we don't recognize is consumed as KeyUnknown (so the caller doesn't
// silently leak escape bytes into the buffer).
func TestDecodeKey_UnknownEscape(t *testing.T) {
	k, consumed, ok := DecodeKey([]byte("\x1b[Z")) // shift-tab on xterm
	if !ok {
		t.Fatalf("decode of unknown CSI should succeed as KeyUnknown")
	}
	if k.Type != KeyUnknown {
		t.Fatalf("want KeyUnknown, got %v", k.Type)
	}
	if consumed != 3 {
		t.Fatalf("expected consumed=3, got %d", consumed)
	}
}

// TestDecodeKey_EmptyInput — calling with an empty slice MUST NOT
// panic and MUST return ok=false.
func TestDecodeKey_EmptyInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecodeKey panicked on empty input: %v", r)
		}
	}()
	_, _, ok := DecodeKey(nil)
	if ok {
		t.Fatalf("empty input should return ok=false")
	}
}

// TestKeyType_String — KeyType.String must return a non-empty,
// human-readable label for every defined value (debugging affordance).
func TestKeyType_String(t *testing.T) {
	for kt := KeyRune; kt <= KeyUnknown; kt++ {
		s := kt.String()
		if s == "" || strings.HasPrefix(s, "KeyType(") {
			// "KeyType(N)" is the stringer fallback for an undefined
			// value — every defined value should have a real name.
			t.Errorf("KeyType(%d) has no human name: %q", kt, s)
		}
	}
}
