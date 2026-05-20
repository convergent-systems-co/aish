package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/theme"
)

// renderPromptBody walks the active theme's `[prompt].segments` and
// composes a themed prompt body (no trailing prompt_char — that's
// appended by Prompt()).
//
// theme-atoms.com themes declare segments like:
//
//	segments = ["cwd", "git-status", "ai-tier", "exit-code"]
//
// v0.2-5 implements `cwd`, `git`/`git-status`, `exit`/`exit-code`.
// Unknown segments are silently skipped so themes from richer profiles
// (ai-tier, drachma-balance, weather, etc. — landing with v0.1-2 cache
// and v0.3-3 economy) gracefully degrade.
//
// Empty segment renders are dropped so the prompt does not carry empty
// space when (say) the cwd is not inside a git repo.
func (s *Shell) renderPromptBody(active *theme.Theme) string {
	segs := active.Segments()
	if len(segs) == 0 {
		segs = []string{"cwd"} // floor — every theme has at least cwd
	}
	parts := make([]string, 0, len(segs))
	for _, name := range segs {
		if r := s.renderSegment(name, active); r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, " ")
}

// renderSegment dispatches one named segment to its renderer.
func (s *Shell) renderSegment(name string, t *theme.Theme) string {
	switch name {
	case "cwd":
		return s.renderCwd(t)
	case "git", "git-status":
		return s.renderGitStatus(t)
	case "exit", "exit-code":
		return s.renderExitCode(t)
	case "prompt":
		// The trailing prompt-char is added by Prompt() — declaring
		// "prompt" as a segment is the canonical way themes indicate
		// "the prompt char goes here," but the rendering happens at the
		// composition boundary, not as part of the body.
		return ""
	default:
		// Unknown segment: skip silently. theme-atoms.com declares
		// future segments (ai-tier, drachma-balance, weather, host,
		// time, duration) that aish will render once their data
		// sources land in subsequent epics.
		return ""
	}
}

// renderCwd renders the working-directory segment with `~` collapse and
// the theme's prompt color.
func (s *Shell) renderCwd(t *theme.Theme) string {
	display := s.cwd
	if home := homeDir(s.env); home != "" {
		switch {
		case display == home:
			display = "~"
		case strings.HasPrefix(display, home+string(filepath.Separator)):
			display = "~" + display[len(home):]
		}
	}
	return t.ColorPrompt(display)
}

// renderGitStatus renders a git-aware segment: branch name when the
// cwd is inside a git working tree, empty otherwise.
//
// Pure-Go — walks up from cwd looking for `.git/`, reads `HEAD`, parses
// the symbolic ref or detached-HEAD SHA. Does NOT shell out to `git`
// (the prompt renders on every keystroke when v0.2-1 ghost-text lands;
// fork+exec per keypress is unacceptable).
//
// Does NOT compute dirty status (would require diffing index against
// working tree — expensive and the wrong place). The `±` dirty indicator
// is v0.2-1 work, gated behind a debounced background watcher.
func (s *Shell) renderGitStatus(t *theme.Theme) string {
	branch := findGitBranch(s.cwd)
	if branch == "" {
		return ""
	}
	glyph := t.Glyph("git_branch", "")
	label := branch
	if glyph != "" {
		label = glyph + " " + branch
	}
	return t.ColorAccent(label)
}

// findGitBranch walks up from `start` looking for `.git/`. Returns the
// branch name (or short SHA for detached HEAD), or "" when the path is
// not inside a git working tree.
func findGitBranch(start string) string {
	dir := start
	for {
		gitPath := filepath.Join(dir, ".git")
		fi, err := os.Stat(gitPath)
		if err == nil {
			// .git is usually a directory; in worktrees it's a file
			// pointing at the real gitdir. Handle both.
			gitDir := gitPath
			if !fi.IsDir() {
				gitDir = resolveGitFileRef(gitPath)
				if gitDir == "" {
					return ""
				}
			}
			return readBranchFromHEAD(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // hit filesystem root
		}
		dir = parent
	}
}

// resolveGitFileRef handles the `.git: gitdir: /path/to/real/gitdir`
// indirection used by worktrees and submodules.
func resolveGitFileRef(gitFile string) string {
	raw, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		s := strings.TrimSpace(string(line))
		if rest, ok := strings.CutPrefix(s, "gitdir:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// readBranchFromHEAD parses gitDir/HEAD into a branch name or short SHA.
// "ref: refs/heads/main" → "main"; bare SHA → first 7 chars.
func readBranchFromHEAD(gitDir string) string {
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	if rest, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
		return rest
	}
	if len(line) >= 7 {
		return line[:7] // detached HEAD shows short SHA
	}
	return ""
}

// renderExitCode shows the last-command exit code IF non-zero, painted
// with the theme's error color. Returns "" on success so the prompt is
// quiet during the golden path.
func (s *Shell) renderExitCode(t *theme.Theme) string {
	if s.lastExit == 0 {
		return ""
	}
	return t.ColorError(strconv.Itoa(s.lastExit))
}
