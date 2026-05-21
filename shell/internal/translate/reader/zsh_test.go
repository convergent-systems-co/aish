package reader

import (
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

func TestReadZshTagsDialect(t *testing.T) {
	src := "echo hi\n"
	s, err := ReadZsh(src)
	if err != nil {
		t.Fatalf("ReadZsh: %v", err)
	}
	if s.Dialect != translate.DialectZsh {
		t.Errorf("Dialect = %q, want zsh", s.Dialect)
	}
}

func TestReadZshProcessSubUnknown(t *testing.T) {
	// zsh-specific `=( )` process substitution should be flagged.
	src := "diff =(echo a) =(echo b)\n"
	s, err := ReadZsh(src)
	if err != nil {
		t.Fatalf("ReadZsh: %v", err)
	}
	hasUnknown := false
	for _, st := range s.Statements {
		if u, ok := st.(translate.Unknown); ok && strings.Contains(u.Reason, "process substitution") {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Errorf("expected Unknown for zsh =() process substitution; got %#v", s.Statements)
	}
}
