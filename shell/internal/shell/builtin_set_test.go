package shell

import (
	"bytes"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

func TestSetBuiltin_BareLists(t *testing.T) {
	s := &Shell{env: env.New()}
	_ = s.env.Set("FOO", "bar")
	_ = s.env.Set("BAZ", "qux")
	var stdout, stderr bytes.Buffer
	exit := s.setBuiltin(nil, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	out := stdout.String()
	if !strings.Contains(out, "FOO=bar") || !strings.Contains(out, "BAZ=qux") {
		t.Errorf("bare set output = %q", out)
	}
}

func TestSetBuiltin_AssignsLocalEnv(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.setBuiltin([]string{"FOO=bar"}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
	}
	if v, _ := s.env.Get("FOO"); v != "bar" {
		t.Errorf("FOO = %q, want bar", v)
	}
}

func TestSetBuiltin_RejectsOptionFlags(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.setBuiltin([]string{"-e"}, &stdout, &stderr)
	if exit != 2 {
		t.Errorf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr.String(), "not supported") {
		t.Errorf("stderr = %q, want 'not supported'", stderr.String())
	}
}

func TestSetBuiltin_StripsQuotedValue(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	if exit := s.setBuiltin([]string{`MSG="hello world"`}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if v, _ := s.env.Get("MSG"); v != "hello world" {
		t.Errorf("MSG = %q, want %q", v, "hello world")
	}
}

func TestUnsetBuiltin_RemovesEnv(t *testing.T) {
	s := &Shell{env: env.New()}
	_ = s.env.Set("FOO", "bar")
	var stdout, stderr bytes.Buffer
	exit := s.unsetBuiltin([]string{"FOO"}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr.String())
	}
	if _, ok := s.env.Get("FOO"); ok {
		t.Error("FOO still set after unset")
	}
}

func TestUnsetBuiltin_UnknownNameIsNoOp(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.unsetBuiltin([]string{"NEVER_SET"}, &stdout, &stderr)
	if exit != 0 {
		t.Errorf("exit = %d, want 0 (POSIX: unset unset is no-op)", exit)
	}
}

func TestUnsetBuiltin_NoArgsExits2(t *testing.T) {
	s := &Shell{env: env.New()}
	var stdout, stderr bytes.Buffer
	exit := s.unsetBuiltin(nil, &stdout, &stderr)
	if exit != 2 {
		t.Errorf("exit = %d, want 2", exit)
	}
}
