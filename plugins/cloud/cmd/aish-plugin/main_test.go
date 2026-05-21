package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeFakeBinary writes a tiny "binary" file under dir and returns
// its absolute path.
func makeFakeBinary(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

func TestRun_ListEmpty(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"aish-plugin", "list", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list empty exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No plugins installed") {
		t.Fatalf("expected 'No plugins installed', got %q", stdout.String())
	}
}

func TestRun_InstallThenList(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	bin := makeFakeBinary(t, binDir, "aish-fake", "fake\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"aish-plugin", "install",
		"--root", root,
		"--binary", bin,
		"--name", "fake",
		"--version", "0.1.0",
		"--kinds", "inference",
		"--signer", "aish-dev",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed fake") {
		t.Fatalf("expected installed line, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"aish-plugin", "list", "--root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list exit=%d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "fake") || !strings.Contains(out, "v0.1.0") {
		t.Fatalf("list missing plugin row: %q", out)
	}
}

func TestRun_VerifyAfterInstall(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	bin := makeFakeBinary(t, binDir, "aish-fake", "fake\n")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"aish-plugin", "install",
		"--root", root, "--binary", bin, "--name", "fake",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("install: %d %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"aish-plugin", "verify", "--root", root, "fake"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("verify exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "ok: fake") {
		t.Fatalf("expected ok line, got %q", stdout.String())
	}
}

func TestRun_VerifyDetectsTamper(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	bin := makeFakeBinary(t, binDir, "aish-fake", "fake\n")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"aish-plugin", "install",
		"--root", root, "--binary", bin, "--name", "fake",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("install: %d %s", code, stderr.String())
	}

	// Tamper with the binary post-install.
	if err := os.WriteFile(bin, []byte("tampered\n"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"aish-plugin", "verify", "--root", root, "fake"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected verify to fail on tampered binary")
	}
	if !strings.Contains(stderr.String(), "sha256") {
		t.Fatalf("expected sha256 mismatch in stderr, got %q", stderr.String())
	}
}

func TestRun_RemoveDeletes(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	bin := makeFakeBinary(t, binDir, "aish-fake", "fake\n")
	var stdout, stderr bytes.Buffer
	_ = run([]string{"aish-plugin", "install", "--root", root, "--binary", bin, "--name", "fake"}, &stdout, &stderr)

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"aish-plugin", "remove", "--root", root, "fake"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("remove exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "removed fake") {
		t.Fatalf("expected removed message, got %q", stdout.String())
	}
	// Confirm it's gone.
	if _, err := os.Stat(filepath.Join(root, "fake")); !os.IsNotExist(err) {
		t.Fatalf("expected fake dir removed, stat err=%v", err)
	}
}

func TestRun_RejectsNonDevSigner(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	bin := makeFakeBinary(t, binDir, "aish-fake", "fake\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"aish-plugin", "install",
		"--root", root, "--binary", bin, "--name", "fake",
		"--signer", "other",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected install to refuse non-dev signer")
	}
	if !strings.Contains(stderr.String(), "aish-dev") {
		t.Fatalf("expected error to name aish-dev as the supported signer, got %q", stderr.String())
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"aish-plugin", "danceparty"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for unknown subcommand, got %d", code)
	}
}

func TestRun_HelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"aish-plugin", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--help exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "Subcommands") {
		t.Fatalf("--help output missing 'Subcommands': %q", stdout.String())
	}
}

func TestRun_VersionExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"aish-plugin", "--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--version exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "aish-plugin") {
		t.Fatalf("--version output missing program name: %q", stdout.String())
	}
}
