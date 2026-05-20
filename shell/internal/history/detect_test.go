package history

import (
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

func parse(t *testing.T, line string) parser.Pipeline {
	t.Helper()
	pl, err := parser.Parse(line)
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	return pl
}

func TestIsDestructive(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"rm /tmp/x", true},
		{"rm -rf ./dist", true},
		{"rmdir /tmp/d", true},
		{"unlink /tmp/x", true},
		{"shred /tmp/secret", true},
		{"truncate -s 0 /tmp/x", true},
		{"dd of=/tmp/x if=/dev/zero", true},
		{"ls", false},
		{"cat /tmp/x", false},
		{"echo hi", false},
		{"grep foo /tmp/x", false},
		// piped destructive in middle should still be flagged
		{"ls | rm", true},
		// arg-named "rm" must NOT match — only argv[0]
		{"echo rm", false},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got := IsDestructive(parse(t, tc.line))
			if got != tc.want {
				t.Errorf("IsDestructive(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestTargetPaths(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"rm /tmp/x", []string{"/tmp/x"}},
		{"rm -rf /tmp/x /tmp/y", []string{"/tmp/x", "/tmp/y"}},
		{"rm -- /tmp/-flag", []string{"/tmp/-flag"}},
		{"rmdir /tmp/d", []string{"/tmp/d"}},
		{"truncate -s 0 /tmp/x", []string{"/tmp/x"}},
		{"shred -uz /tmp/secret", []string{"/tmp/secret"}},
		{"dd of=/tmp/x if=/dev/zero", []string{"/tmp/x"}},
		{"ls", nil},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got := TargetPaths(parse(t, tc.line))
			if len(got) != len(tc.want) {
				t.Fatalf("len(TargetPaths) = %d (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for i, p := range tc.want {
				if got[i] != p {
					t.Errorf("[%d] got %q want %q", i, got[i], p)
				}
			}
		})
	}
}
