// Package shell is the top-level aish runtime. The minimum shell (v0.1-1)
// reads commands from stdin, dispatches them, and surfaces output. Later
// epics add the intent cache, plugin contract, history engine, and personas.
package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/env"
	"github.com/convergent-systems-co/aish/shell/internal/exec"
	"github.com/convergent-systems-co/aish/shell/internal/parser"
	"github.com/convergent-systems-co/aish/shell/internal/theme"
)

// Shell holds runtime state across REPL iterations: working directory,
// environment, last exit code, and (later) cache/plugin handles.
type Shell struct {
	// cwd is the shell's current working directory. Built-in `cd` updates
	// this; child processes inherit it as their starting cwd.
	cwd string
	// env owns env-var storage and $VAR/$? expansion.
	env *env.Env
	// lastExit is the exit code of the most recent foreground pipeline.
	// Expanded into `$?` and `${?}` by env.Expand.
	lastExit int
	// themes is the registry of available shell brands. Always non-nil
	// after New() — bundled themes guarantee a usable "default" theme.
	themes *theme.Registry
}

// New returns a Shell with cwd initialised to the current process working
// directory and an env seeded from os.Environ. If os.Getwd fails (rare —
// the calling directory was removed under the process), cwd falls back to
// "/" rather than leaving the Shell in an unrecoverable state; the
// caller still gets a usable REPL and the first `cd` will fix it.
func New() *Shell {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}
	e := env.FromSlice(os.Environ())
	reg := theme.NewRegistry()

	// Restore persisted active theme from ~/.aish/config.toml. Failures
	// are silent — the default theme is always a usable fallback.
	if home, _ := e.Get("HOME"); home != "" {
		if active := theme.ReadActiveTheme(home); active != "" {
			_ = reg.SetActive(active) // unknown name silently falls through to "default"
		}
	}

	return &Shell{
		cwd:    cwd,
		env:    e,
		themes: reg,
	}
}

// Run drives the REPL until stdin closes.
//
// Loop shape:
//  1. render prompt to stdout
//  2. read one line from stdin
//  3. if line is `cd` or `cd <path>`, call Cd; on error write to stderr; loop
//  4. if line starts with `export NAME=VALUE`, call SetEnv; on error write
//     to stderr; loop
//  5. otherwise expand $VAR/${VAR}/$? then parser.Parse + exec.Run with
//     s.cwd, s.env.Environ(), and the caller's I/O streams; capture the
//     pipeline's exit code via SetLastExit; loop
//
// Returns nil on clean EOF, non-nil on unrecoverable stdin I/O failures.
func (s *Shell) Run(stdin io.Reader, stdout, stderr io.Writer) error {
	reader := bufio.NewReader(stdin)
	for {
		// Render the prompt before each read so an interactive user sees it.
		// Errors writing the prompt are non-fatal — a piped stdin/stdout
		// session may discard the prompt entirely.
		if _, err := io.WriteString(stdout, s.Prompt()); err != nil {
			// stdout failures end the REPL: there is nowhere left to report.
			return fmt.Errorf("write prompt: %w", err)
		}

		line, readErr := reader.ReadString('\n')
		// Process whatever content arrived before the error, then act on
		// the error itself — this is the bufio idiom for "handle the last
		// unterminated line on EOF".
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed != "" {
			if dispatchErr := s.dispatch(trimmed, stdin, stdout, stderr); dispatchErr != nil {
				// dispatch only returns an error for unrecoverable I/O on the
				// caller's streams. Surface it so the caller can decide.
				return dispatchErr
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read input: %w", readErr)
		}
	}
}

// dispatch routes a single (newline-stripped, non-empty) input line to
// the built-in or external-command path. It captures the exit code on
// the Shell so subsequent `$?` expansions see it.
//
// Returns a non-nil error only when the caller's stdout/stderr cannot be
// written — those are unrecoverable for the REPL. A failing built-in or
// child process is reported via stderr and reflected in lastExit; it
// does not abort Run.
func (s *Shell) dispatch(line string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Whitespace-only lines are no-ops with no exit-code change. POSIX
	// shells behave the same.
	if strings.TrimSpace(line) == "" {
		return nil
	}

	// Built-in: `cd` or `cd <path>`. Trimming the prefix tolerates a
	// trailing space after `cd` (`cd `) and `cd<TAB>` alike.
	if line == "cd" || strings.HasPrefix(line, "cd ") || strings.HasPrefix(line, "cd\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "cd"))
		if err := s.Cd(rest); err != nil {
			fmt.Fprintf(stderr, "aish: cd: %v\n", err)
			s.SetLastExit(1)
			return nil
		}
		s.SetLastExit(0)
		return nil
	}

	// Built-in: `export NAME=VALUE`. Multi-assignment forms (`export A=1 B=2`)
	// and bare `export NAME` (mark for export) are out of scope for v0.1-1.
	if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
		spec := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "export"), "\t"))
		spec = strings.TrimSpace(spec)
		name, value, ok := strings.Cut(spec, "=")
		if !ok {
			fmt.Fprintf(stderr, "aish: export: missing `=` in %q\n", spec)
			s.SetLastExit(1)
			return nil
		}
		// Strip optional surrounding quotes on the value so
		// `export FOO="bar"` and `export FOO='bar'` work as expected.
		value = stripOuterQuotes(value)
		if err := s.SetEnv(name, value); err != nil {
			fmt.Fprintf(stderr, "aish: export: %v\n", err)
			s.SetLastExit(1)
			return nil
		}
		s.SetLastExit(0)
		return nil
	}

	// Built-in: `theme list | show <name> | set <name> | preview <name>`.
	// All theme administration runs here, not via an external; the active
	// theme drives the prompt rendering inside this very process.
	if line == "theme" || strings.HasPrefix(line, "theme ") || strings.HasPrefix(line, "theme\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "theme"))
		args := strings.Fields(rest)
		s.SetLastExit(s.themeBuiltin(args, stdout, stderr))
		return nil
	}

	// External command path: expand variables → parse → exec.
	expanded := s.env.Expand(line, s.lastExit)
	pipeline, parseErr := parser.Parse(expanded)
	if parseErr != nil {
		fmt.Fprintf(stderr, "aish: parse: %v\n", parseErr)
		s.SetLastExit(2)
		return nil
	}
	// Empty pipeline (whitespace after expansion) is a no-op.
	if len(pipeline.Commands) == 0 {
		return nil
	}

	// exec.Run inherits the shell's env and cwd. The cwd is propagated by
	// chdir-ing the parent before Start; v0.1-1 keeps it simple by relying
	// on the process-wide cwd, which Cd has already set via os.Chdir.
	exitCode, runErr := exec.Run(
		context.Background(),
		pipeline,
		s.env.Environ(),
		stdin,
		stdout,
		stderr,
	)
	if runErr != nil {
		fmt.Fprintf(stderr, "aish: %v\n", runErr)
		s.SetLastExit(127) // POSIX convention for "command not found / not runnable"
		return nil
	}
	s.SetLastExit(exitCode)
	return nil
}

