//go:build !windows

package shell

import "syscall"

// platformExec is the POSIX path: syscall.Exec replaces the
// current process image with the named binary. On success this
// function NEVER RETURNS — the Go runtime is gone. On failure
// (binary not found mid-call, permission denied, ENOEXEC, …) it
// returns the syscall error.
//
// We wrap that error in errExecReplaced{Code: 127} so the REPL
// loop unwinds the same way it does for a not-found at lookup
// time. The error message is written by the caller (execBuiltin).
func platformExec(argv0 string, argv []string, envv []string) error {
	if err := syscall.Exec(argv0, argv, envv); err != nil {
		return &errExecReplaced{Code: 127}
	}
	// Unreachable — syscall.Exec doesn't return on success — but
	// keeping the explicit nil satisfies the compiler and signals
	// intent.
	return nil
}
