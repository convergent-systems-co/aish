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
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/cache"
	"github.com/convergent-systems-co/aish/shell/internal/cache/community"
	"github.com/convergent-systems-co/aish/shell/internal/env"
	"github.com/convergent-systems-co/aish/shell/internal/history"
	"github.com/convergent-systems-co/aish/shell/internal/parser"
	"github.com/convergent-systems-co/aish/shell/internal/persona"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
	"github.com/convergent-systems-co/aish/shell/internal/telemetry"
	"github.com/convergent-systems-co/aish/shell/internal/term"
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
	// history is the v0.1-4 reversibility interceptor — wraps the
	// event log + snapshotter. nil when ~/.aish is unwritable; the
	// shell still runs but every destructive command is permanent
	// (degrades gracefully per the same pattern as the cache).
	history *history.History
	// telemetry is the v0.1-5 measurement interceptor — per-session
	// counters + cost tracking + opt-in aggregate queue. nil when
	// ~/.aish is unwritable; `aish stats` then prints "telemetry
	// not available."
	telemetry *telemetry.Recorder
	// community is the v0.2-3 L3 community-cache bundle. nil when
	// no bundle is installed or available on disk; `aish community
	// info` then reports "not loaded". The cache's L3 wire-up
	// (Cache.WithCommunityBundle) is set whenever this is non-nil.
	community *community.Bundle
	// personas is the v0.3-5 persona registry — bundled + user
	// overrides. Always non-nil after New() — the bundled set
	// guarantees a usable "default" persona even with no user files.
	personas *persona.Loader
	// activePersona is the name of the persona currently shaping
	// inference dispatch. Empty means "default"; the Persona()
	// accessor handles the fallback. Written by `persona set` and
	// restored from ~/.aish/config.toml on New().
	activePersona string
	// interceptors is the registered set of PreCommand/PostCommand
	// observers. History registers as one entry; telemetry (v0.1-5)
	// registers a second. Order is insertion order for Before;
	// reverse for After (see interceptor.go).
	interceptors []Interceptor
	// secretPass is the session-scoped passphrase cache for the v0.3-3
	// secrets engine. nil before the first `secret` command; zeroed on
	// `secret lock`, on Close, and on Open-vault failure. NEVER logged.
	// We cache the passphrase rather than the derived key because each
	// vault Open re-derives anyway; this keeps the long-lived in-memory
	// secret surface minimal.
	secretPass []byte
	// secretCostShown is the one-shot flag for printing the KDF cost
	// description on first vault initialization. Per the threat-model
	// requirement to make Argon2id costs visible to the user.
	secretCostShown bool
	// secretKDFOverride is non-nil only in tests; production uses
	// secrets.DefaultKDFParams. See SetSecretKDFParamsForTesting.
	secretKDFOverride *secrets.KDFParams
	// secretClipFn is the clipboard sink; nil means use the real OS
	// clipboard via secrets.CopyToClipboard. Tests inject a capturing
	// stub via SetClipboardFnForTesting.
	secretClipFn func([]byte) error
	// loginMode is true when aish was invoked as a login shell — via
	// `-l`, `--login`, or `argv[0][0] == '-'`. Controls RC sourcing
	// (login.go) and `logout` semantics (builtin_logout.go). Set by
	// NewWithOptions; default false for backward-compatible New().
	loginMode bool
	// versionString is the build-time version (`main.version`) passed
	// in via Options.Version. Sourced into $AISH_VERSION in login mode
	// (login.go applyLoginEnvDefaults). Empty when caller didn't
	// supply it — that's harmless, the env var simply isn't set.
	versionString string
	// aliases is the v0.3-1 RC `[aliases]` table. Populated from
	// /etc/aish/aishrc and ~/.aish/aishrc.toml on login. NOT applied
	// to dispatch in this PR — the table is parsed and stored so the
	// follow-up that adds the `alias` built-in (#87) doesn't require
	// an RC format change. Nil until first alias landed.
	aliases map[string]string
	// execFn is the injection point for `exec <cmd>`'s syscall.Exec
	// call. Production code leaves this nil and the built-in calls
	// the real syscall.Exec. Tests inject a capturing stub so they
	// can assert the resolved binary path + argv WITHOUT actually
	// replacing the test process.
	execFn func(argv0 string, argv []string, envv []string) error
}

