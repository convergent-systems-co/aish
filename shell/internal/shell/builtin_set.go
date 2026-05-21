package shell

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// setBuiltin implements POSIX `set [NAME=VALUE ...]` — v0.3-1
// follow-up task #87.
//
//	bare `set`              — list every NAME=VALUE in the session
//	                          env (sorted) per POSIX "list variables".
//	`set NAME=VALUE [...]`  — bind each NAME=VALUE in the local env.
//	                          UNLIKE `export`, no mark-for-child-env
//	                          flag is applied (aish currently passes
//	                          the full env table to every child, so
//	                          the distinction is informational; it
//	                          becomes load-bearing when we add an
//	                          export-mask in a later epic).
//
// Option flags (`set -e`, `set -o`, `set -u`, `set -x`, …) are
// DELIBERATELY out of scope for this PR — they need broader runtime
// integration (e.g. `set -e` interacts with every dispatch arm).
// Invoking `set -*` prints a "not yet supported" warning + exits 2
// so we surface the gap rather than silently no-oping.
func (s *Shell) setBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		entries := s.env.Environ()
		sort.Strings(entries)
		for _, e := range entries {
			fmt.Fprintln(stdout, e)
		}
		return 0
	}
	// Reject shell-option flags up front so callers learn the gap
	// (Code.md P3 — Surface, Don't Bury).
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			fmt.Fprintf(stderr, "aish: set: option flags not supported in this build (%q)\n", a)
			return 2
		}
	}
	exit := 0
	for _, arg := range args {
		name, value, ok := strings.Cut(arg, "=")
		if !ok {
			fmt.Fprintf(stderr, "aish: set: missing `=` in %q\n", arg)
			exit = 2
			continue
		}
		if name == "" {
			fmt.Fprintf(stderr, "aish: set: empty name in %q\n", arg)
			exit = 2
			continue
		}
		value = stripOuterQuotes(value)
		if err := s.env.Set(name, value); err != nil {
			fmt.Fprintf(stderr, "aish: set: %v\n", err)
			exit = 1
		}
	}
	return exit
}
