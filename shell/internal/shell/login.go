// Package-internal: login-shell capabilities for v0.3-1.
//
// A login shell is the first process a user lands in when they log
// into a system (via getty, sshd, login(8), GDM, …). POSIX shells
// adopt three conventions to detect "I am a login shell":
//
//  1. argv[0] begins with a `-` (e.g. `-bash`, `-aish`).
//     login(8) and sshd invoke shells this way.
//  2. The `-l` flag is present in argv.
//  3. The `--login` flag is present in argv.
//
// Detection happens once in cmd/aish/main.go before the Shell is
// constructed; the result lives on Shell.loginMode.
//
// When loginMode == true, NewWithOptions sources two RC files in
// order *before* opening the cache / history / telemetry seams (so
// RC-set env vars like ANTHROPIC_API_KEY reach the plugin spawn):
//
//  1. /etc/aish/aishrc        — system-wide
//  2. $HOME/.aish/aishrc.toml — per-user
//
// The user file overrides the system file on key collisions. A
// missing file is silently skipped — RC failure must not deny
// login.
//
// Non-login mode does NOT source these files. Interactive
// non-login shells (the default for the v0.1 demo) source a
// different RC, which is deferred to a follow-up.
package shell

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/BurntSushi/toml"
)

// systemRCPath is the canonical system-wide RC file location.
// Linux distributions and Homebrew packagers can drop site-wide
// defaults here without touching $HOME. Matches the placement of
// /etc/bash.bashrc and /etc/zshrc.
const systemRCPath = "/etc/aish/aishrc"

// userRCRelPath is appended to $HOME to locate the per-user RC.
// Lives under ~/.aish/ so it shares a directory with cache.db /
// history.db / config.toml.
const userRCRelPath = ".aish/aishrc.toml"

// defaultPOSIXPath is the bash-compatible PATH default applied when
// loginMode == true AND $PATH is unset after RC sourcing. Mirrors
// bash's behavior for a fresh login session.
const defaultPOSIXPath = "/usr/local/bin:/usr/bin:/bin"

// rcFile is the on-disk TOML schema for an aish RC file. All three
// tables are optional; an empty file is a valid no-op RC.
type rcFile struct {
	Env     map[string]string `toml:"env"`
	Shell   rcShell           `toml:"shell"`
	Aliases map[string]string `toml:"aliases"`
}

// rcShell holds shell-runtime options. v0.3-1 ships `umask`; future
// fields (history.size, prompt.symbol, …) live here without a
// schema break.
type rcShell struct {
	Umask string `toml:"umask"`
}

// loadRCFiles applies systemRCPath then $HOME/aishrc.toml to the
// Shell's env + alias table. Errors are written to stderr but never
// propagated — RC failure must not deny login. Order matters: the
// user file is applied second so any conflicting key wins for the
// user.
func (s *Shell) loadRCFiles(stderr io.Writer) {
	// 1. System-wide.
	if _, err := os.Stat(systemRCPath); err == nil {
		if rcErr := s.applyRCFile(systemRCPath, stderr); rcErr != nil {
			fmt.Fprintf(stderr, "aish: warning: %s: %v\n", systemRCPath, rcErr)
		}
	}
	// 2. Per-user.
	if home := homeDir(s.env); home != "" {
		userRC := filepath.Join(home, userRCRelPath)
		if _, err := os.Stat(userRC); err == nil {
			if rcErr := s.applyRCFile(userRC, stderr); rcErr != nil {
				fmt.Fprintf(stderr, "aish: warning: %s: %v\n", userRC, rcErr)
			}
		}
	}
}

// applyRCFile parses one TOML RC file and applies it to the Shell.
// The file MUST exist when this is called (loadRCFiles gates on
// os.Stat). Returns a non-nil error only on parse failure or an
// invalid umask string — both surfaced as a one-line stderr
// warning by the caller.
func (s *Shell) applyRCFile(path string, stderr io.Writer) error {
	var rc rcFile
	if _, err := toml.DecodeFile(path, &rc); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	// [env] — assign into the shell's env. An empty key is rejected by
	// env.Env.Set so we don't need to pre-check; an empty value is
	// allowed (POSIX: `export FOO=` is legitimate).
	for k, v := range rc.Env {
		if err := s.env.Set(k, v); err != nil {
			fmt.Fprintf(stderr, "aish: warning: %s: env[%s]: %v\n", path, k, err)
		}
	}
	// [shell] umask — parse as octal and apply via the platform hook.
	if rc.Shell.Umask != "" {
		mask, err := strconv.ParseInt(rc.Shell.Umask, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid umask %q: %w", rc.Shell.Umask, err)
		}
		applyUmask(int(mask))
	}
	// [aliases] — accepted and stored on the Shell, but NOT applied
	// to dispatch this PR. The follow-up (#87) will wire them into
	// the dispatcher; the on-disk format lands here today so users
	// don't have to migrate later.
	if len(rc.Aliases) > 0 {
		if s.aliases == nil {
			s.aliases = make(map[string]string, len(rc.Aliases))
		}
		for k, v := range rc.Aliases {
			s.aliases[k] = v
		}
	}
	return nil
}

// applyLoginEnvDefaults runs after RC sourcing in login mode. It
// sets two POSIX-mandated defaults that bash also applies:
//
//   - $AISH_VERSION is set (or overwritten) to the build-time version
//     string so children of the login shell can introspect aish.
//   - $PATH is given a sane default if still unset after RC.
//
// Called only when loginMode == true.
func (s *Shell) applyLoginEnvDefaults() {
	// AISH_VERSION: overwrite any inherited value so the version a
	// user sees in a login session always reflects the binary that
	// ran. Non-login sessions inherit whatever the parent set.
	if s.versionString != "" {
		_ = s.env.Set("AISH_VERSION", s.versionString)
	}
	// PATH default — only applied on Unix. On Windows, $PATH is
	// always set by the OS, and our default would be wrong anyway.
	if runtime.GOOS != "windows" {
		if p, ok := s.env.Get("PATH"); !ok || p == "" {
			_ = s.env.Set("PATH", defaultPOSIXPath)
		}
	}
}