// Options configures a Shell at construction time. Zero value means
// "the historical New() behavior" — non-login, no version string.
//
// Options is part of the v0.3-1 login-shell work: every existing
// caller of New() keeps its semantics; the new login-shell path
// uses NewWithOptions explicitly.
type Options struct {
	// Login marks this shell as a login shell. When true,
	// NewWithOptions sources /etc/aish/aishrc and
	// ~/.aish/aishrc.toml before opening the cache / history /
	// telemetry seams, applies $AISH_VERSION, and gives $PATH a
	// POSIX default if unset.
	Login bool
	// Version is the build-time version string (main.version)
	// to surface as $AISH_VERSION inside login sessions. Ignored
	// when Login is false.
	Version string
	// Stderr is where RC parse-failure warnings are written.
	// nil means os.Stderr (production default). Tests inject a
	// bytes.Buffer to assert the warning text.
	Stderr io.Writer
}

// New returns a Shell configured as a non-login interactive session.
// Equivalent to NewWithOptions(Options{}). Kept as the canonical
// constructor for backward compatibility with every existing caller
// (cmd/aish, tests, integration harness). The login-shell path is
// reached only via NewWithOptions.
func New() *Shell {
	return NewWithOptions(Options{})
}

// NewWithOptions is the v0.3-1 login-aware constructor. When
// opts.Login is true, the constructor sources /etc/aish/aishrc and
// $HOME/.aish/aishrc.toml *before* opening the cache / history /
// telemetry seams (so RC-set env vars like ANTHROPIC_API_KEY reach
// the plugin spawn), then applies the POSIX login defaults
// ($AISH_VERSION, $PATH).
//
// Non-login behavior matches the historical New(): no RC sourcing,
// no login env defaults. Existing call sites should keep calling
// New(); only cmd/aish's flag-parsing layer reaches for
// NewWithOptions today.
//
// cwd / env seeding mirrors New(): os.Getwd with `/` fallback,
// env.FromSlice(os.Environ()). Theme registry + cache + history +
// telemetry + community + persona openers fire in the same order
// as New().
func NewWithOptions(opts Options) *Shell {
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
		cwd:           cwd,
		env:           e,
		themes:        reg,
		loginMode:     opts.Login,
		versionString: opts.Version,
	}

	// v0.3-1 login-shell wiring: source RC files BEFORE opening the
	// cache / history / telemetry seams so that any env vars the
	// RC sets (notably $ANTHROPIC_API_KEY / $CS_API_KEY) reach the
	// plugin-spawn path inside openCache. Non-login sessions skip
	// this block entirely.
	if opts.Login {
		stderr := opts.Stderr
		if stderr == nil {
			stderr = os.Stderr
		}
		s.loadRCFiles(stderr)
		s.applyLoginEnvDefaults()
	}

	// Open the L1 intent cache at ~/.aish/cache.db and (when a bearer
	// key is set) eagerly start the inference plugin as a child. Any
	// failure is logged-by-omission — the shell still works without a
	// cache, just without the AI-native dispatch tier.
	s.openCache(e)

	// Open the v0.1-4 history engine (event log + snapshotter). On
	// failure, s.history stays nil and `undo` / `restore` will print
	// "history not available" — the shell keeps running.
	s.openHistory(e)

	// Open the v0.1-5 telemetry recorder. On failure, s.telemetry
	// stays nil and `aish stats` will print "telemetry not
	// available" — the shell keeps running.
	s.openTelemetry(e)

	// Open the v0.2-3 community-cache bundle. On failure (no
	// bundle on disk, verification failure, etc.) s.community stays
	// nil and `aish community info` prints "not loaded" — the
	// shell keeps running with just the v0.1-2 L1 cache.
	s.openCommunity(os.Stderr)

	// Open the v0.3-5 persona registry (bundled set + any user
	// overrides under ~/.aish/personas/) and restore the persisted
	// active persona from ~/.aish/config.toml. On total failure
	// s.personas is nil and the `persona` built-in reports
	// "registry not available" — the shell keeps running, the
	// inference path falls back to no system-prompt injection.
	s.openPersona(e)

	return s
}

