package shell

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// sourceBuiltin implements `source <file>` — v0.3-1 follow-up task #87.
//
// aish RC files are TOML (per v0.3-1 #190), so `source` accepts two
// formats:
//
//  1. TOML aishrc — same schema as the login RC. Honored sections:
//     `[env]` (each k/v applied to the live env), `[aliases]` (each
//     k/v added to s.aliases). `[shell].umask` is intentionally NOT
//     applied here — interactive sourcing should not silently change
//     the process umask. (The login-time RC pass IS where umask
//     belongs; we'd surprise users who type `source ~/.aish/aishrc.toml`
//     in mid-session.)
//
//  2. POSIX-ish env lines — when the file does NOT parse as TOML, we
//     fall back to reading line-by-line and accepting `K=V` per line
//     (matching the dominant ".env" convention). Blank lines and lines
//     starting with `#` are comments. Lines beginning with `export `
//     are tolerated (the `export ` prefix is stripped). Anything else
//     surfaces as a per-line warning on stderr — the file isn't
//     rejected wholesale.
//
// Errors:
//   - missing arg              → exit 2, usage on stderr.
//   - file doesn't exist       → exit 1, stderr message.
//   - file read failure        → exit 1, stderr message.
//   - TOML parse + non-env line → warnings, but exit 0 if anything was
//     applied; otherwise exit 1.
func (s *Shell) sourceBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aish: source: usage: source <file>")
		return 2
	}
	path := args[0]
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "aish: source: %s: no such file\n", path)
		} else {
			fmt.Fprintf(stderr, "aish: source: %s: %v\n", path, err)
		}
		return 1
	}
	// Try TOML first — that's the native aish RC format.
	var rc rcFile
	if _, tomlErr := toml.Decode(string(raw), &rc); tomlErr == nil && rcLooksTOML(raw) {
		applied := s.applySourcedRC(&rc, stderr, path)
		if applied {
			return 0
		}
		// Empty TOML file (no [env], no [aliases]) — degrade to env-line
		// parsing in case the file is actually a `.env`.
	}
	// Fallback: POSIX-ish K=V env lines.
	return s.applySourcedEnvLines(raw, stderr, path)
}

// rcLooksTOML is a cheap heuristic: a file with no `=`-bearing line
// that begins with `[` or `K = V` styled TOML is probably an empty
// or fully commented-out file. We only commit to the TOML path when
// the bytes contain at least one square-bracketed header — otherwise
// we fall through to env-line parsing so `.env` files with no
// section header still work.
func rcLooksTOML(raw []byte) bool {
	return strings.Contains(string(raw), "[")
}

// applySourcedRC walks a parsed rcFile, applying its env + aliases
// to the live shell. Returns true when anything was applied.
func (s *Shell) applySourcedRC(rc *rcFile, stderr io.Writer, path string) bool {
	applied := false
	for k, v := range rc.Env {
		if err := s.env.Set(k, v); err != nil {
			fmt.Fprintf(stderr, "aish: source: %s: env[%s]: %v\n", path, k, err)
			continue
		}
		applied = true
	}
	for k, v := range rc.Aliases {
		s.aliasSet(k, v)
		applied = true
	}
	if rc.Shell.Umask != "" {
		fmt.Fprintf(stderr, "aish: source: %s: [shell] umask ignored (umask is login-time only)\n", path)
	}
	return applied
}

// applySourcedEnvLines parses raw bytes as POSIX-ish K=V env lines.
// Empty lines and `#` comments are skipped. A leading `export ` is
// tolerated and stripped. Malformed lines emit a per-line warning but
// don't abort. Returns the exit code (0 on any successful binding, 1
// when no lines applied).
func (s *Shell) applySourcedEnvLines(raw []byte, stderr io.Writer, path string) int {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	applied := 0
	lineno := 0
	for scanner.Scan() {
		lineno++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// `export FOO=bar` — strip the `export ` prefix so the same
		// `.env` files bash users have keep working.
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			fmt.Fprintf(stderr, "aish: source: %s:%d: skipping malformed line\n", path, lineno)
			continue
		}
		value = stripOuterQuotes(strings.TrimSpace(value))
		name = strings.TrimSpace(name)
		if err := s.env.Set(name, value); err != nil {
			fmt.Fprintf(stderr, "aish: source: %s:%d: %v\n", path, lineno, err)
			continue
		}
		applied++
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "aish: source: %s: read: %v\n", path, err)
		return 1
	}
	if applied == 0 {
		return 1
	}
	return 0
}
