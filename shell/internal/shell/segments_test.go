package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	proto "github.com/convergent-systems-co/aish/libs/proto/theme"
	"github.com/convergent-systems-co/aish/shell/internal/theme"
)

// makeTheme builds a Compiled theme with the given segments and a
// well-known palette so tests can assert on specific ANSI sequences.
func makeTheme(t *testing.T, segments []string) *theme.Theme {
	t.Helper()
	tm, err := theme.Compile(proto.Brand{
		Name: "test",
		Palette: proto.Palette{
			"primary": "#aabbcc", // prompt color
			"accent":  "#88c0d0", // git color
			"muted":   "#6c757d",
			"error":   "#bf616a", // exit-code color
		},
		Roles: proto.Roles{
			"prompt": "$palette.primary",
			"accent": "$palette.accent",
			"error":  "$palette.error",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{"prompt_char": "❯"},
		},
		Prompt: proto.PromptConfig{
			Segments:   segments,
			Separators: "minimal",
		},
	})
	if err != nil {
		t.Fatalf("theme.Compile: %v", err)
	}
	return tm
}

// ---------- renderCwd / ~-collapse ----------

func TestRenderCwd_HomeCollapse(t *testing.T) {
	// On macOS, t.TempDir() returns a path under /var/ which is a
	// symlink to /private/var/. After Cd, os.Getwd resolves the symlink
	// so cwd has the /private/ prefix; HOME doesn't. Resolve HOME up
	// front so the prefix comparison succeeds.
	raw := t.TempDir()
	home, err := filepath.EvalSymlinks(raw)
	if err != nil {
		home = raw
	}
	t.Setenv("HOME", home)

	s := New()
	if err := s.Cd(home); err != nil {
		t.Fatalf("Cd(home): %v", err)
	}
	tm := makeTheme(t, []string{"cwd"})
	out := s.renderCwd(tm)
	if !strings.Contains(out, "~") {
		t.Errorf("renderCwd in HOME should collapse to ~; got %q", out)
	}
	if strings.Contains(out, home) {
		t.Errorf("renderCwd should not leak full HOME=%q; got %q", home, out)
	}
}

func TestRenderCwd_OutsideHome_KeepsAbsolute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New()
	dir := t.TempDir() // different from HOME
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	tm := makeTheme(t, []string{"cwd"})
	out := s.renderCwd(tm)
	if !strings.Contains(out, filepath.Base(dir)) {
		t.Errorf("renderCwd should include cwd basename; got %q", out)
	}
}

// ---------- renderExitCode ----------

func TestRenderExitCode_ZeroIsEmpty(t *testing.T) {
	s := New()
	s.SetLastExit(0)
	tm := makeTheme(t, []string{"exit-code"})
	if got := s.renderExitCode(tm); got != "" {
		t.Errorf("renderExitCode(0) = %q, want empty (quiet on success)", got)
	}
}

func TestRenderExitCode_NonZeroRendersInErrorColor(t *testing.T) {
	s := New()
	s.SetLastExit(127)
	tm := makeTheme(t, []string{"exit-code"})
	out := s.renderExitCode(tm)
	if !strings.Contains(out, "127") {
		t.Errorf("renderExitCode(127) missing number; got %q", out)
	}
	// ANSI for #bf616a — RGB(191,97,106).
	if !strings.Contains(out, "\x1b[38;2;191;97;106m") {
		t.Errorf("renderExitCode should use error color; got %q", out)
	}
}

// ---------- renderGitStatus ----------

// makeGitRepo creates a minimal "git" working tree at dir/.git/HEAD so
// findGitBranch has something to walk to. We don't need a real git
// repo — only the on-disk shape findGitBranch reads.
func makeGitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	headContent := "ref: refs/heads/" + branch + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(headContent), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
}

func TestRenderGitStatus_InsideRepo_ShowsBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	makeGitRepo(t, dir, "main")

	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	tm := makeTheme(t, []string{"git-status"})
	out := s.renderGitStatus(tm)
	if !strings.Contains(out, "main") {
		t.Errorf("renderGitStatus inside repo should include branch name; got %q", out)
	}
}

func TestRenderGitStatus_NestedDirectory_FindsRepoRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := t.TempDir()
	makeGitRepo(t, repoRoot, "develop")
	nested := filepath.Join(repoRoot, "deep", "nested", "path")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	s := New()
	if err := s.Cd(nested); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	out := s.renderGitStatus(makeTheme(t, []string{"git-status"}))
	if !strings.Contains(out, "develop") {
		t.Errorf("renderGitStatus from nested dir should find parent repo's branch; got %q", out)
	}
}

func TestRenderGitStatus_OutsideRepo_IsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir() // no .git in this temp dir

	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	out := s.renderGitStatus(makeTheme(t, []string{"git-status"}))
	if out != "" {
		t.Errorf("renderGitStatus outside repo should be empty; got %q", out)
	}
}

