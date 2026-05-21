package parser

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeExecutor is the parser-package CmdSubExecutor stub. RunForCapture
// returns canned output keyed by the trimmed command string so tests
// don't need a real shell.
type fakeExecutor struct {
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (f *fakeExecutor) RunForCapture(cmd string, _ io.Writer) (string, error) {
	f.calls = append(f.calls, cmd)
	if err, ok := f.errs[cmd]; ok {
		return "", err
	}
	if out, ok := f.outputs[cmd]; ok {
		return out, nil
	}
	return "", fmt.Errorf("unexpected sub-command in test: %q", cmd)
}

func TestExpandBrace_Comma(t *testing.T) {
	got := expandBrace("{a,b,c}")
	want := []string{"a", "b", "c"}
	if !equalSlices(got, want) {
		t.Errorf("expandBrace({a,b,c}) = %v, want %v", got, want)
	}
}

func TestExpandBrace_PrefixSuffix(t *testing.T) {
	got := expandBrace("pre{a,b}post")
	want := []string{"preapost", "prebpost"}
	if !equalSlices(got, want) {
		t.Errorf("expandBrace(pre{a,b}post) = %v, want %v", got, want)
	}
}

func TestExpandBrace_NumericRange(t *testing.T) {
	got := expandBrace("{1..5}")
	want := []string{"1", "2", "3", "4", "5"}
	if !equalSlices(got, want) {
		t.Errorf("expandBrace({1..5}) = %v, want %v", got, want)
	}
}

func TestExpandBrace_DescendingRange(t *testing.T) {
	got := expandBrace("{3..1}")
	want := []string{"3", "2", "1"}
	if !equalSlices(got, want) {
		t.Errorf("expandBrace({3..1}) = %v, want %v", got, want)
	}
}

func TestExpandBrace_RangeWithPrefix(t *testing.T) {
	got := expandBrace("x{1..3}y")
	want := []string{"x1y", "x2y", "x3y"}
	if !equalSlices(got, want) {
		t.Errorf("expandBrace(x{1..3}y) = %v, want %v", got, want)
	}
}

func TestExpandBrace_NoBrace_Identity(t *testing.T) {
	got := expandBrace("plain")
	if !equalSlices(got, []string{"plain"}) {
		t.Errorf("expandBrace(plain) = %v, want [plain]", got)
	}
}

func TestExpandBrace_SingleItemTreatedLiteral(t *testing.T) {
	// `{onlyone}` is NOT a brace expansion in bash either — no comma,
	// no range -> literal.
	got := expandBrace("{onlyone}")
	if !equalSlices(got, []string{"{onlyone}"}) {
		t.Errorf("expandBrace({onlyone}) = %v, want [{onlyone}]", got)
	}
}

func TestExpandBrace_CrossProduct(t *testing.T) {
	got := expandBrace("{a,b}{c,d}")
	want := []string{"ac", "ad", "bc", "bd"}
	if !equalSlices(got, want) {
		t.Errorf("expandBrace({a,b}{c,d}) = %v, want %v", got, want)
	}
}

func TestExpandGlob_NoMetaIsIdentity(t *testing.T) {
	dir := t.TempDir()
	got := expandGlob("plain.txt", dir)
	if !equalSlices(got, []string{"plain.txt"}) {
		t.Errorf("expandGlob(plain.txt) = %v, want identity", got)
	}
}

func TestExpandGlob_StarMatchesRealFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed file %s: %v", name, err)
		}
	}
	got := expandGlob("*.go", dir)
	sort.Strings(got)
	want := []string{"a.go", "b.go"}
	if !equalSlices(got, want) {
		t.Errorf("expandGlob(*.go) = %v, want %v", got, want)
	}
}

func TestExpandGlob_NoMatchPreservesLiteral(t *testing.T) {
	dir := t.TempDir()
	got := expandGlob("*.go", dir)
	if !equalSlices(got, []string{"*.go"}) {
		t.Errorf("expandGlob(*.go) on empty dir = %v, want [*.go]", got)
	}
}

func TestExpandLine_CmdSub_Simple(t *testing.T) {
	exec := &fakeExecutor{outputs: map[string]string{"date": "Tue May 21 12:00:00 UTC 2026\n"}}
	out, err := ExpandLine("echo $(date)", ExpandContext{CmdSub: exec})
	if err != nil {
		t.Fatalf("ExpandLine: %v", err)
	}
	want := "echo Tue May 21 12:00:00 UTC 2026"
	if out != want {
		t.Errorf("ExpandLine = %q, want %q", out, want)
	}
	if len(exec.calls) != 1 || exec.calls[0] != "date" {
		t.Errorf("expected one call to `date`, got %v", exec.calls)
	}
}

func TestExpandLine_CmdSub_Nested(t *testing.T) {
	exec := &fakeExecutor{outputs: map[string]string{
		"echo inner":        "inner-result",
		"echo inner-result": "inner-result", // outer call
	}}
	out, err := ExpandLine("echo $(echo $(echo inner))", ExpandContext{CmdSub: exec})
	if err != nil {
		t.Fatalf("ExpandLine: %v", err)
	}
	want := "echo inner-result"
	if out != want {
		t.Errorf("ExpandLine = %q, want %q", out, want)
	}
}

func TestExpandLine_CmdSub_LeavesSingleQuotesAlone(t *testing.T) {
	exec := &fakeExecutor{outputs: map[string]string{}}
	out, err := ExpandLine(`echo '$(date)'`, ExpandContext{CmdSub: exec})
	if err != nil {
		t.Fatalf("ExpandLine: %v", err)
	}
	if out != `echo '$(date)'` {
		t.Errorf("single-quoted $(date) should not expand: got %q", out)
	}
	if len(exec.calls) != 0 {
		t.Errorf("no executor calls expected, got %v", exec.calls)
	}
}

func TestExpandLine_CmdSub_NoSubstWithoutDollarParen(t *testing.T) {
	out, err := ExpandLine("echo hello world", ExpandContext{})
	if err != nil {
		t.Fatalf("ExpandLine: %v", err)
	}
	if out != "echo hello world" {
		t.Errorf("ExpandLine = %q, want unchanged", out)
	}
}

func TestExpandLine_CmdSub_UnterminatedIsError(t *testing.T) {
	exec := &fakeExecutor{}
	_, err := ExpandLine("echo $(date", ExpandContext{CmdSub: exec})
	if err == nil {
		t.Fatal("expected error for unterminated $(, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("error message = %q, want it to mention unterminated", err.Error())
	}
}

func TestExpandLine_CmdSub_ExecutorErrorPropagates(t *testing.T) {
	exec := &fakeExecutor{errs: map[string]error{"bad": errors.New("kaboom")}}
	_, err := ExpandLine("echo $(bad)", ExpandContext{CmdSub: exec})
	if err == nil {
		t.Fatal("expected propagated error from executor, got nil")
	}
}

func TestExpandPipeline_BraceAndGlob(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.go", "beta.go"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	p := Pipeline{Commands: []Command{
		{Name: "echo", Args: []string{"{a,b}", "*.go"}},
	}}
	out, err := ExpandPipeline(p, ExpandContext{Cwd: dir})
	if err != nil {
		t.Fatalf("ExpandPipeline: %v", err)
	}
	if len(out.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(out.Commands))
	}
	args := out.Commands[0].Args
	sort.Strings(args)
	want := []string{"a", "alpha.go", "b", "beta.go"}
	sort.Strings(want)
	if !equalSlices(args, want) {
		t.Errorf("expanded args = %v, want %v", args, want)
	}
}
