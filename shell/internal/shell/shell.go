// Package shell is the top-level aish runtime. The minimum shell (v0.1-1)
// reads commands from stdin, dispatches them, and surfaces output. Later
// epics add the intent cache, plugin contract, history engine, and personas.
package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/cache"
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
	// cache is the L1 intent cache + (optional) inference plugin handle.
	// nil when the SQLite store at ~/.aish/cache.db cannot be opened
	// (read-only home, disk full, etc.) — the shell still runs, just
	// without the AI-native dispatch tier.
	cache *cache.Cache
	// cacheStore + cachePlugin are held separately so Close() can tear
	// them down explicitly. cache.New takes ownership of these but
	// doesn't expose a unified Close, so the shell owns the lifecycle.
	cacheStore  *cache.Store
	cachePlugin *cache.PluginClient
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

	// Load any previously-synced Brand-Atoms themes from the local cache
	// so `theme list` shows them even before the user runs `theme sync`
	// again. Silent — a missing cache directory or a broken file is
	// non-fatal (the bundled themes are always a usable floor).
	if home := homeDir(e); home != "" {
		cacheDir := filepath.Join(home, ".aish", theme.CacheDirName)
		_, _ = theme.LoadCacheDir(cacheDir, reg)
	}

	// Restore persisted active theme from ~/.aish/config.toml. Failures
	// are silent — the default theme is always a usable fallback.
	if home := homeDir(e); home != "" {
		if active := theme.ReadActiveTheme(home); active != "" {
			_ = reg.SetActive(active) // unknown name silently falls through to "default"
		}
	}

	s := &Shell{
		cwd:    cwd,
		env:    e,
		themes: reg,
	}

	// Open the L1 intent cache at ~/.aish/cache.db and (when a bearer
	// key is set) eagerly start the inference plugin as a child. Any
	// failure is logged-by-omission — the shell still works without a
	// cache, just without the AI-native dispatch tier.
	s.openCache(e)

	return s
}

// Close releases shell-owned resources (cache DB, plugin child process).
// Idempotent. Safe to call on a freshly-constructed Shell that never
// successfully opened a cache.
func (s *Shell) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	if s.cachePlugin != nil {
		if err := s.cachePlugin.Close(); err != nil {
			firstErr = err
		}
		s.cachePlugin = nil
	}
	if s.cacheStore != nil {
		if err := s.cacheStore.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.cacheStore = nil
	}
	s.cache = nil
	return firstErr
}

// openCache opens ~/.aish/cache.db (creating the directory if needed)
// and, when a bearer key is present in env, attempts to start the
// aish-inference-cloud child plugin. On total failure the shell falls
// back to no-cache mode.
func (s *Shell) openCache(e *env.Env) {
	home := homeDir(e)
	if home == "" {
		return
	}
	dbDir := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return
	}
	store, err := cache.Open(filepath.Join(dbDir, "cache.db"))
	if err != nil {
		return
	}
	s.cacheStore = store
	s.cachePlugin = tryStartPlugin(e)
	s.cache = cache.New(store, s.cachePlugin)
}

