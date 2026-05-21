package shell

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// aliasBuiltin implements POSIX `alias [NAME[=COMMAND]] ...` —
// v0.3-1 follow-up task #87.
//
//	bare `alias`               — list every registered alias (sorted)
//	                             in `alias NAME='COMMAND'` form.
//	`alias NAME`               — print just that alias, exit 0;
//	                             exit 1 + stderr if NAME is unbound.
//	`alias NAME=COMMAND`       — register NAME → COMMAND in the live
//	                             session.
//
// Aliases declared in the RC `[aliases]` table are seeded into
// s.aliases at login (login.go applyRCFile). This built-in writes
// the live map; persistence to RC is deferred — the user must edit
// the RC file directly if they want the alias to survive restart.
// (Bash's behavior matches: `alias` without `-p` doesn't persist.)
//
// Recursive aliases (`alias x=y; alias y=x`) are bounded by the
// dispatcher's expansion cap (see resolveAlias in shell.go).
func (s *Shell) aliasBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		s.aliasList(stdout)
		return 0
	}
	exit := 0
	for _, arg := range args {
		name, value, hasEq := strings.Cut(arg, "=")
		if !hasEq {
			// Lookup form: `alias NAME`.
			if v, ok := s.aliasGet(name); ok {
				fmt.Fprintf(stdout, "alias %s=%s\n", name, quoteAliasValue(v))
			} else {
				fmt.Fprintf(stderr, "aish: alias: %s: not found\n", name)
				exit = 1
			}
			continue
		}
		if name == "" {
			fmt.Fprintf(stderr, "aish: alias: missing name in %q\n", arg)
			exit = 2
			continue
		}
		// Strip optional surrounding quotes on the value so
		// `alias ll='ls -la'` and `alias ll="ls -la"` both store `ls -la`.
		value = stripOuterQuotes(value)
		s.aliasSet(name, value)
	}
	return exit
}

// aliasList writes every alias to stdout in sorted order. Format
// matches bash: `alias NAME='VALUE'` per line (single-quoted to make
// round-trip-safe paste output).
func (s *Shell) aliasList(stdout io.Writer) {
	if len(s.aliases) == 0 {
		return
	}
	names := make([]string, 0, len(s.aliases))
	for k := range s.aliases {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(stdout, "alias %s=%s\n", n, quoteAliasValue(s.aliases[n]))
	}
}

// aliasGet returns the value of name, if bound.
func (s *Shell) aliasGet(name string) (string, bool) {
	if s.aliases == nil {
		return "", false
	}
	v, ok := s.aliases[name]
	return v, ok
}

// aliasSet binds name=value in the live session.
func (s *Shell) aliasSet(name, value string) {
	if s.aliases == nil {
		s.aliases = make(map[string]string)
	}
	s.aliases[name] = value
}

// quoteAliasValue wraps v in single quotes (bash convention for
// `alias`-listing output). Any embedded `'` is rewritten as `'\”`
// — the canonical bash-safe escape so output can be re-fed to the
// shell.
func quoteAliasValue(v string) string {
	if !strings.Contains(v, "'") {
		return "'" + v + "'"
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// resolveAlias expands the first token of a command line through the
// live alias table. Returns the rewritten line (or input unchanged
// when nothing matched).
//
// Bounded at 16 iterations so `alias x=y; alias y=x` cannot loop.
// When the cap is hit, we leave the line in its last-rewritten state
// and write a warning to stderr — the user gets feedback and the
// command still attempts to run.
func (s *Shell) resolveAlias(line string, stderr io.Writer) string {
	if len(s.aliases) == 0 || line == "" {
		return line
	}
	const cap = 16
	seen := make(map[string]bool, 4)
	for i := 0; i < cap; i++ {
		first := firstToken(line)
		if first == "" {
			return line
		}
		value, ok := s.aliasGet(first)
		if !ok {
			return line
		}
		if seen[first] {
			// Cycle — stop here. The current line has the most
			// recent rewrite applied; bash silently breaks the cycle
			// after one pass, we emit a warning to be honest.
			fmt.Fprintf(stderr, "aish: alias: cycle detected at %q — stopping\n", first)
			return line
		}
		seen[first] = true
		// Replace the first token with the alias value, preserving
		// the remainder of the line.
		rest := strings.TrimPrefix(line, first)
		line = value + rest
	}
	fmt.Fprintf(stderr, "aish: alias: expansion exceeded %d iterations — stopping\n", cap)
	return line
}
