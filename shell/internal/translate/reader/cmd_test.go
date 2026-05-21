package reader

import (
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

func TestReadCmdHello(t *testing.T) {
	src := readWinTestdata(t, "hello.bat")
	s, err := ReadCmd(src)
	if err != nil {
		t.Fatalf("ReadCmd: %v", err)
	}
	if s.Dialect != translate.DialectCmd {
		t.Errorf("Dialect = %q, want cmd", s.Dialect)
	}
	// Expect: [echo off command, REM comment, echo command].
	if len(s.Statements) != 3 {
		t.Fatalf("want 3 statements, got %d (%+v)", len(s.Statements), s.Statements)
	}
	// `@echo off` parses as command "echo" with arg "off".
	if c, ok := s.Statements[0].(translate.Command); !ok || c.Name != "echo" {
		t.Errorf("Statements[0] = %v, want `echo off` Command", s.Statements[0])
	}
	if _, ok := s.Statements[1].(translate.Comment); !ok {
		t.Errorf("Statements[1] = %T, want Comment (REM)", s.Statements[1])
	}
	if c, ok := s.Statements[2].(translate.Command); !ok || c.Name != "echo" {
		t.Errorf("Statements[2] = %v, want echo Command", s.Statements[2])
	}
}

func TestReadCmdEnvSetup(t *testing.T) {
	src := readWinTestdata(t, "env_setup.cmd")
	s, err := ReadCmd(src)
	if err != nil {
		t.Fatalf("ReadCmd: %v", err)
	}
	// Expect at least one Assign, one Cond, and the goto :EOF Command.
	var hasAssign, hasCond, hasGoto bool
	for _, st := range s.Statements {
		switch v := st.(type) {
		case translate.Assign:
			hasAssign = true
			if v.Name != "TARGET" && v.Name != "PATH" {
				t.Errorf("unexpected Assign.Name = %q", v.Name)
			}
		case translate.Cond:
			hasCond = true
			if len(v.Branches) != 1 {
				t.Errorf("Cond.Branches = %d, want 1", len(v.Branches))
			}
			if len(v.Else) == 0 {
				t.Errorf("Cond.Else missing — expected else branch")
			}
		case translate.Command:
			if v.Name == "goto" {
				hasGoto = true
			}
		}
	}
	if !hasAssign {
		t.Errorf("expected Assign (set NAME=VALUE)")
	}
	if !hasCond {
		t.Errorf("expected Cond (if/else)")
	}
	if !hasGoto {
		t.Errorf("expected goto :EOF Command")
	}
}

func TestReadCmdErrorlevel(t *testing.T) {
	src := readWinTestdata(t, "errorlevel.bat")
	s, err := ReadCmd(src)
	if err != nil {
		t.Fatalf("ReadCmd: %v", err)
	}
	// The if/else should have a body containing an echo Command and an
	// else with an echo Command.
	var cond *translate.Cond
	for _, st := range s.Statements {
		if c, ok := st.(translate.Cond); ok {
			cond = &c
			break
		}
	}
	if cond == nil {
		t.Fatalf("expected Cond statement; got %+v", s.Statements)
	}
	if len(cond.Branches[0].Body) != 1 {
		t.Errorf("then body = %d, want 1", len(cond.Branches[0].Body))
	}
	if len(cond.Else) != 1 {
		t.Errorf("else body = %d, want 1", len(cond.Else))
	}
}

func TestReadCmdPipelineAndUnknown(t *testing.T) {
	src := `dir | findstr "log"
for /F %%i in (...) do echo %%i
call setup.bat
:label
`
	s, err := ReadCmd(src)
	if err != nil {
		t.Fatalf("ReadCmd: %v", err)
	}
	// Expect a Pipe + three Unknowns.
	var hasPipe bool
	unknowns := 0
	for _, st := range s.Statements {
		switch st.(type) {
		case translate.Pipe:
			hasPipe = true
		case translate.Unknown:
			unknowns++
		}
	}
	if !hasPipe {
		t.Errorf("expected a Pipe statement; got %+v", s.Statements)
	}
	if unknowns < 3 {
		t.Errorf("expected >=3 Unknown statements (for / call / label); got %d", unknowns)
	}
}

func TestReadCmdRemComment(t *testing.T) {
	// Verify both REM-with-space and ::-comment shapes parse as Comment.
	src := "REM hello\n:: another\n"
	s, err := ReadCmd(src)
	if err != nil {
		t.Fatalf("ReadCmd: %v", err)
	}
	if len(s.Statements) != 2 {
		t.Fatalf("want 2 comments, got %d", len(s.Statements))
	}
	for i, st := range s.Statements {
		c, ok := st.(translate.Comment)
		if !ok {
			t.Errorf("Statements[%d] = %T, want Comment", i, st)
		} else if !strings.HasPrefix(c.Text, "REM") && !strings.HasPrefix(c.Text, "::") {
			t.Errorf("Comment.Text = %q, want REM/:: prefix", c.Text)
		}
	}
}
