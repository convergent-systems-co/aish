package stream

import (
	"bytes"
	"strings"
	"testing"
)

// TestDetectTable exercises every Kind in one place. Each case states the
// reason its input must classify the way the plan §"T3 — Output stream
// type detection" requires.
func TestDetectTable(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  Kind
	}{
		{
			name:  "empty input",
			input: nil,
			want:  KindText,
		},
		{
			name:  "plain text",
			input: []byte("hello world"),
			want:  KindText,
		},
		{
			name:  "json object",
			input: []byte(`{"name": "aish", "version": "0.1"}`),
			want:  KindJSON,
		},
		{
			name:  "json array",
			input: []byte(`[1, 2, 3]`),
			want:  KindJSON,
		},
		{
			name:  "json object with leading whitespace",
			input: []byte("   \n  {\"k\":\"v\"}"),
			want:  KindJSON,
		},
		{
			name:  "ndjson two lines",
			input: []byte(`{"a":1}` + "\n" + `{"b":2}` + "\n"),
			want:  KindNDJSON,
		},
		{
			name:  "ndjson with blank lines tolerated",
			input: []byte(`{"a":1}` + "\n\n" + `{"b":2}` + "\n"),
			want:  KindNDJSON,
		},
		{
			name: "table tab-separated 2x2",
			input: []byte("name\tage\n" +
				"alice\t30\n" +
				"bob\t25\n"),
			want: KindTable,
		},
		{
			name:  "looks like json but unterminated",
			input: []byte(`{"a": 1, "b": 2`),
			want:  KindText,
		},
		{
			name:  "leading text then json is still text",
			input: []byte(`prefix {"a":1}`),
			want:  KindText,
		},
		{
			name:  "single tab-separated line is not a table",
			input: []byte("name\tage\tcity\n"),
			want:  KindText,
		},
		{
			name:  "single column with newlines is text not table",
			input: []byte("alpha\nbeta\ngamma\n"),
			want:  KindText,
		},
		{
			name:  "ndjson with one invalid line falls back to text",
			input: []byte(`{"a":1}` + "\n" + `not-json` + "\n"),
			want:  KindText,
		},
		{
			name:  "not json",
			input: []byte("not json"),
			want:  KindText,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.input)
			if got != tc.want {
				t.Errorf("Detect(%q) = %v, want %v",
					summarize(tc.input), got, tc.want)
			}
		})
	}
}

// TestDetectProbesAt512Bytes verifies the 512-byte probe cap from the
// plan. Beyond 512 bytes the detector must NOT walk the whole buffer —
// behaviour past that point is undefined for performance reasons.
// Construct an input whose first 512 bytes look like JSON and whose tail
// is garbage; Detect should still classify as JSON.
func TestDetectProbesAt512Bytes(t *testing.T) {
	head := []byte(`{"padding":"` + strings.Repeat("x", 480) + `"}`)
	if len(head) <= 512 {
		// pad to ~510 bytes of valid JSON so the test stays meaningful
		head = append(head, bytes.Repeat([]byte{' '}, 512-len(head))...)
	}
	garbage := bytes.Repeat([]byte{0xFF}, 4096)
	input := append(head, garbage...)
	got := Detect(input)
	if got != KindJSON {
		t.Errorf("Detect(512B-json + garbage) = %v, want %v (probe must cap at 512B)",
			got, KindJSON)
	}
}

// TestDetectNeverReturnsUnknown is a total-function guarantee: Detect
// must always return a defined Kind. A bug that returned KindUnknown on
// some path would silently pollute downstream type-switch logic.
func TestDetectNeverReturnsUnknown(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		[]byte("anything"),
		[]byte(`{"k":"v"}`),
		[]byte("a\tb\nc\td\n"),
		bytes.Repeat([]byte{0x00}, 256),
		bytes.Repeat([]byte{0xFF}, 256),
	}
	for _, in := range inputs {
		got := Detect(in)
		if got == KindUnknown {
			t.Errorf("Detect(%q) returned KindUnknown; Detect must be total",
				summarize(in))
		}
	}
}

// TestKindString verifies the stable string representation. The intent
// cache schema serialises Kind by name; a typo here cascades into stored
// data.
func TestKindString(t *testing.T) {
	tests := []struct {
		k    Kind
		want string
	}{
		{KindText, "text"},
		{KindJSON, "json"},
		{KindNDJSON, "ndjson"},
		{KindTable, "table"},
		{KindUnknown, "unknown"},
	}
	for _, tc := range tests {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

// summarize trims an input for use in test failure messages.
func summarize(b []byte) string {
	if len(b) > 64 {
		return string(b[:64]) + "..."
	}
	return string(b)
}