// tryStartPlugin spawns the inference plugin when a bearer key is set
// and the binary resolves on PATH (or via $AISH_INFERENCE_PLUGIN).
// Returns nil on any startup failure; the cache then runs in lookup-
// only mode.
//
// The plugin defaults to api.convergent-systems.co/llm/v1 (set on the
// plugin side via DefaultBaseURL). $ANTHROPIC_BASE_URL overrides.
func tryStartPlugin(e *env.Env) *cache.PluginClient {
	// Avoid spawning a child that will exit 2 immediately because the
	// API key isn't set. The plugin reads $ANTHROPIC_API_KEY.
	if k, _ := e.Get("ANTHROPIC_API_KEY"); k == "" {
		return nil
	}
	binary := ""
	if v, ok := e.Get("AISH_INFERENCE_PLUGIN"); ok {
		binary = v
	}
	pc, err := cache.Start(cache.PluginConfig{
		BinaryPath: binary,
		Env:        e.Environ(),
		Stderr:     os.Stderr,
	})
	if err != nil {
		return nil
	}
	return pc
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
	for {
		// Render the prompt before each read so an interactive user sees it.
		// Errors writing the prompt are non-fatal — a piped stdin/stdout
		// session may discard the prompt entirely.
		if _, err := io.WriteString(stdout, s.Prompt()); err != nil {
			// stdout failures end the REPL: there is nowhere left to report.
			return fmt.Errorf("write prompt: %w", err)
		}

		// readLine reads one byte at a time so we never prefetch bytes that
		// a child process (`cat`, `head`, …) is supposed to consume. See
		// issue #167.
		line, readErr := readLine(stdin)
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

// readLine reads from r byte-by-byte until it sees `\n` or hits EOF. The
// returned line INCLUDES the trailing `\n` (if any). The caller is
// responsible for trimming.
//
// Byte-by-byte reads are intentional: any buffered prefetch would steal
// bytes that external children (`cat`, `head`, `read`) need to consume.
// See issue #167 for the regression we're avoiding.
//
// The performance cost is one syscall per typed character. At shell-input
// rates that's invisible; even for scripted input piped through a 1 MB
// stdin, the OS-level pipe buffer makes each Read effectively a memcpy.
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	var buf [1]byte
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			b.WriteByte(buf[0])
			if buf[0] == '\n' {
				return b.String(), nil
			}
		}
		if err != nil {
			return b.String(), err
		}
	}
}

// dispatch routes a single (newline-stripped, non-empty) input line to
// the built-in or external-command path. It captures the exit code on
// the Shell so subsequent `$?` expansions see it.
//
// stdin is passed straight through to external children. Because Run
// reads input byte-by-byte (no bufio prefetch), the bytes following the
// current line are still available on stdin for the child to consume
// (e.g. `cat` reading subsequent lines). See issue #167.
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

	// Built-in: `cache stats | clear`. Per v0.1-2 acceptance.
	if line == "cache" || strings.HasPrefix(line, "cache ") || strings.HasPrefix(line, "cache\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "cache"))
		args := strings.Fields(rest)
		s.SetLastExit(s.cacheBuiltin(args, stdout, stderr))
		return nil
	}

	// Dispatch tier 1 — known-binary passthrough. If the first token
	// resolves on PATH (or via the shell's env override), treat the line
	// as a literal command. This preserves POSIX behavior for everything
	// the user expects to "just work" (cat, ls, grep, vim, …) and avoids
	// paying the cache + plugin round-trip on the hot path.
	expanded := s.env.Expand(line, s.lastExit)
	if first := firstToken(expanded); first != "" && isKnownBinary(first, s.env) {
		return s.runExternal(expanded, stdin, stdout, stderr)
	}

	// Dispatch tier 2/3 — AI-native: cache lookup → plugin inference →
	// cache write-back. The raw line (NOT $VAR-expanded) is the intent;
	// the plugin compiles it to an invocation, which we then run through
	// the normal external path.
	if s.cache != nil {
		invocation, _, err := s.cache.Resolve(context.Background(), line, runtime.GOOS)
		switch {
		case err == nil:
			return s.runExternal(invocation, stdin, stdout, stderr)
		case errors.Is(err, cache.ErrNoPlugin):
			// No plugin configured (no API key, no binary on PATH). Fall
			// through to the legacy parser+exec path so the user sees
			// the familiar "command not found" exit-127.
		default:
			fmt.Fprintf(stderr, "aish: %v\n", err)
			s.SetLastExit(127)
			return nil
		}
	}

	// Dispatch tier 4 — legacy fallback (parse + exec on $VAR-expanded
	// text). Yields exit-127 for unrecognised commands.
	return s.runExternal(expanded, stdin, stdout, stderr)
}