// Cwd returns the shell's current working directory.
func (s *Shell) Cwd() string {
	return s.cwd
}

// Cd changes the shell's working directory. A relative path resolves
// against the current Cwd; `~` (alone or as a path prefix `~/sub`)
// expands to $HOME. An empty path is treated as `~` — the POSIX
// convention for bare `cd`. Returns a non-nil error if the target does
// not exist or is not a directory.
//
// os.Chdir is invoked so that subsequent os/exec child processes (which
// inherit the parent's cwd by default when cmd.Dir is unset) start in
// the shell's working directory. exec.Run does not set cmd.Dir today; if
// that changes, this function still keeps the parent's cwd in sync so
// other stdlib calls (os.Stat on a relative path, etc.) behave.
func (s *Shell) Cd(path string) error {
	target := expandTilde(path, s.env)
	if target == "" {
		// Bare `cd` with no $HOME falls back to staying put — emit an error
		// so the REPL surfaces it rather than silently succeeding.
		return errors.New("HOME not set; cannot resolve `cd`")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(s.cwd, target)
	}
	fi, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("not a directory: %s", target)
	}
	if err := os.Chdir(target); err != nil {
		return err
	}
	// Re-read via Getwd so the stored cwd reflects symlink resolution
	// consistent with how child processes will see it.
	if resolved, gwerr := os.Getwd(); gwerr == nil {
		s.cwd = resolved
	} else {
		s.cwd = target
	}
	return nil
}

// SetEnv binds name=value in the shell env.
func (s *Shell) SetEnv(name, value string) error {
	return s.env.Set(name, value)
}

// GetEnv returns the bound value of name and whether it was set.
func (s *Shell) GetEnv(name string) (string, bool) {
	return s.env.Get(name)
}

// LastExit returns the exit code of the most recent foreground pipeline.
// Zero before any command has run.
func (s *Shell) LastExit() int {
	return s.lastExit
}

// SetLastExit records code as the most recent pipeline's exit code.
// Visible to subsequent input via `$?` and `${?}` substitution.
func (s *Shell) SetLastExit(code int) {
	s.lastExit = code
}

// Prompt renders the prompt string aish writes before each REPL read.
//
// v0.1-1 baseline: "<cwd-shortened> > " where `~` substitutes for the
// $HOME prefix.
//
// v0.2-5 theming: when an active theme is present, the cwd is wrapped in
// the theme's `prompt` ANSI sequence and the `prompt_char` glyph (e.g.
// "❯") replaces the literal ">". Themes with no prompt color or no
// glyph fall back to the baseline string.
func (s *Shell) Prompt() string {
	display := s.cwd
	if home, ok := s.env.Get("HOME"); ok && home != "" {
		switch {
		case display == home:
			display = "~"
		case strings.HasPrefix(display, home+string(filepath.Separator)):
			display = "~" + display[len(home):]
		}
	}

	active := s.themes.Active()
	promptChar := active.Glyph("prompt_char", ">")
	return active.ColorPrompt(display) + " " + promptChar + " "
}

// Themes returns the theme registry. Exposed for the `theme` built-in.
func (s *Shell) Themes() *theme.Registry {
	return s.themes
}

// expandTilde returns path with a leading `~` or `~/` replaced by $HOME.
// A bare empty string is treated as `~` (bare-cd semantics). If $HOME is
// unset and the path needs $HOME, returns "" so the caller can surface
// the failure.
func expandTilde(path string, e *env.Env) string {
	if path == "" || path == "~" {
		if home, ok := e.Get("HOME"); ok && home != "" {
			return home
		}
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if home, ok := e.Get("HOME"); ok && home != "" {
			return filepath.Join(home, path[2:])
		}
		return ""
	}
	return path
}

// stripOuterQuotes removes one layer of matching single or double quotes
// surrounding s. This is the minimum needed to make `export FOO="bar"`
// behave as expected; full quote-aware parsing inside `export` is
// deferred until the shell grows a real built-in dispatcher.
func stripOuterQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
