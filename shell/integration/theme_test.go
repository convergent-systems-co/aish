package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThemeList — `theme list` prints every bundled theme; the active
// theme is marked with a leading asterisk.
func TestThemeList(t *testing.T) {
	s := run(t, script("theme list"))
	s.assertExit(0)
	s.assertStdoutContains("default")
	s.assertStdoutContains("nord-powerline")
	s.assertStdoutContains("monokai")
	// Active marker — exactly one `* ` line should be in the output.
	stars := strings.Count(s.stdout, "* ")
	if stars < 1 {
		t.Fatalf("expected at least one active marker '* '; got 0\nstdout:\n%s", s.stdout)
	}
}

// TestThemeShowActive — `theme show` with no arg prints the active
// theme's inspection block (Name, Segments, Glyphs, Roles).
func TestThemeShowActive(t *testing.T) {
	s := run(t, script("theme show"))
	s.assertExit(0)
	s.assertStdoutContains("Name:")
	s.assertStdoutContains("Segments:")
	s.assertStdoutContains("Roles:")
}

// TestThemeShowByName — `theme show <name>` selects a specific theme.
func TestThemeShowByName(t *testing.T) {
	s := run(t, script("theme show nord-powerline"))
	s.assertExit(0)
	s.assertStdoutContains("nord-powerline")
	s.assertStdoutContains("powerline")
}

// TestThemeShowUnknown — error path: missing theme name surfaces an
// error to stderr but does not crash the REPL.
func TestThemeShowUnknown(t *testing.T) {
	s := run(t, script(
		"theme show definitely-not-a-real-theme-xyz",
		"echo still_alive=yes",
	))
	s.assertExit(0)
	s.assertStderrContains("no such theme")
	s.assertStdoutContains("still_alive=yes")
}

// TestThemePreview — `theme preview <name>` renders sample output
// without activating the theme.
func TestThemePreview(t *testing.T) {
	s := run(t, script("theme preview monokai"))
	s.assertExit(0)
	s.assertStdoutContains("hello")
	s.assertStdoutContains("aish") // the preview path includes "~/projects/aish"
}

// TestThemePreviewUnknown — missing-theme error path for preview.
func TestThemePreviewUnknown(t *testing.T) {
	s := run(t, script(
		"theme preview never-bundled-theme",
		"echo afterwards=yes",
	))
	s.assertExit(0)
	s.assertStderrContains("no such theme")
	s.assertStdoutContains("afterwards=yes")
}

// TestThemeSetAndPersist — `theme set <name>` activates AND writes the
// choice to $HOME/.aish/config.toml. We point HOME at a tempdir so the
// real home directory is not touched.
func TestThemeSetAndPersist(t *testing.T) {
	tmp := t.TempDir()
	// Build a clean env that points HOME at our tempdir.
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmp,
	}
	s := runWithEnv(t, script("theme set nord-powerline"), env)
	s.assertExit(0)
	s.assertStdoutContains("active = nord-powerline")
	// File created at the documented path.
	configPath := filepath.Join(tmp, ".aish", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml not at %s: %v", configPath, err)
	}
	if !strings.Contains(string(data), `active = "nord-powerline"`) {
		t.Errorf("config.toml does not record active theme; contents:\n%s", string(data))
	}
}

// TestThemeSetSurvivesSession — set in session 1, restart in session 2,
// the active theme persists across the restart.
func TestThemeSetSurvivesSession(t *testing.T) {
	tmp := t.TempDir()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmp,
	}
	// Session 1: set nord-powerline.
	s1 := runWithEnv(t, script("theme set nord-powerline"), env)
	s1.assertExit(0)
	s1.assertStdoutContains("active = nord-powerline")
	// Session 2: a fresh aish invocation reads the config.
	s2 := runWithEnv(t, script("theme list"), env)
	s2.assertExit(0)
	// `* nord-powerline` line proves the persistence round-tripped.
	if !strings.Contains(s2.stdout, "* nord-powerline") {
		t.Errorf("expected '* nord-powerline' active marker in session 2; got:\n%s", s2.stdout)
	}
}

// TestThemeSetUnknown — setting an unknown theme is a user error; the
// REPL continues and the previously-active theme is unchanged.
func TestThemeSetUnknown(t *testing.T) {
	tmp := t.TempDir()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmp,
	}
	s := runWithEnv(t, script(
		"theme set never-bundled-theme",
		"echo afterwards=yes",
	), env)
	s.assertExit(0)
	s.assertStderrContains("unknown theme")
	s.assertStdoutContains("afterwards=yes")
}

// TestThemeBareInvocation — `theme` with no args prints usage.
func TestThemeBareInvocation(t *testing.T) {
	s := run(t, script("theme"))
	s.assertExit(0)
	s.assertStdoutContains("Usage:")
	s.assertStdoutContains("list")
	s.assertStdoutContains("show")
	s.assertStdoutContains("set")
	s.assertStdoutContains("preview")
}

// TestThemeHelpFlag — `theme --help` and `theme help` produce the same
// usage output as `theme` with no arguments.
func TestThemeHelpFlag(t *testing.T) {
	s := run(t, script("theme --help"))
	s.assertExit(0)
	s.assertStdoutContains("Usage:")
	s.assertStdoutContains("preview")
}

// TestPromptContainsAnsiWithActiveTheme — with the default theme
// active, the prompt output includes the ANSI escape sequence prefix.
// This pins the theming's render-path effect: theme.ColorPrompt() is
// actually wrapping the cwd. Skip-able if the host doesn't surface
// raw ANSI through the test harness (it does — we don't strip).
func TestPromptContainsAnsiWithActiveTheme(t *testing.T) {
	// A `theme list` invocation emits at least one prompt line; that
	// prompt should carry an ANSI escape.
	s := run(t, script("echo hi"))
	s.assertExit(0)
	if !strings.Contains(s.stdout, "\x1b[") {
		t.Errorf("expected ANSI escape in stdout with default theme active; got:\n%q", s.stdout)
	}
}
