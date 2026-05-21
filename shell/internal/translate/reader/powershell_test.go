package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

func readWinTestdata(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("win_testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestReadPowerShellHello(t *testing.T) {
	src := readWinTestdata(t, "hello.ps1")
	s, err := ReadPowerShell(src)
	if err != nil {
		t.Fatalf("ReadPowerShell: %v", err)
	}
	if s.Dialect != translate.DialectPowerShell {
		t.Errorf("Dialect = %q, want powershell", s.Dialect)
	}
	if len(s.Statements) != 2 {
		t.Fatalf("want 2 statements (comment + Write-Host), got %d", len(s.Statements))
	}
	if _, ok := s.Statements[0].(translate.Comment); !ok {
		t.Errorf("Statements[0] = %T, want Comment", s.Statements[0])
	}
	cmd, ok := s.Statements[1].(translate.Command)
	if !ok {
		t.Fatalf("Statements[1] = %T, want Command", s.Statements[1])
	}
	if cmd.Name != "Write-Host" {
		t.Errorf("cmd.Name = %q, want Write-Host", cmd.Name)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "hello world" {
		t.Errorf("cmd.Args = %v, want [hello world]", cmd.Args)
	}
}

func TestReadPowerShellServicesRestart(t *testing.T) {
	src := readWinTestdata(t, "services_restart.ps1")
	s, err := ReadPowerShell(src)
	if err != nil {
		t.Fatalf("ReadPowerShell: %v", err)
	}
	hasAssign := false
	hasCond := false
	for _, st := range s.Statements {
		switch v := st.(type) {
		case translate.Assign:
			hasAssign = true
			if v.Name != "svc" || v.Value != "Spooler" {
				t.Errorf("Assign = %+v, want svc=Spooler", v)
			}
		case translate.Cond:
			hasCond = true
			if len(v.Branches) != 1 {
				t.Errorf("Cond branches = %d, want 1", len(v.Branches))
			}
			if len(v.Else) == 0 {
				t.Errorf("Cond.Else missing — expected else branch")
			}
			// Inside the then-branch we expect two commands: Stop-Service then Start-Service.
			body := v.Branches[0].Body
			if len(body) < 2 {
				t.Errorf("Cond.then body = %d statements, want >=2", len(body))
			} else {
				if c, ok := body[0].(translate.Command); !ok || c.Name != "Stop-Service" {
					t.Errorf("then[0] = %v, want Stop-Service command", body[0])
				}
				if c, ok := body[1].(translate.Command); !ok || c.Name != "Start-Service" {
					t.Errorf("then[1] = %v, want Start-Service command", body[1])
				}
			}
		}
	}
	if !hasAssign {
		t.Errorf("expected an Assign statement; statements=%+v", s.Statements)
	}
	if !hasCond {
		t.Errorf("expected a Cond statement; statements=%+v", s.Statements)
	}
}

func TestReadPowerShellPipeline(t *testing.T) {
	src := readWinTestdata(t, "install_pipeline.ps1")
	s, err := ReadPowerShell(src)
	if err != nil {
		t.Fatalf("ReadPowerShell: %v", err)
	}
	// First non-comment statement should be a Pipe with 3 stages.
	var pipe *translate.Pipe
	for _, st := range s.Statements {
		if p, ok := st.(translate.Pipe); ok {
			pipe = &p
			break
		}
	}
	if pipe == nil {
		t.Fatalf("expected a Pipe; got statements=%+v", s.Statements)
	}
	if len(pipe.Stages) != 3 {
		t.Errorf("Pipe.Stages = %d, want 3", len(pipe.Stages))
	}
	wantNames := []string{"Get-Process", "Where-Object", "Out-Host"}
	for i, name := range wantNames {
		if i >= len(pipe.Stages) {
			break
		}
		if pipe.Stages[i].Name != name {
			t.Errorf("Stages[%d].Name = %q, want %q", i, pipe.Stages[i].Name, name)
		}
	}
	// Block comment should be preserved.
	hasComment := false
	for _, st := range s.Statements {
		if c, ok := st.(translate.Comment); ok && strings.Contains(c.Text, "<#") {
			hasComment = true
			break
		}
	}
	if !hasComment {
		t.Errorf("expected the <# … #> block comment to be preserved")
	}
}

func TestReadPowerShellFunctionDef(t *testing.T) {
	src := `function Greet {
    Write-Host 'hello'
}
Greet
`
	s, err := ReadPowerShell(src)
	if err != nil {
		t.Fatalf("ReadPowerShell: %v", err)
	}
	if len(s.Statements) < 2 {
		t.Fatalf("want >=2 statements, got %d", len(s.Statements))
	}
	fn, ok := s.Statements[0].(translate.FuncDef)
	if !ok {
		t.Fatalf("Statements[0] = %T, want FuncDef", s.Statements[0])
	}
	if fn.Name != "Greet" {
		t.Errorf("fn.Name = %q, want Greet", fn.Name)
	}
	if len(fn.Body) != 1 {
		t.Errorf("fn.Body = %d, want 1", len(fn.Body))
	}
}

func TestReadPowerShellUnknownConstructs(t *testing.T) {
	// elseif chains, += compound assignments, and try/catch are all
	// out of MVP scope — expect Unknown nodes for each.
	src := `if ($x) { Write-Host 'one' } elseif ($y) { Write-Host 'two' }
$x += 1
`
	s, err := ReadPowerShell(src)
	if err != nil {
		t.Fatalf("ReadPowerShell: %v", err)
	}
	unknowns := 0
	for _, st := range s.Statements {
		if _, ok := st.(translate.Unknown); ok {
			unknowns++
		}
	}
	if unknowns < 1 {
		t.Errorf("expected >=1 Unknown nodes; statements=%+v", s.Statements)
	}
}
