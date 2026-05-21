//go:build windows

package shell

import (
	"os"
	osexec "os/exec"
)

// platformExec is the Windows fallback. Windows has no
// syscall.Exec equivalent — there is no in-place process
// replacement. We spawn the child as a normal subprocess,
// wire stdio through to the parent, wait for it to finish,
// and return errExecReplaced carrying the child's exit code.
// The caller's main() inspects via IsExecReplaced and
// terminates the aish process with that code, approximating
// the POSIX semantics from the user's perspective.
func platformExec(argv0 string, argv []string, envv []string) error {
	cmd := osexec.Command(argv0, argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = envv
	if err := cmd.Run(); err != nil {
		// ExitError carries the child's status; anything else is
		// a spawn failure we treat as exit 127 (matches POSIX
		// `command not found`).
		if ee, ok := err.(*osexec.ExitError); ok {
			return &errExecReplaced{Code: ee.ExitCode()}
		}
		return &errExecReplaced{Code: 127}
	}
	return &errExecReplaced{Code: 0}
}
