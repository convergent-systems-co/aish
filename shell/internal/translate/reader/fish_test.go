package reader

import (
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

func TestReadFishHello(t *testing.T) {
	src := readTestdata(t, "hello.fish")
	s, err := ReadFish(src)
	if err != nil {
		t.Fatalf("ReadFish: %v", err)
	}
	if s.Dialect != translate.DialectFish {
		t.Errorf("Dialect = %q, want fish", s.Dialect)
	}
	hasAssign := false
	hasCond := false
	for _, st := range s.Statements {
		switch v := st.(type) {
		case translate.Assign:
			if v.Name == "name" && v.Value == "world" {
				hasAssign = true
			}
		case translate.Cond:
			hasCond = true
		}
	}
	if !hasAssign {
		t.Errorf("missing `set -l name world` -> Assign{name, world}; got %#v", s.Statements)
	}
	if !hasCond {
		t.Errorf("missing fish `if … end` -> Cond")
	}
}

func TestReadFishFor(t *testing.T) {
	src := "for x in a b c\n  echo $x\nend\n"
	s, err := ReadFish(src)
	if err != nil {
		t.Fatalf("ReadFish: %v", err)
	}
	if len(s.Statements) == 0 {
		t.Fatalf("no statements parsed")
	}
	l, ok := s.Statements[0].(translate.Loop)
	if !ok {
		t.Fatalf("Statements[0] = %T, want Loop", s.Statements[0])
	}
	if l.Kind != translate.LoopFor || l.Var != "x" {
		t.Errorf("loop var = %q (kind %v), want x (LoopFor)", l.Var, l.Kind)
	}
	if !equalStrings(l.Words, []string{"a", "b", "c"}) {
		t.Errorf("loop words = %v, want [a b c]", l.Words)
	}
}

func TestReadFishSetExport(t *testing.T) {
	s, err := ReadFish("set -x PATH /usr/local/bin\n")
	if err != nil {
		t.Fatalf("ReadFish: %v", err)
	}
	if len(s.Statements) != 1 {
		t.Fatalf("statements = %d, want 1", len(s.Statements))
	}
	a, ok := s.Statements[0].(translate.Assign)
	if !ok {
		t.Fatalf("Statements[0] = %T, want Assign", s.Statements[0])
	}
	if !a.Exported {
		t.Errorf("Exported = false, want true (from -x)")
	}
}
