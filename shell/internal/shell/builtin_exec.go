package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// errExecReplaced is the sentinel for "this shell has been
// replaced by an exec'd process and the REPL should terminate."
// On POSIX, syscall.Exec only returns on failure; the success
// path leaves the calling Go process behind and never reaches
// any Go code. On Windows we fake it: spawn the child, wait,
// then return errExecReplaced{Code: child exit} so the REPL
// shuts down cleanly with the right code.
type errExecReplaced struct {
	Code int
}

func (e *errExecReplaced) Error() string {
	return fmt.Sprintf("exec replaced (exit %d)", e.Code)
}

// IsExecReplaced reports whether err originated from `exec` having
// successfully run on a platform without true `syscall.Exec`
// (Windows). On POSIX this is never reached because syscall.Exec
// doesn't return on success.
func IsExecReplaced(err error) (int, bool) {
	var er *errExecReplaced
	if errors.As(err, &er) {
		return er.Code, true
	}
	return 0, false
}

// execBuiltin implements the POSIX `exec [cmd [args …]]` built-in.
//
//	Bare `exec`:
//	  No-op success (POSIX: applies redirections only — we have
//	  none yet → silent success, exit 0). Returns nil.
//
//	`exec <cmd> [args]`:
//	  Resolves <cmd> via the shell's PATH, then on POSIX calls
//	  syscall.Exec — which REPLACES the calling process and never
//	  returns on success. On Windows, runs the child to completion
//	  and returns errExecReplaced so main() exits with the child's
//	  status.
//
//	Resolution failure:
//	  Writes "aish: exec: <cmd>: command not found" and exits 127.
//	  POSIX is explicit: a failed exec in a login shell terminates
//	  the shell — but matching bash's behavior, we exit non-zero
//	  via the sentinel rather than abort the REPL loop, so tests
//	  and non-login interactive sessions don't hang.
//
// The dispatcher is responsible for splitting the line on
// whitespace; this function takes already-tokenised args. The
// first arg is the command; subsequent args become its argv[1:].
//
// Variables in args are expected to be ALREADY EXPANDED by the
// caller — execBuiltin does no further expansion.
// stdout is accepted for API symmetry with the rest of the
// dispatcher's built-in signatures even though exec writes
// only to stderr on failure.
func (s *Shell) execBuiltin(args []string, _ io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		// Bare `exec` — POSIX redirection-only form. We have no
		// redirection support yet, so this is a silent no-op.
		s.SetLastExit(0)
		return nil
	}
	// Resolve the binary via the shell's $PATH. We deliberately do
	// NOT fall back to the host PATH — the shell's view of PATH
	// (post-RC, post-export) is the source of truth.
	binary, err := s.lookPathInShellEnv(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "aish: exec: %s: command not found\n", args[0])
		s.SetLastExit(127)
		// Match bash: a failed exec in a login shell terminates the
		// shell. The sentinel carries the 127 status so main() exits
		// with the right code; non-login REPL sessions also unwind,
		// which is intentional — bash does the same.
		return &errExecReplaced{Code: 127}
	}
	envv := s.env.Environ()
	// argv[0] is conventionally the resolved binary path; pass
	// args verbatim so the child sees its own name as args[0].
	argv := make([]string, 0, len(args))
	argv = append(argv, args[0])
	argv = append(argv, args[1:]...)
	// Production path: call the platform exec hook. On POSIX it's
	// syscall.Exec and does NOT return on success. On Windows it's
	// a spawn+wait that returns errExecReplaced.
	if s.execFn != nil {
		// Test injection — let tests assert without replacing the
		// process. The injected fn returns an error; we return it
		// verbatim so tests see the exact value.
		return s.execFn(binary, argv, envv)
	}
	return platformExec(binary, argv, envv)
}

// lookPathInShellEnv resolves cmd against the shell's env $PATH
// (NOT the host os.Getenv("PATH")). Mirrors the pivot in
// isKnownBinary above — we want `exec` to see the same world the
// REPL sees, including any in-process `export PATH=...`.
func (s *Shell) lookPathInShellEnv(cmd string) (string, error) {
	// Absolute / relative paths bypass PATH lookup, matching POSIX.
	if strings.ContainsAny(cmd, "/\\") {
		if _, statErr := os.Stat(cmd); statErr == nil {
			return cmd, nil
		}
		return "", fmt.Errorf("not found")
	}
	pathBefore := os.Getenv("PATH")
	if p, ok := s.env.Get("PATH"); ok {
		_ = os.Setenv("PATH", p)
		defer func() { _ = os.Setenv("PATH", pathBefore) }()
	}
	return osexec.LookPath(cmd)
}

// parseExecLine tokenises the `exec <cmd> [args …]` tail using
// the shell's existing parser so quoting and basic word-splitting
// work consistently with the rest of the dispatcher. Returns the
// argv slice (empty for bare `exec`) and any parse error.
func parseExecLine(tail string) ([]string, error) {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return nil, nil
	}
	pipeline, err := parser.Parse(tail)
	if err != nil {
		return nil, err
	}
	if len(pipeline.Commands) != 1 {
		// `exec` can't pipe — POSIX semantics: it's a builtin that
		// replaces the current process; pipes don't apply.
		return nil, fmt.Errorf("pipelines not allowed after `exec`")
	}
	c := pipeline.Commands[0]
	// parser.Command splits argv[0] (Name) from argv[1:] (Args); we
	// want the flat slice including the command name.
	out := make([]string, 0, 1+len(c.Args))
	out = append(out, c.Name)
	out = append(out, c.Args...)
	return out, nil
}
