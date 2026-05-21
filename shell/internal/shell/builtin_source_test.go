package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

func writeTempFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestSourceBuiltin_TOMLEnvAndAliases(t *testing.T) {
	path := writeTempFile(t, `
[env]
FOO = "bar"

[aliases]
ll = "ls -la"
`)
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.sourceBuiltin([]string{path}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
	}
	if v, _ := s.env.Get("FOO"); v != "bar" {
		t.Errorf("FOO = %q, want bar", v)
	}
	if v, _ := s.aliasGet("ll"); v != "ls -la" {
		t.Errorf("ll alias = %q, want %q", v, "ls -la")
	}
}

func TestSourceBuiltin_DotEnvStyle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	body := `# comment
FOO=bar
export BAZ=qux
QUOTED="has spaces"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.sourceBuiltin([]string{path}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
	}
	if v, _ := s.env.Get("FOO"); v != "bar" {
		t.Errorf("FOO = %q, want bar", v)
	}
	if v, _ := s.env.Get("BAZ"); v != "qux" {
		t.Errorf("BAZ = %q, want qux", v)
	}
	if v, _ := s.env.Get("QUOTED"); v != "has spaces" {
		t.Errorf("QUOTED = %q, want 'has spaces'", v)
	}
}

func TestSourceBuiltin_MissingFileExits1(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.sourceBuiltin([]string{"/no/such/file"}, &stdout, &stderr)
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr.String(), "no such file") {
		t.Errorf("stderr = %q, want 'no such file'", stderr.String())
	}
}

func TestSourceBuiltin_NoArgsExits2(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.sourceBuiltin(nil, &stdout, &stderr)
	if exit != 2 {
		t.Errorf("exit = %d, want 2", exit)
	}
}