// Close releases shell-owned resources (cache DB, plugin child process,
// history DB). Idempotent. Safe to call on a freshly-constructed Shell
// that never successfully opened a cache or history.
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
	if s.history != nil {
		if err := s.history.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.history = nil
	}
	if s.telemetry != nil {
		if err := s.telemetry.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.telemetry = nil
	}
	if s.community != nil {
		if err := s.community.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.community = nil
	}
	s.interceptors = nil
	// Wipe any cached passphrase before the Shell falls out of scope.
	// secretLock is idempotent; safe even when secretPass is already nil.
	s.secretLock()
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
	// v0.3-5 persona seam: cache consults this on every Infer to
	// inject the active persona's safety-floor + voice prompt. The
	// closure reads s.activePersona LIVE so `persona set` mid-session
	// takes effect on the next intent.
	s.cache.WithSystemPromptSource(func() string {
		if s.personas == nil {
			return ""
		}
		return s.personaSystemPromptForInfer()
	})
}

// openHistory opens ~/.aish/history.db (creating the directory if
// needed) and wires the history.History interceptor onto the Shell's
// interceptor slice. On any failure the shell falls back to no-history
// mode — `undo` / `restore` will print "history not available" and
// every destructive command runs unobserved (POSIX-default behavior).
func (s *Shell) openHistory(e *env.Env) {
	home := homeDir(e)
	if home == "" {
		return
	}
	dotAish := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		return
	}
	store, err := history.Open(filepath.Join(dotAish, "history.db"))
	if err != nil {
		return
	}
	// v0.3-4: attach the per-install signer so every Append /
	// Checkpoint event carries a verifiable Ed25519 signature.
	// Failures here are non-fatal — the store keeps working
	// unsigned, the rest of the engine still functions. The
	// degradation matches the rest of openHistory's posture.
	if signer, signErr := history.NewFileSigner(history.DefaultKeyPath(dotAish)); signErr == nil {
		store.WithSigner(signer)
	}
	cfg := history.LoadConfig(dotAish)
	snapRoot := filepath.Join(dotAish, "snapshots")
	sn := history.NewSnapshotter(snapRoot, cfg.SnapshotMaxBytes, history.DefaultIgnoreMatcher())
	h := history.NewHistory(store, sn)
	if h == nil {
		_ = store.Close()
		return
	}
	h.SetCwdFn(func() string { return s.cwd })
	s.history = h
	s.interceptors = append(s.interceptors, h)
}

// openTelemetry constructs the v0.1-5 telemetry.Recorder and
// registers it on the interceptor slice. Like openHistory, this is
// best-effort: any failure (HOME missing, mkdir denied, recorder
// constructor failure) leaves s.telemetry nil and the `aish stats`
// built-in returns "telemetry not available."
//
// The recorder reads cache hit/miss deltas via a thin adapter
// (cacheStatsAdapter) so telemetry never imports the cache package
// directly — the dependency stays one-way.
func (s *Shell) openTelemetry(e *env.Env) {
	home := homeDir(e)
	if home == "" {
		return
	}
	dotAish := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		return
	}
	cfg := telemetry.Config{
		DotAishDir: dotAish,
	}
	if s.cacheStore != nil {
		cfg.CacheReader = &cacheStatsAdapter{store: s.cacheStore}
	}
	if s.cachePlugin != nil {
		// Capture once: the plugin's lifetime equals the shell's.
		cfg.PluginActive = func() bool { return true }
	} else {
		cfg.PluginActive = func() bool { return false }
	}
	rec, err := telemetry.New(cfg)
	if err != nil {
		return
	}
	s.telemetry = rec
	s.interceptors = append(s.interceptors, rec)
}

