package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	// testdata lives one directory above this package (shared with
	// the parent translate package).
	path := filepath.Join("..", "testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestReadBashHello(t *testing.T) {
	src := readTestdata(t, "hello.sh")
	s, err := ReadBash(src)
	if err != nil {
		t.Fatalf("ReadBash: %v", err)
	}
	if s.Dialect != translate.DialectBash {
		t.Errorf("Dialect = %q, want bash", s.Dialect)
	}
	if len(s.Statements) < 3 {
		t.Fatalf("expected at least 3 statements (shebang comment, comment, echo), got %d", len(s.Statements))
	}
	// shebang as Comment
	if _, ok := s.Statements[0].(translate.Comment); !ok {
		t.Errorf("Statements[0] = %T, want Comment", s.Statements[0])
	}
	// echo command
	last := s.Statements[len(s.Statements)-1]
	cmd, ok := last.(translate.Command)
	if !ok {
		t.Fatalf("last statement = %T, want Command", last)
	}
	if cmd.Name != "echo" {
		t.Errorf("cmd.Name = %q, want echo", cmd.Name)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "hello" || cmd.Args[1] != "world" {
		t.Errorf("cmd.Args = %v, want [hello world]", cmd.Args)
	}
}

func TestReadBashIfFor(t *testing.T) {
	src := readTestdata(t, "if_for.sh")
	s, err := ReadBash(src)
	if err != nil {
		t.Fatalf("ReadBash: %v", err)
	}
	hasCond := false
	hasFor := false
	hasAssign := false
	for _, st := range s.Statements {
		switch v := st.(type) {
		case translate.Cond:
			hasCond = true
			if len(v.Branches) == 0 || len(v.Else) == 0 {
				t.Errorf("Cond branches/else missing: branches=%d else=%d", len(v.Branches), len(v.Else))
			}
		case translate.Loop:
			if v.Kind == translate.LoopFor {
				hasFor = true
				if v.Var != "x" {
					t.Errorf("for var = %q, want x", v.Var)
				}
				want := []string{"a", "b", "c"}
				if !equalStrings(v.Words, want) {
					t.Errorf("for words = %v, want %v", v.Words, want)
				}
			}
		case translate.Assign:
			if v.Name == "NAME" {
				hasAssign = true
				if v.Value != "world" {
					t.Errorf("NAME value = %q, want world", v.Value)
				}
			}
		}
	}
	if !hasCond {
		t.Errorf("missing if-cond")
	}
	if !hasFor {
		t.Errorf("missing for-loop")
	}
	if !hasAssign {
		t.Errorf("missing NAME=world assignment")
	}
}

func TestReadBashPipeline(t *testing.T) {
	src := readTestdata(t, "pipeline.sh")
	s, err := ReadBash(src)
	if err != nil {
		t.Fatalf("ReadBash: %v", err)
	}
	var pipe translate.Pipe
	found := false
	for _, st := range s.Statements {
		if p, ok := st.(translate.Pipe); ok {
			pipe = p
			found = true
		}
	}
	if !found {
		t.Fatalf("no Pipe statement parsed; got %#v", s.Statements)
	}
	if len(pipe.Stages) != 2 {
		t.Fatalf("pipe stages = %d, want 2", len(pipe.Stages))
	}
	if pipe.Stages[0].Name != "echo" {
		t.Errorf("stage[0].Name = %q, want echo", pipe.Stages[0].Name)
	}
	if pipe.Stages[1].Name != "tr" {
		t.Errorf("stage[1].Name = %q, want tr", pipe.Stages[1].Name)
	}
	// Final stage has a `> out.txt` redirect.
	if len(pipe.Stages[1].Redirects) != 1 {
		t.Fatalf("redirect count = %d, want 1", len(pipe.Stages[1].Redirects))
	}
	r := pipe.Stages[1].Redirects[0]
	if r.Op != translate.RedirectOut || r.Target != "out.txt" {
		t.Errorf("redirect = (%v, %q), want (RedirectOut, out.txt)", r.Op, r.Target)
	}
}

func TestReadBashUnknownHeredoc(t *testing.T) {
	src := readTestdata(t, "unknown.sh")
	s, err := ReadBash(src)
	if err != nil {
		t.Fatalf("ReadBash: %v", err)
	}
	// Find the Unknown classifying the heredoc.
	found := false
	for _, st := range s.Statements {
		if u, ok := st.(translate.Unknown); ok {
			if !strings.Contains(u.Reason, "heredoc") {
				t.Errorf("Unknown.Reason = %q, want substring 'heredoc'", u.Reason)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected at least one Unknown for the heredoc; got %#v", s.Statements)
	}
}

func TestReadBashEmpty(t *testing.T) {
	s, err := ReadBash("")
	if err != nil {
		t.Fatalf("ReadBash(\"\"): %v", err)
	}
	if len(s.Statements) != 0 {
		t.Errorf("empty script: got %d statements, want 0", len(s.Statements))
	}
}

func TestReadBashCommentsPreserved(t *testing.T) {
	src := "# header\necho ok\n# trailing\n"
	s, err := ReadBash(src)
	if err != nil {
		t.Fatalf("ReadBash: %v", err)
	}
	nComments := 0
	for _, st := range s.Statements {
		if _, ok := st.(translate.Comment); ok {
			nComments++
		}
	}
	if nComments != 2 {
		t.Errorf("comments preserved = %d, want 2", nComments)
	}
}

func TestReadBashUnknownAndAndOr(t *testing.T) {
	src := "true && echo yes\n"
	s, err := ReadBash(src)
	if err != nil {
		t.Fatalf("ReadBash: %v", err)
	}
	hasUnknown := false
	for _, st := range s.Statements {
		if u, ok := st.(translate.Unknown); ok {
			if !strings.Contains(u.Reason, "short-circuit") {
				t.Errorf("Unknown.Reason = %q, want substring 'short-circuit'", u.Reason)
			}
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Errorf("expected Unknown for &&")
	}
}

func equalStrings(a, b []string) bool {
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
