package translate

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// fakeRunner records every cmdline it sees. Exit code returned from
// each invocation is taken from Exits in order; if Exits is shorter
// than the call count, the runner returns 0 for the overflow.
type fakeRunner struct {
	Calls []string
	Exits []int
}

func (f *fakeRunner) Run(ctx context.Context, cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	idx := len(f.Calls)
	f.Calls = append(f.Calls, cmdline)
	if idx < len(f.Exits) {
		return f.Exits[idx], nil
	}
	return 0, nil
}

func TestRunCommandsInOrder(t *testing.T) {
	script := &Script{
		Dialect: DialectBash,
		Statements: []Statement{
			Command{BaseStmt: BaseStmt{Line: 1}, Name: "echo", Args: []string{"a"}},
			Command{BaseStmt: BaseStmt{Line: 2}, Name: "echo", Args: []string{"b"}},
		},
	}
	r := &fakeRunner{}
	var out, errBuf bytes.Buffer
	if _, err := Run(context.Background(), r, script, RunOptions{Stdout: &out, Stderr: &errBuf}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(r.Calls))
	}
	if r.Calls[0] != "echo a" || r.Calls[1] != "echo b" {
		t.Errorf("calls = %v, want [echo a, echo b]", r.Calls)
	}
}

func TestRunIfThenBranches(t *testing.T) {
	// `if true; then echo yes; else echo no; fi` — using test=Command
	// returning 0 → yes branch runs.
	script := &Script{
		Statements: []Statement{
			Cond{
				BaseStmt: BaseStmt{Line: 1},
				Branches: []CondBranch{
					{
						Test: Command{Name: "true"},
						Body: []Statement{Command{Name: "echo", Args: []string{"yes"}}},
					},
				},
				Else: []Statement{Command{Name: "echo", Args: []string{"no"}}},
			},
		},
	}
	r := &fakeRunner{Exits: []int{0}}
	if _, err := Run(context.Background(), r, script, RunOptions{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Calls) != 2 || r.Calls[1] != "echo yes" {
		t.Errorf("calls = %v, want [true, echo yes]", r.Calls)
	}
}

func TestRunIfElseBranch(t *testing.T) {
	// test returns 1 → else runs.
	script := &Script{
		Statements: []Statement{
			Cond{
				Branches: []CondBranch{
					{
						Test: Command{Name: "false"},
						Body: []Statement{Command{Name: "echo", Args: []string{"yes"}}},
					},
				},
				Else: []Statement{Command{Name: "echo", Args: []string{"no"}}},
			},
		},
	}
	r := &fakeRunner{Exits: []int{1}}
	if _, err := Run(context.Background(), r, script, RunOptions{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Calls) != 2 || r.Calls[1] != "echo no" {
		t.Errorf("calls = %v, want [false, echo no]", r.Calls)
	}
}

func TestRunForLoopIterates(t *testing.T) {
	got := []string{}
	envSet := func(name, value string) {
		got = append(got, name+"="+value)
	}
	script := &Script{
		Statements: []Statement{
			Loop{
				Kind:  LoopFor,
				Var:   "x",
				Words: []string{"a", "b", "c"},
				Body:  []Statement{Command{Name: "echo", Args: []string{"$x"}}},
			},
		},
	}
	r := &fakeRunner{}
	if _, err := Run(context.Background(), r, script, RunOptions{
		Stdout: io.Discard, Stderr: io.Discard,
		EnvSet: envSet,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Calls) != 3 {
		t.Errorf("calls = %d, want 3", len(r.Calls))
	}
	if len(got) != 3 || got[0] != "x=a" || got[2] != "x=c" {
		t.Errorf("env sets = %v, want [x=a x=b x=c]", got)
	}
}

func TestRunUnknownAbortsCleanly(t *testing.T) {
	var errBuf bytes.Buffer
	script := &Script{
		Statements: []Statement{
			Command{Name: "echo", Args: []string{"before"}},
			Unknown{BaseStmt: BaseStmt{Line: 42}, Reason: "heredoc unsupported", Source: "cat <<EOF"},
			Command{Name: "echo", Args: []string{"after"}},
		},
	}
	r := &fakeRunner{}
	code, err := Run(context.Background(), r, script, RunOptions{Stdout: io.Discard, Stderr: &errBuf})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2 on Unknown abort", code)
	}
	// The runner should NOT have executed `echo after`.
	for _, c := range r.Calls {
		if strings.Contains(c, "after") {
			t.Errorf("post-Unknown statement executed: %q", c)
		}
	}
	if !strings.Contains(errBuf.String(), "line 42") {
		t.Errorf("stderr = %q, want substring 'line 42'", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "heredoc") {
		t.Errorf("stderr = %q, want substring 'heredoc'", errBuf.String())
	}
}

func TestRunCommentsAreNoops(t *testing.T) {
	r := &fakeRunner{}
	script := &Script{Statements: []Statement{
		Comment{Text: "# hi"},
		Command{Name: "echo", Args: []string{"ok"}},
	}}
	if _, err := Run(context.Background(), r, script, RunOptions{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Calls) != 1 || r.Calls[0] != "echo ok" {
		t.Errorf("calls = %v, want [echo ok]", r.Calls)
	}
}

func TestRunCaseMatch(t *testing.T) {
	r := &fakeRunner{}
	script := &Script{Statements: []Statement{
		Case{
			Word: "foo.go",
			Arms: []CaseArm{
				{Patterns: []string{"*.sh"}, Body: []Statement{Command{Name: "echo", Args: []string{"sh"}}}},
				{Patterns: []string{"*.go"}, Body: []Statement{Command{Name: "echo", Args: []string{"go"}}}},
				{Patterns: []string{"*"}, Body: []Statement{Command{Name: "echo", Args: []string{"other"}}}},
			},
		},
	}}
	if _, err := Run(context.Background(), r, script, RunOptions{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Calls) != 1 || r.Calls[0] != "echo go" {
		t.Errorf("calls = %v, want [echo go]", r.Calls)
	}
}
