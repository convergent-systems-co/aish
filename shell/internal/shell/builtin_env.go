package shell

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// envBuiltin implements `aish env <list|get|set|unset> [name [value]]`
// — v1.0-2 task #140.
//
// MVP scope: in-process env only. We read/mutate the Shell's own
// env.Env so subsequent commands in the session see the changes,
// matching how `export NAME=VALUE` already works at the dispatch
// layer.
//
// Out of scope (deferred to v1.1):
//
//   - `--persist` flag writing to HKCU\Environment and broadcasting
//     WM_SETTINGCHANGE so a new process picks it up. Requires
//     SetEnvironmentVariable + SendMessageTimeout, both available
//     in golang.org/x/sys/windows but each is a per-mode discipline
//     worth its own PR.
//
// The built-in is host-agnostic — POSIX env reads work the same on
// Linux/macOS, so we don't gate behind GOOS. (On non-Windows hosts
// the absence of HKCU is moot; the in-process mutation behaves
// identically.)
func (s *Shell) envBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "list" {
		// `env` and `env list` are equivalent.
		entries := s.env.Environ()
		sort.Strings(entries)
		for _, e := range entries {
			fmt.Fprintln(stdout, e)
		}
		return 0
	}
	switch args[0] {
	case "help":
		fmt.Fprintln(stdout, "usage: env <list|get|set|unset> [name [value]]")
		fmt.Fprintln(stdout, "  list                — print every NAME=VALUE in the session env")
		fmt.Fprintln(stdout, "  get <name>          — print VALUE or empty + exit 1 if unset")
		fmt.Fprintln(stdout, "  set <name> <value>  — bind NAME=VALUE in the session env")
		fmt.Fprintln(stdout, "  unset <name>        — remove NAME from the session env")
		return 0
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "aish: env: get: usage: get <name>")
			return 2
		}
		v, ok := s.env.Get(args[1])
		if !ok {
			return 1
		}
		fmt.Fprintln(stdout, v)
		return 0
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "aish: env: set: usage: set <name> <value>")
			return 2
		}
		// Allow `env set NAME val with spaces` — args[2:] joined.
		value := strings.Join(args[2:], " ")
		if err := s.env.Set(args[1], value); err != nil {
			fmt.Fprintf(stderr, "aish: env: set: %v\n", err)
			return 1
		}
		return 0
	case "unset":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "aish: env: unset: usage: unset <name>")
			return 2
		}
		s.env.Unset(args[1])
		return 0
	default:
		fmt.Fprintf(stderr, "aish: env: unknown subcommand %q (try `env help`)\n", args[0])
		return 2
	}
}