// runExternal parses cmdline as a pipeline and runs it. Used by both
// the known-binary tier (with $VAR-expanded text) and the cache tier
// (with the plugin-compiled invocation).
func (s *Shell) runExternal(cmdline string, stdin io.Reader, stdout, stderr io.Writer) error {
	pipeline, parseErr := parser.Parse(cmdline)
	if parseErr != nil {
		fmt.Fprintf(stderr, "aish: parse: %v\n", parseErr)
		s.SetLastExit(2)
		return nil
	}
	if len(pipeline.Commands) == 0 {
		return nil
	}
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
		s.SetLastExit(127)
		return nil
	}
	s.SetLastExit(exitCode)
	return nil
}

// firstToken returns the first whitespace-separated token of line.
// Used to peek at "is this command a known binary?" without paying a
// full parser.Parse — the tokenization here is lossy on purpose
// (single-quoted strings, redirects, etc. are not respected).
func firstToken(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	for i, r := range line {
		if r == ' ' || r == '\t' {
			return line[:i]
		}
	}
	return line
}

// isKnownBinary returns true when tok resolves to an executable on the
// shell's $PATH. Uses os/exec.LookPath after pivoting through the
// shell's env so an in-process `export PATH=...` is honored.
func isKnownBinary(tok string, e *env.Env) bool {
	// Absolute / relative paths bypass PATH lookup.
	if strings.ContainsAny(tok, "/\\") {
		_, err := os.Stat(tok)
		return err == nil
	}
	// Pivot $PATH through the shell env so the lookup matches what an
	// actual exec would resolve to.
	pathBefore := os.Getenv("PATH")
	if p, ok := e.Get("PATH"); ok {
		_ = os.Setenv("PATH", p)
		defer os.Setenv("PATH", pathBefore)
	}
	_, err := osexec.LookPath(tok)
	return err == nil
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
// v0.2-5 multi-segment theming: the active theme declares an ordered
// list of segments in `[prompt].segments`. Prompt() walks the list via
// renderPromptBody (segments.go), space-joins the rendered segments,
// and appends the themed prompt_char glyph. Segments that have no data
// for the current state (e.g. git when cwd is not in a repo, exit when
// last exit was 0) render as empty and drop out of the joined body.
func (s *Shell) Prompt() string {
	active := s.themes.Active()
	body := s.renderPromptBody(active)
	promptChar := active.Glyph("prompt_char", ">")
	return body + " " + active.ColorPrompt(promptChar) + " "
}

// Themes returns the theme registry. Exposed for the `theme` built-in.
func (s *Shell) Themes() *theme.Registry {
	return s.themes
}

// expandTilde returns path with a leading `~` or `~/` replaced by the
// user's home directory (resolved per homeDir — $HOME on POSIX,
// $USERPROFILE on Windows). A bare empty string is treated as `~` (the
// bare-cd semantic). If neither env var is set, returns "" so the caller
// can surface the failure.
func expandTilde(path string, e *env.Env) string {
	if path == "" || path == "~" {
		if home := homeDir(e); home != "" {
			return home
		}
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if home := homeDir(e); home != "" {
			return filepath.Join(home, path[2:])
		}
		return ""
	}
	return path
}

// homeDir returns the user's home directory according to the shell's
// env. On POSIX systems this is $HOME. On Windows the equivalent is
// $USERPROFILE; we accept either (preferring $HOME when both are set)
// so the same code path works across platforms without an explicit
// runtime.GOOS check.
//
// Returns "" if neither is set — callers should treat that as "no home"
// and surface an error rather than silently using a wrong default.
func homeDir(e *env.Env) string {
	if h, ok := e.Get("HOME"); ok && h != "" {
		return h
	}
	if h, ok := e.Get("USERPROFILE"); ok && h != "" {
		return h
	}
	return ""
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