// cacheStatsAdapter satisfies telemetry.CacheStatsReader against a
// *cache.Store, unwrapping the cache.Stats struct so the telemetry
// package doesn't need to know about it.
type cacheStatsAdapter struct {
	store *cache.Store
}

func (a *cacheStatsAdapter) StatsSnapshot() (int64, int64, error) {
	st, err := a.store.Stats()
	if err != nil {
		return 0, 0, err
	}
	return st.Hits, st.Misses, nil
}

// tryStartPlugin spawns the inference plugin when a bearer key is set
// and the binary resolves on PATH (or via $AISH_INFERENCE_PLUGIN, or
// via the v0.3-2 plugin registry under ~/.aish/plugins/). Returns nil
// on any startup failure; the cache then runs in lookup-only mode.
//
// Resolution order for the binary:
//  1. $AISH_INFERENCE_PLUGIN — explicit per-session override.
//  2. The v0.3-2 plugin registry: first inference-kind plugin found.
//  3. $PATH lookup for DefaultPluginBinary — pre-v0.3-2 fallback.
//
// The plugin defaults to api.convergent-systems.co/llm/v1 (set on the
// plugin side via DefaultBaseURL). $ANTHROPIC_BASE_URL overrides.
func tryStartPlugin(e *env.Env) *cache.PluginClient {
	// Avoid spawning a child that will exit 2 immediately because the
	// API key isn't set. The plugin reads $ANTHROPIC_API_KEY (legacy)
	// or $CS_API_KEY (current).
	keyAvailable := false
	if k, _ := e.Get("ANTHROPIC_API_KEY"); k != "" {
		keyAvailable = true
	}
	if k, _ := e.Get("CS_API_KEY"); k != "" {
		keyAvailable = true
	}
	if !keyAvailable {
		return nil
	}
	binary := ""
	if v, ok := e.Get("AISH_INFERENCE_PLUGIN"); ok {
		binary = v
	}
	// Consult the v0.3-2 plugin registry when the env-var override is
	// unset. An empty registry yields "", which falls through to the
	// PATH lookup inside cache.Start.
	if binary == "" {
		if home := homeDir(e); home != "" {
			dotAish := filepath.Join(home, ".aish")
			binary = selectRegistryInferencePlugin(dotAish, os.Stderr)
		}
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
	if term.IsTTY(stdin) {
		// stdin is a real terminal — dispatch to the v0.2-1 line editor.
		// Falls through to the byte-by-byte path on any setup failure so
		// the user never gets stuck without a prompt.
		if err := s.runTTY(stdin, stdout, stderr); err == nil {
			return nil
		}
		// fallthrough: best-effort.
	}
	return s.runStream(stdin, stdout, stderr)
}

// runStream is the pre-v0.2-1 byte-by-byte REPL. Used unchanged when
// stdin is a script / pipe / non-TTY reader. This is the path the
// issue-#167 regression seatbelt (TestCatConsumesPipedStdin) covers —
// touching it requires a separate plan.
func (s *Shell) runStream(stdin io.Reader, stdout, stderr io.Writer) error {
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
				// v0.3-1 sentinels: `logout` and (Windows) `exec`
				// propagate a typed error to unwind the REPL cleanly.
				// Surface them up so main() can exit with the right
				// status; the REPL itself returns nil so callers see
				// "shell terminated normally."
				if _, ok := IsLogout(dispatchErr); ok {
					return dispatchErr
				}
				if _, ok := IsExecReplaced(dispatchErr); ok {
					return dispatchErr
				}
				// Anything else is an unrecoverable I/O error on the
				// caller's streams.
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

// runTTY drives the v0.2-1 interactive line editor.
//
// Invariant: this function is only entered when term.IsTTY(stdin) was
// true. The editor allocates a RawTerminal on stdin; failure to enter
// raw mode causes the caller (Run) to fall through to the byte-by-byte
// path.
//
// The editor's history source is a session-local MemorySource — disk
// history.Store isn't wired in v0.2-1 (it lives in a partition-locked
// package; see the plan's "Backward compatibility" section).
func (s *Shell) runTTY(stdin io.Reader, stdout, stderr io.Writer) error {
	f, ok := stdin.(*os.File)
	if !ok {
		return errors.New("shell: TTY editor requires *os.File stdin")
	}
	rt, err := term.NewRawTerminal(f)
	if err != nil {
		return err
	}
	history := term.NewMemorySource(nil)
	// Build the completer once; cwd + PATH are read live each ReadLine
	// so a `cd` between prompts is reflected on the next Tab.
	editor := term.NewEditor(term.Config{
		Stdin:     stdin,
		Stdout:    stdout,
		Prompt:    s.Prompt,
		History:   history,
		Resolver:  s,
		Completer: s.newCompleter(),
		RawTerm:   rt,
	})
	for {
		line, err := editor.ReadLine(context.Background())
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, term.ErrInterrupt) {
			s.SetLastExit(130)
			continue
		}
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
		// Re-bind the editor's completer if the cwd has changed — the
		// completer captures cwd at construction.
		editor = term.NewEditor(term.Config{
			Stdin:     stdin,
			Stdout:    stdout,
			Prompt:    s.Prompt,
			History:   history,
			Resolver:  s,
			Completer: s.newCompleter(),
			RawTerm:   rt,
		})
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		history.Append(trimmed)
		if dispatchErr := s.dispatch(trimmed, stdin, stdout, stderr); dispatchErr != nil {
			// v0.3-1 sentinels mirror runStream: surface them so
			// main() inspects the exit code, but the REPL itself
			// terminates normally.
			return dispatchErr
		}
	}
}

// newCompleter builds the production tab-completer for the current
// shell state: aish built-ins + $PATH binaries + filesystem paths
// rooted at the shell's cwd. Read lazily so a `cd` in the REPL
// updates the next ReadLine's completer.
func (s *Shell) newCompleter() term.Completer {
	pathDirs := []string{}
	if p, ok := s.env.Get("PATH"); ok {
		for _, d := range filepath.SplitList(p) {
			if d == "" {
				continue
			}
			pathDirs = append(pathDirs, d)
		}
	}
	return term.NewDefaultCompleter(s.cwd, pathDirs)
}

// ResolveTier classifies the first token of an input line for the
// term package's syntax highlighter. Matches dispatch's decision tree:
// built-in names win, then known-binary lookup, then AI-intent.
//
// This satisfies term.TierResolver without making the term package
// depend on shell's dispatch internals.
func (s *Shell) ResolveTier(firstToken string) term.Tier {
	switch firstToken {
	case "cd", "export", "theme", "cache", "community", "plugin", "stats", "undo", "restore",
		"run", "explain", "migrate", "persona", "secret", "identity",
		"logout", "exec", "history",
		"install", "service", "process", "env", "network":
		return term.TierBuiltin
	}
	if isKnownBinary(firstToken, s.env) {
		return term.TierKnownBinary
	}
	return term.TierAIIntent
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

	// Built-in: `community info | status | install | refresh |
	// contribute`. Per v0.2-3 acceptance (#58–#63).
	if line == "community" || strings.HasPrefix(line, "community ") || strings.HasPrefix(line, "community\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "community"))
		args := strings.Fields(rest)
		s.SetLastExit(s.communityBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `plugin list | install <path> | remove <name> |
	// verify <name> | status`. Per v0.3-2 acceptance (#89–#94).
	if line == "plugin" || strings.HasPrefix(line, "plugin ") || strings.HasPrefix(line, "plugin\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "plugin"))
		args := strings.Fields(rest)
		s.SetLastExit(s.pluginBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `stats [N]`. Per v0.1-5 task #43 — local dashboard.
	if line == "stats" || strings.HasPrefix(line, "stats ") || strings.HasPrefix(line, "stats\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "stats"))
		args := strings.Fields(rest)
		s.SetLastExit(s.statsBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `undo` and `restore <path>`. Per v0.1-4 acceptance
	// (#35, #36). These are intentionally bare-word built-ins (not
	// prefixed with `aish`) so the viral demo reads as one keystroke.
	if line == "undo" || strings.HasPrefix(line, "undo ") || strings.HasPrefix(line, "undo\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "undo"))
		args := strings.Fields(rest)
		s.SetLastExit(s.undoBuiltin(args, stdout, stderr))
		return nil
	}
	if strings.HasPrefix(line, "restore ") || strings.HasPrefix(line, "restore\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "restore"))
		args := strings.Fields(rest)
		s.SetLastExit(s.restoreBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `history list | show | search | purge | checkpoint |
	// rollback`. Per v0.3-4 acceptance (#108–#113).
	if line == "history" || strings.HasPrefix(line, "history ") || strings.HasPrefix(line, "history\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "history"))
		args := strings.Fields(rest)
		s.SetLastExit(s.historyBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `run <script>` — v0.2-4. Parse + execute a bash/zsh/
	// fish script through the existing dispatch tier. Each invocation
	// builds a fresh env copy so in-script assignments don't leak.
	if line == "run" || strings.HasPrefix(line, "run ") || strings.HasPrefix(line, "run\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "run"))
		args := strings.Fields(rest)
		s.SetLastExit(s.runScriptBuiltin(args, stdin, stdout, stderr))
		return nil
	}

	// Built-in: `explain [--with-llm] <script>` — v0.2-4. Deterministic
	// numbered description by default; optional LLM enrichment when
	// --with-llm is passed AND an API key is available.
	if line == "explain" || strings.HasPrefix(line, "explain ") || strings.HasPrefix(line, "explain\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "explain"))
		args := strings.Fields(rest)
		s.SetLastExit(s.explainScriptBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `migrate <script>` — v0.2-4. AST → aish-native script.
	// Rule-based (no LLM) so output is reproducible.
	if line == "migrate" || strings.HasPrefix(line, "migrate ") || strings.HasPrefix(line, "migrate\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "migrate"))
		args := strings.Fields(rest)
		s.SetLastExit(s.migrateScriptBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `install <pkg>` — v1.0-2 task #137. Windows-only at
	// runtime (delegates to winget); non-Windows hosts surface a
	// polite "not supported".
	if line == "install" || strings.HasPrefix(line, "install ") || strings.HasPrefix(line, "install\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "install"))
		args := strings.Fields(rest)
		s.SetLastExit(s.installBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `service <list|status|start|stop>` — v1.0-2 task #138.
	if line == "service" || strings.HasPrefix(line, "service ") || strings.HasPrefix(line, "service\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "service"))
		args := strings.Fields(rest)
		s.SetLastExit(s.serviceBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `process <list|kill>` — v1.0-2 task #139.
	if line == "process" || strings.HasPrefix(line, "process ") || strings.HasPrefix(line, "process\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "process"))
		args := strings.Fields(rest)
		s.SetLastExit(s.processBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `env <list|get|set|unset>` — v1.0-2 task #140.
	if line == "env" || strings.HasPrefix(line, "env ") || strings.HasPrefix(line, "env\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "env"))
		args := strings.Fields(rest)
		s.SetLastExit(s.envBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `network <interfaces|routes>` — v1.0-2 task #141.
	if line == "network" || strings.HasPrefix(line, "network ") || strings.HasPrefix(line, "network\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "network"))
		args := strings.Fields(rest)
		s.SetLastExit(s.networkBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `persona list | show <name> | set <name> | use <name>
	// | active`. Per v0.3-5 acceptance (#114–#129).
	if line == "persona" || strings.HasPrefix(line, "persona ") || strings.HasPrefix(line, "persona\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "persona"))
		args := strings.Fields(rest)
		s.SetLastExit(s.personaBuiltin(args, stdout, stderr))
		return nil
	}

	// Built-in: `secret <set|get|list|rm|lock|help>` — v0.3-3 task #97.
	// Stdin is the value source for `set` and the passphrase source on
	// first call of the session. NEVER echoes a value to stdout/stderr.
	if line == "secret" || strings.HasPrefix(line, "secret ") || strings.HasPrefix(line, "secret\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "secret"))
		args := strings.Fields(rest)
		s.SetLastExit(s.secretBuiltin(args, stdin, stdout, stderr))
		return nil
	}

	// Built-in: `identity <use|list|show|create|help>` — v0.3-3 task
	// #103. Operates on ~/.aish/identity.toml + ~/.aish/identities/.
	if line == "identity" || strings.HasPrefix(line, "identity ") || strings.HasPrefix(line, "identity\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "identity"))
		args := strings.Fields(rest)
		s.SetLastExit(s.identityBuiltin(args, stdin, stdout, stderr))
		return nil
	}

	// Built-in: `logout [n]` — v0.3-1 task #87 subset. In login mode,
	// terminates the REPL cleanly (sentinel propagates up to runStream
	// / runTTY, which return nil). In non-login mode, prints an error
	// and returns 1 (matches bash). The sentinel is the ONLY case
	// where dispatch returns non-nil for a built-in.
	if line == "logout" || strings.HasPrefix(line, "logout ") || strings.HasPrefix(line, "logout\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "logout"))
		args := strings.Fields(rest)
		if err := s.logoutBuiltin(args, stderr); err != nil {
			return err
		}
		return nil
	}

	// Built-in: `exec <cmd>` — v0.3-1 task #87 subset. On POSIX,
	// syscall.Exec replaces the current process and never returns
	// on success. Bare `exec` is a no-op. Resolution failure is
	// fatal (exit 127) — bash semantics for login shells.
	if line == "exec" || strings.HasPrefix(line, "exec ") || strings.HasPrefix(line, "exec\t") {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "exec"))
		args, parseErr := parseExecLine(rest)
		if parseErr != nil {
			fmt.Fprintf(stderr, "aish: exec: %v\n", parseErr)
			s.SetLastExit(1)
			return nil
		}
		if err := s.execBuiltin(args, stdout, stderr); err != nil {
			return err
		}
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
	//
	// v0.3-5 persona seam: the cache key is the bare user intent
	// (persona-agnostic — `delete log files` resolves the same way
	// regardless of who the shell is being for). The persona's system
	// prompt is injected by cache.Cache.WithSystemPromptSource ahead
	// of any Infer call; see openCache for the wiring and
	// .artifacts/plans/v0.3-5.md for the proto-extension deferral.
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
//
// Interceptor seam (v0.1-4): each registered Interceptor.Before fires
// just before exec.Run, in registration order. After fires immediately
// post-exec in REVERSE order so the last-registered observer sees the
// state produced by earlier observers. A Before error is logged to
// stderr but does NOT abort the command — "snapshot is best-effort,
// command is mandatory."
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
	for _, ic := range s.interceptors {
		if err := ic.Before(&pipeline, cmdline); err != nil {
			fmt.Fprintf(stderr, "aish: interceptor: %v\n", err)
		}
	}
	start := time.Now()
	// v0.2-2 dispatch seam: when the pipeline is a single command,
	// the parent stdin/stdout are *os.File handles AND the parent
	// stdin is a real TTY AND the command is on the curated
	// interactive list (vim, less, top, htop, ssh, az, …), route
	// through exec.RunPTY so the child sees a real controlling
	// terminal. Every other case (pipelines, scripted stdin, non-
	// interactive commands) stays on the existing stdio path.
	exitCode, runErr := s.runPipeline(pipeline, stdin, stdout, stderr)
	dur := time.Since(start)
	finalExit := exitCode
	if runErr != nil {
		fmt.Fprintf(stderr, "aish: %v\n", runErr)
		finalExit = 127
	}
	// Reverse order — see contract on Interceptor.
	for i := len(s.interceptors) - 1; i >= 0; i-- {
		s.interceptors[i].After(&pipeline, cmdline, finalExit, dur)
	}
	s.SetLastExit(finalExit)
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
