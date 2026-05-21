//go:build windows

package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadRCFiles_WindowsAppdata exercises the loadRCFiles
// integration path under GOOS=windows. The test sets %APPDATA% to
// a tempdir, writes an aishrc.toml inside that tempdir's
// `aish/` sub-folder, and asserts that loadRCFiles picked it up.
//
// Build-tagged windows so it only runs on Windows CI / on a
// Windows host. The cross-platform helper test
// (TestUserRCPathForGoos) in login_paths_test.go covers the
// pure-function logic from any host.
func TestLoadRCFiles_WindowsAppdata(t *testing.T) {
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)
	t.Setenv("PROGRAMDATA", t.TempDir()) // ensure no real system RC fires

	aishDir := filepath.Join(appdata, "aish")
	if err := os.MkdirAll(aishDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rcPath := filepath.Join(aishDir, "aishrc.toml")
	if err := os.WriteFile(rcPath, []byte(`
[env]
WINDOWS_RC_LOADED = "yes"
`), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}

	// Use the test constructor — homeDir is irrelevant here
	// because the Windows path resolver pulls APPDATA, not
	// HOME, when both are present.
	s := newLoginShellForTest(t, "")
	_ = s.env.Set("USERPROFILE", t.TempDir())

	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if v, _ := s.env.Get("WINDOWS_RC_LOADED"); v != "yes" {
		t.Errorf("WINDOWS_RC_LOADED = %q, want %q (RC at %s)", v, "yes", rcPath)
	}
}
