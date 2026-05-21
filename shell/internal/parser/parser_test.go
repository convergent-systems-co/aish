package parser

import (
	"strings"
	"testing"
)

// TestParseSimpleCommand exercises the most basic case: a bare command with
// no args. Gates sub-issue #4 (tokenizer handles quotes, flags, pipes —
// simplest path).
func TestParseSimpleCommand(t *testing.T) {
	p, err := Parse("echo")
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", "echo", err)
	}
	if len(p.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d (%+v)", len(p.Commands), p.Commands)
	}
	if p.Commands[0].Name != "echo" {
		t.Errorf("Command.Name = %q, want %q", p.Commands[0].Name, "echo")
	}
	if len(p.Commands[0].Args) != 0 {
		t.Errorf("expected no args, got %v", p.Commands[0].Args)
	}
}

// TestParseTable runs a table of representative inputs covering the full
// v0.1-1 sub-issue #4 surface: positional args, single quotes, double
// quotes, short flags, long flags, long flags with `=value`, multiple
// commands joined by `|`, leading/trailing whitespace.
func TestParseTable(t *testing.T) {
	type cmd struct {
		name string
		args []string
	}
	tests := []struct {
		name  string
		input string
		want  []cmd
	}{
		{
			name:  "positional args",
			input: "echo hello world",
			want:  []cmd{{"echo", []string{"hello", "world"}}},
		},
		{
			name:  "single quoted arg preserves spaces",
			input: `echo 'hello world'`,
			want:  []cmd{{"echo", []string{"hello world"}}},
		},
		{
			name:  "double quoted arg preserves spaces",
			input: `echo "hello world"`,
			want:  []cmd{{"echo", []string{"hello world"}}},
		},
		{
			name:  "short flag",
			input: "ls -l",
			want:  []cmd{{"ls", []string{"-l"}}},
		},
		{
			name:  "long flag",
			input: "ls --color",
			want:  []cmd{{"ls", []string{"--color"}}},
		},
		{
			name:  "long flag with equals value",
			input: "ls --color=auto",
			want:  []cmd{{"ls", []string{"--color=auto"}}},
		},
		{
			name:  "two-stage pipe",
			input: "echo foo | tr a-z A-Z",
			want: []cmd{
				{"echo", []string{"foo"}},
				{"tr", []string{"a-z", "A-Z"}},
			},
		},
		{
			name:  "three-stage pipe",
			input: "cat file | grep needle | wc -l",
			want: []cmd{
				{"cat", []string{"file"}},
				{"grep", []string{"needle"}},
				{"wc", []string{"-l"}},
			},
		},
		{
			name:  "leading and trailing whitespace",
			input: "   echo hi   ",
			want:  []cmd{{"echo", []string{"hi"}}},
		},
		{
			name:  "double quotes around flag value",
			input: `grep "two words" file.txt`,
			want:  []cmd{{"grep", []string{"two words", "file.txt"}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.input, err)
			}
			if len(got.Commands) != len(tc.want) {
				t.Fatalf("Parse(%q): got %d commands, want %d (%+v)",
					tc.input, len(got.Commands), len(tc.want), got.Commands)
			}
			for i, w := range tc.want {
				gc := got.Commands[i]
				if gc.Name != w.name {
					t.Errorf("Parse(%q) cmd[%d].Name = %q, want %q",
						tc.input, i, gc.Name, w.name)
				}
				if !equalSlices(gc.Args, w.args) {
					t.Errorf("Parse(%q) cmd[%d].Args = %v, want %v",
						tc.input, i, gc.Args, w.args)
				}
			}
		})
	}
}

// TestParseEmptyInput verifies that whitespace-only input returns an empty
// Pipeline with no error — the REPL needs this for a bare Enter keypress.
func TestParseEmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\t", "\n"} {
		p, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) returned error: %v (want nil)", in, err)
		}
		if len(p.Commands) != 0 {
			t.Errorf("Parse(%q) returned %d commands, want 0", in, len(p.Commands))
		}
	}
}

// TestParseUnterminatedQuote is the negative path: an opened quote with no
// matching close MUST surface as an error, not silently swallow the
// remainder of the input. Required by Code.md §1 (no silent swallowing).
func TestParseUnterminatedQuote(t *testing.T) {
	bad := []string{
		`echo 'unterminated`,
		`echo "still open`,
		`grep 'a"b`,
	}
	for _, in := range bad {
		_, err := Parse(in)
		if err == nil {
			t.Errorf("Parse(%q) returned nil error, want non-nil", in)
			continue
		}
		// Error message should mention "quote" so users can debug.
		if !strings.Contains(strings.ToLower(err.Error()), "quote") {
			t.Errorf("Parse(%q) error = %q, want message mentioning 'quote'",
				in, err.Error())
		}
	}
}

// TestParseEmptyPipelineStage rejects inputs like `echo foo |` or `| tr a-z A-Z`
// where the pipeline contains an empty command stage.
func TestParseEmptyPipelineStage(t *testing.T) {
	bad := []string{
		"echo foo |",
		"| tr a-z A-Z",
		"echo | | wc",
	}
	for _, in := range bad {
		_, err := Parse(in)
		if err == nil {
			t.Errorf("Parse(%q) returned nil error, want non-nil", in)
		}
	}
}

// TestParseBackground exercises the v0.3-1 follow-up: a trailing
// unquoted `&` marks the pipeline as a background job. Mid-line `&`
// is a syntax error (POSIX statement-separator form is future work).
// Quoted `&` stays a literal word.
func TestParseBackground(t *testing.T) {
	t.Run("trailing & marks background", func(t *testing.T) {
		p, err := Parse("sleep 30 &")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !p.Background {
			t.Errorf("Background = false, want true")
		}
		if len(p.Commands) != 1 || p.Commands[0].Name != "sleep" {
			t.Fatalf("commands = %+v, want [sleep 30]", p.Commands)
		}
		if !equalSlices(p.Commands[0].Args, []string{"30"}) {
			t.Errorf("Args = %v, want [30] (no trailing &)", p.Commands[0].Args)
		}
	})

	t.Run("trailing & with pipeline", func(t *testing.T) {
		p, err := Parse("yes | head -n5 &")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !p.Background {
			t.Errorf("Background = false, want true")
		}
		if len(p.Commands) != 2 {
			t.Fatalf("got %d commands, want 2", len(p.Commands))
		}
	})

	t.Run("foreground has Background false", func(t *testing.T) {
		p, err := Parse("sleep 30")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if p.Background {
			t.Errorf("Background = true, want false")
		}
	})

	t.Run("mid-line & rejected", func(t *testing.T) {
		_, err := Parse("sleep 30 & echo done")
		if err == nil {
			t.Errorf("Parse: expected error for mid-line `&`")
		}
	})

	t.Run("bare & rejected", func(t *testing.T) {
		_, err := Parse("&")
		if err == nil {
			t.Errorf("Parse: expected error for bare `&`")
		}
	})

	t.Run("quoted & stays a word", func(t *testing.T) {
		p, err := Parse(`echo 'a & b'`)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if p.Background {
			t.Errorf("Background = true, want false (quoted &)")
		}
		if len(p.Commands) != 1 || p.Commands[0].Args[0] != "a & b" {
			t.Errorf("commands = %+v, want [echo 'a & b']", p.Commands)
		}
	})
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
