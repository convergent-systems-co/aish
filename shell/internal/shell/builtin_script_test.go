package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes src to a fresh tempfile and returns its path.
func writeScript(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestExplainBuiltinBaselineDeterministic(t *testing.T) {
	path := writeScript(t, "hi.sh", "#!/usr/bin/env bash\necho hi\n")
	s := New()
	defer s.Close()
	var out1, errBuf bytes.Buffer
	if code := s.explainScriptBuiltin([]string{path}, &out1, &errBuf); code != 0 {
		t.Fatalf("explain exited %d; stderr=%s", code, errBuf.String())
	}
	var out2 bytes.Buffer
	if code := s.explainScriptBuiltin([]string{path}, &out2, &errBuf); code != 0 {
		t.Fatalf("explain (2nd) exited %d", code)
	}
	if out1.String() != out2.String() {
		t.Errorf("explain not deterministic across calls")
	}
	if !strings.Contains(out1.String(), "echo hi") {
		t.Errorf("explain missing 'echo hi': %s", out1.String())
	}
}

func TestMigrateBuiltinPreservesComments(t *testing.T) {
	src := "#!/usr/bin/env bash\n# greeting\necho hi\n"
	path := writeScript(t, "g.sh", src)
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	if code := s.migrateScriptBuiltin([]string{path}, &out, &errBuf); code != 0 {
		t.Fatalf("migrate exited %d; stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "# greeting") {
		t.Errorf("comment lost: %s", out.String())
	}
	if !strings.Contains(out.String(), "echo hi") {
		t.Errorf("command lost: %s", out.String())
	}
}

func TestRunBuiltinFreshEnvPerInvocation(t *testing.T) {
	// A script that sets a variable must NOT leak that variable
	// into the parent shell's env across `aish run` invocations.
	path := writeScript(t, "set.sh", "FOO=fromscript\n")
	s := New()
	defer s.Close()
	if _, ok := s.env.Get("FOO"); ok {
		t.Fatalf("precondition: FOO already set in parent env")
	}
	var out, errBuf bytes.Buffer
	if code := s.runScriptBuiltin([]string{path}, nil, &out, &errBuf); code != 0 {
		t.Fatalf("run exited %d; stderr=%s", code, errBuf.String())
	}
	if v, ok := s.env.Get("FOO"); ok {
		t.Errorf("FOO leaked to parent env: %q", v)
	}
}

func TestRunBuiltinUnknownAborts(t *testing.T) {
	// A heredoc is out of MVP scope. Run must abort with exit 2 and
	// not execute statements that follow the Unknown.
	path := writeScript(t, "u.sh", "echo before\ncat <<EOF\nbody\nEOF\necho after\n")
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	code := s.runScriptBuiltin([]string{path}, nil, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "cannot translate") {
		t.Errorf("stderr missing 'cannot translate': %s", errBuf.String())
	}
	if strings.Contains(out.String(), "after") {
		t.Errorf("stdout contains output from post-Unknown statement: %s", out.String())
	}
}

func TestRunBuiltinMissingFile(t *testing.T) {
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	code := s.runScriptBuiltin([]string{"/nonexistent/aish-test"}, nil, &out, &errBuf)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 on missing file", code)
	}
}

func TestExplainBuiltinWithLLMWithoutKey(t *testing.T) {
	// --with-llm without an API key must emit baseline + a warning
	// to stderr; exit code is still 0.
	path := writeScript(t, "x.sh", "echo hi\n")
	s := New()
	defer s.Close()
	// Force no key.
	_ = s.env.Set("ANTHROPIC_API_KEY", "")
	var out, errBuf bytes.Buffer
	if code := s.explainScriptBuiltin([]string{"--with-llm", path}, &out, &errBuf); code != 0 {
		t.Fatalf("explain exited %d", code)
	}
	if !strings.Contains(errBuf.String(), "no API key") {
		t.Errorf("stderr missing 'no API key': %s", errBuf.String())
	}
	if !strings.Contains(out.String(), "echo hi") {
		t.Errorf("baseline missing: %s", out.String())
	}
	if strings.Contains(out.String(), "Summary") {
		t.Errorf("Summary written without API key: %s", out.String())
	}
}

func TestExplainBuiltinUsageError(t *testing.T) {
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	code := s.explainScriptBuiltin(nil, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
}

func TestMigrateBuiltinUsageError(t *testing.T) {
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	code := s.migrateScriptBuiltin(nil, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
}

func TestRunBuiltinUsageError(t *testing.T) {
	s := New()
	defer s.Close()
	var out, errBuf bytes.Buffer
	code := s.runScriptBuiltin(nil, nil, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
}