func TestRenderGitStatus_DetachedHEAD_ShowsShortSHA(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Detached HEAD: HEAD contains a bare SHA, not a ref pointer.
	sha := "abc1234def5678gh9012ijkl3456mn78"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(sha+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	out := s.renderGitStatus(makeTheme(t, []string{"git-status"}))
	if !strings.Contains(out, sha[:7]) {
		t.Errorf("renderGitStatus on detached HEAD should show 7-char SHA prefix; got %q", out)
	}
}

func TestRenderGitStatus_WorktreeRef_ResolvesGitDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Real gitdir lives somewhere else with HEAD inside.
	realRepoTmp := t.TempDir()
	realGitDir := filepath.Join(realRepoTmp, "git-worktree")
	if err := os.MkdirAll(realGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realGitDir, "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Worktree dir contains a `.git` FILE pointing at the real gitdir.
	workDir := t.TempDir()
	gitFile := filepath.Join(workDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+realGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New()
	if err := s.Cd(workDir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	out := s.renderGitStatus(makeTheme(t, []string{"git-status"}))
	if !strings.Contains(out, "feature/x") {
		t.Errorf("renderGitStatus on worktree should resolve gitdir + show branch; got %q", out)
	}
}

// ---------- renderPromptBody composition ----------

func TestRenderPromptBody_JoinsSegmentsWithSpace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	makeGitRepo(t, dir, "main")

	s := New()
	s.SetLastExit(2)
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	tm := makeTheme(t, []string{"cwd", "git-status", "exit-code"})
	body := s.renderPromptBody(tm)
	// All three pieces present.
	for _, want := range []string{filepath.Base(dir), "main", "2"} {
		if !strings.Contains(body, want) {
			t.Errorf("renderPromptBody missing %q; got %q", want, body)
		}
	}
	// Joined by space.
	if !strings.Contains(body, " ") {
		t.Errorf("renderPromptBody should space-join; got %q", body)
	}
}

func TestRenderPromptBody_DropsEmptySegments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir() // no git repo; exit-code 0
	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	tm := makeTheme(t, []string{"cwd", "git-status", "exit-code"})
	body := s.renderPromptBody(tm)
	// Should not have two consecutive spaces from collapsed segments.
	if strings.Contains(body, "  ") {
		t.Errorf("empty segments should drop, not produce double spaces; got %q", body)
	}
}

func TestRenderPromptBody_UnknownSegmentSkipsSilently(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	// "ai-tier" and "drachma-balance" are theme-atoms.com canonical
	// segments that aish v0.2-5 has no renderer for. They must drop
	// silently, not error.
	tm := makeTheme(t, []string{"cwd", "ai-tier", "drachma-balance"})
	body := s.renderPromptBody(tm)
	if !strings.Contains(body, filepath.Base(dir)) {
		t.Errorf("cwd should still render alongside unknown segments; got %q", body)
	}
}

func TestRenderPromptBody_EmptySegmentsFallsBackToCwd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	tm := makeTheme(t, nil) // theme declares no segments
	body := s.renderPromptBody(tm)
	if !strings.Contains(body, filepath.Base(dir)) {
		t.Errorf("missing segments should default to cwd; got %q", body)
	}
}

// ---------- renderPersona (v0.3-5.1 #124) ----------

func TestRenderPersona_NoLoaderIsEmpty(t *testing.T) {
	// Shell built without a HOME or with a torched loader returns "".
	s := &Shell{}
	tm := makeTheme(t, []string{"persona"})
	if got := s.renderPersona(tm); got != "" {
		t.Errorf("renderPersona without loader = %q; want empty", got)
	}
}

func TestRenderPersona_GlyphRendersWithAccentColor(t *testing.T) {
	// HOME with a user persona that declares a non-empty
	// greeting_glyph. The segment renders that glyph painted with the
	// theme's accent colour.
	home := t.TempDir()
	t.Setenv("HOME", home)
	personasDir := filepath.Join(home, ".aish", "personas")
	if err := os.MkdirAll(personasDir, 0o700); err != nil {
		t.Fatalf("mkdir personas: %v", err)
	}
	body := `
name = "glyphy"
version = 1
description = "test persona with a glyph"
voice = "test"
system_prompt = "you are a test"

[tone]
verbosity = "medium"
formality = "neutral"
emoji = false

[capability_gates]

[prompt_overrides]
greeting_glyph = "✦"
voice_phrase = ""
accent_char = ""
`
	if err := os.WriteFile(filepath.Join(personasDir, "glyphy.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write persona: %v", err)
	}
	s := New()
	// Force the loader to pick up the new persona.
	if _, ok := s.personas.Get("glyphy"); !ok {
		t.Fatalf("loader did not pick up glyphy persona")
	}
	s.activePersona = "glyphy"

	tm := makeTheme(t, []string{"persona"})
	out := s.renderPersona(tm)
	if !strings.Contains(out, "✦") {
		t.Errorf("renderPersona = %q; want glyph ✦ present", out)
	}
	// Accent palette in makeTheme is #88c0d0 -> RGB(136,192,208).
	if !strings.Contains(out, "\x1b[38;2;136;192;208m") {
		t.Errorf("renderPersona should use accent color; got %q", out)
	}
}

func TestRenderPersona_EmptyGlyphIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New()
	// Default persona ships with empty greeting_glyph.
	tm := makeTheme(t, []string{"persona"})
	if got := s.renderPersona(tm); got != "" {
		t.Errorf("renderPersona on default persona = %q; want empty", got)
	}
}
