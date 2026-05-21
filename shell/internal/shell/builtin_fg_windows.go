//go:build windows

// Package shell — `fg` / `bg` stubs for Windows.
//
// Job control via POSIX process groups, controlling-TTY ownership,
// and SIGCONT/SIGTSTP doesn't map to Windows. The JobObject-based
// equivalent is a separate v1.0 follow-up. For now the built-ins
// print a clear message and return exit 1 on Windows; the rest of
// the shell stays usable.
package shell

import (
	"fmt"
	"io"
)

func (s *Shell) fgBuiltin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "aish: fg: job control not supported on Windows")
	return 1
}

func (s *Shell) bgBuiltin(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "aish: bg: job control not supported on Windows")
	return 1
}
