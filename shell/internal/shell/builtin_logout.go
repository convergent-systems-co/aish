package shell

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// errLogout is the sentinel that propagates out of `logout` so the
// REPL loop (runStream / runTTY) recognizes a clean shutdown
// equivalent to EOF on stdin. The wrapped Code is `logout`'s
// optional integer argument (default 0) — the process exits with
// this code per POSIX `logout [n]`.
//
// We use a typed error rather than reusing io.EOF so the loop can
// distinguish "user typed logout" (intentional, exit code carried)
// from "stdin closed" (EOF, default exit code 0).
type errLogout struct {
	Code int
}

func (e *errLogout) Error() string {
	return fmt.Sprintf("logout (exit %d)", e.Code)
}

// IsLogout reports whether err originated from a `logout` built-in.
// The dispatchers use this to short-circuit the REPL loop cleanly.
func IsLogout(err error) (int, bool) {
	var lo *errLogout
	if errors.As(err, &lo) {
		return lo.Code, true
	}
	return 0, false
}

// logoutBuiltin implements the POSIX `logout [n]` command.
//
//	In login mode:
//	  - bare `logout` returns errLogout{Code: 0} so the REPL loop
//	    exits as if stdin had closed.
//	  - `logout 7` returns errLogout{Code: 7} so the calling process
//	    can exit with that status (main inspects with IsLogout).
//	  - A non-integer argument is a user error: exit 1 with a stderr
//	    message; the shell keeps running.
//
//	In non-login mode:
//	  - prints `aish: logout: not login shell` to stderr (bash's exact
//	    error text), exits 1, the shell keeps running.
//
// The return signature is (err) only; setting lastExit before
// returning errLogout matters when the caller chooses to swallow
// the sentinel (currently nothing does — but keeping the contract
// explicit avoids surprise).
func (s *Shell) logoutBuiltin(args []string, stderr io.Writer) error {
	if !s.loginMode {
		fmt.Fprintln(stderr, "aish: logout: not login shell")
		s.SetLastExit(1)
		return nil
	}
	code := 0
	if len(args) > 0 {
		// POSIX `logout [n]` — only one integer argument is meaningful.
		// Extra args are accepted but ignored (matches bash).
		raw := strings.TrimSpace(args[0])
		n, err := strconv.Atoi(raw)
		if err != nil {
			fmt.Fprintf(stderr, "aish: logout: %s: numeric argument required\n", args[0])
			s.SetLastExit(1)
			return nil
		}
		code = n
	}
	s.SetLastExit(code)
	return &errLogout{Code: code}
}
