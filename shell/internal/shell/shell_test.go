package shell

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

func TestNew(t *testing.T) {
	if New() == nil {
		t.Fatal("New() returned nil")
	}
}

func TestRunSeed(t *testing.T) {
	// Seed-commit-level contract: Run accepts stdin/stdout/stderr and returns
	// without error when stdin is empty. The interactive REPL behaviour is
	// owned by the v0.1-1 coder sub-tasks; this test exists so the seed has
	// at least one passing test per Code.md §3.
	s := New()
	var out, errBuf bytes.Buffer
	if err := s.Run(strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("Run() returned error on empty stdin: %v", err)
	}
}

// TestReadLine_BasicCases pins the byte-by-byte line reader's contract:
// it reads UP TO and INCLUDING the next '\n', leaving subsequent bytes
// on the underlying stream untouched. This is the key property that
// keeps external children (cat, head, read) able to see their input.
// See issue #167.
func TestReadLine_BasicCases(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantLine  string
		wantRest  string
		wantErrIs error // nil means no error
	}{
		{
			name:     "single line with newline",
			input:    "hello\n",
			wantLine: "hello\n",
			wantRest: "",
		},
		{
			name:     "first line of multi-line input",
			input:    "first\nsecond\nthird\n",
			wantLine: "first\n",
			wantRest: "second\nthird\n",
		},
		{
			name:      "no trailing newline at EOF",
			input:     "no-newline",
			wantLine:  "no-newline",
			wantRest:  "",
			wantErrIs: io.EOF,
		},
		{
			name:     "empty line returns just newline",
			input:    "\nnext\n",
			wantLine: "\n",
			wantRest: "next\n",
		},
		{
			name:      "empty input returns empty string and EOF",
			input:     "",
			wantLine:  "",
			wantRest:  "",
			wantErrIs: io.EOF,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			got, err := readLine(r)
			if got != tc.wantLine {
				t.Errorf("readLine returned %q, want %q", got, tc.wantLine)
			}
			if tc.wantErrIs != nil {
				if !errors.Is(err, tc.wantErrIs) {
					t.Errorf("err = %v, want errors.Is(%v)", err, tc.wantErrIs)
				}
			} else if err != nil {
				t.Errorf("unexpected err = %v", err)
			}
			// What remains on the underlying reader is exactly tc.wantRest.
			rest, _ := io.ReadAll(r)
			if string(rest) != tc.wantRest {
				t.Errorf("remaining stream = %q, want %q", string(rest), tc.wantRest)
			}
		})
	}
}

// TestHomeDir covers the POSIX-or-Windows home-dir resolution.
// HOME wins when both are set (POSIX takes precedence on every host).
// USERPROFILE is the Windows fallback.
func TestHomeDir(t *testing.T) {
	cases := []struct {
		name        string
		home        string
		userprofile string
		want        string
	}{
		{name: "HOME set wins", home: "/home/u", userprofile: "C:\\Users\\u", want: "/home/u"},
		{name: "USERPROFILE fallback (Windows)", home: "", userprofile: "C:\\Users\\u", want: "C:\\Users\\u"},
		{name: "neither set returns empty", home: "", userprofile: "", want: ""},
		{name: "empty HOME falls through to USERPROFILE", home: "", userprofile: "D:\\u", want: "D:\\u"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := env.New()
			if tc.home != "" {
				_ = e.Set("HOME", tc.home)
			}
			if tc.userprofile != "" {
				_ = e.Set("USERPROFILE", tc.userprofile)
			}
			if got := homeDir(e); got != tc.want {
				t.Errorf("homeDir = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadLine_LeavesBytesForChildren is the explicit regression
// seatbelt for issue #167: after readLine returns the first line, the
// reader still has the subsequent bytes available — exactly the
// property that lets `cat` read what the user typed after invoking it.
func TestReadLine_LeavesBytesForChildren(t *testing.T) {
	r := strings.NewReader("cat\nfirst line\nsecond line\n")
	got, err := readLine(r)
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if got != "cat\n" {
		t.Fatalf("readLine = %q, want %q", got, "cat\n")
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := "first line\nsecond line\n"
	if string(rest) != want {
		t.Errorf("after readLine, underlying = %q, want %q", string(rest), want)
	}
}
