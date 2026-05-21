package shell

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// TestShouldUsePTY locks the four routing rules at the dispatch
// seam (v0.2-2). The decision is policy, not implementation — keep
// the test pure (no syscalls beyond os.Pipe for the *os.File
// concrete-type check).
//
// A real TTY isn't available under `go test` so this file does NOT
// have a positive case for the term.IsTerminal predicate. The
// integration coverage for the positive path lives in
// shell/internal/exec/pty_test.go (TestRunPTYIsATTY etc.).
func TestShouldUsePTY(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()

	cases := []struct {
		name     string
		pipeline parser.Pipeline
		stdin    io.Reader
		stdout   io.Writer
		want     bool
	}{
		{
			name:     "multi-stage pipeline rejected",
			pipeline: parser.Pipeline{Commands: []parser.Command{{Name: "vim"}, {Name: "cat"}}},
			stdin:    pr,
			stdout:   pw,
			want:     false,
		},
		{
			name:     "empty pipeline rejected",
			pipeline: parser.Pipeline{},
			stdin:    pr,
			stdout:   pw,
			want:     false,
		},
		{
			name:     "non-file stdin rejected",
			pipeline: parser.Pipeline{Commands: []parser.Command{{Name: "vim"}}},
			stdin:    strings.NewReader(""),
			stdout:   pw,
			want:     false,
		},
		{
			name:     "non-file stdout rejected",
			pipeline: parser.Pipeline{Commands: []parser.Command{{Name: "vim"}}},
			stdin:    pr,
			stdout:   &bytes.Buffer{},
			want:     false,
		},
		{
			name:     "non-TTY *os.File rejected (pipe stdin)",
			pipeline: parser.Pipeline{Commands: []parser.Command{{Name: "vim"}}},
			stdin:    pr, // os.Pipe is *os.File but NOT a TTY
			stdout:   pw,
			want:     false,
		},
		{
			name:     "non-interactive command rejected (echo)",
			pipeline: parser.Pipeline{Commands: []parser.Command{{Name: "echo"}}},
			stdin:    pr,
			stdout:   pw,
			want:     false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldUsePTY(c.pipeline, c.stdin, c.stdout)
			if got != c.want {
				t.Errorf("shouldUsePTY = %v, want %v", got, c.want)
			}
		})
	}
}
