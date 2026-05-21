package shell

import (
	"fmt"
	"io"
)

// unsetBuiltin implements POSIX `unset NAME [NAME ...]` — v0.3-1
// follow-up task #87.
//
// Removes each NAME from the session env. Unsetting an unbound name is
// a no-op (matches bash). Empty / missing args print usage and return
// 2. Each name failure is independent — we accumulate the worst exit
// code rather than bailing on the first miss.
//
// Out of scope: `unset -f` (functions) and `unset -v` (explicit
// variable mode). aish has no shell functions yet, so the default
// "unset variable" behavior is all that applies.
func (s *Shell) unsetBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aish: unset: usage: unset NAME [NAME ...]")
		return 2
	}
	exit := 0
	for _, name := range args {
		if name == "" {
			fmt.Fprintln(stderr, "aish: unset: empty name")
			exit = 2
			continue
		}
		s.env.Unset(name)
	}
	return exit
}
