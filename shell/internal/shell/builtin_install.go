package shell

import (
	"fmt"
	"io"
	osexec "os/exec"
	"runtime"
)

// installBuiltin implements `aish install <pkg>` — v1.0-2 task #137.
//
// MVP delegates to the platform package manager:
//
//   - Windows: `winget install <pkg>` (`--silent` so we don't block on
//     a GUI confirmation when called from a script).
//   - macOS / Linux: returns a one-line "not supported on <GOOS>" so
//     the cross-host build stays honest about scope. POSIX install
//     verbs (`apt install`, `brew install`) are a separate epic.
//
// The built-in does NOT attempt to auto-elevate. If winget needs
// admin privileges for the install location, the user re-runs from
// an elevated shell.
func (s *Shell) installBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(stdout, "usage: install <pkg>")
		fmt.Fprintln(stdout, "  Install <pkg> using the platform package manager.")
		fmt.Fprintln(stdout, "  Windows uses winget; non-Windows hosts report `not supported`.")
		return 0
	}
	if runtime.GOOS != "windows" {
		fmt.Fprintf(stderr, "aish: install: not supported on %s (v1.0 ships winget only)\n", runtime.GOOS)
		return 2
	}
	pkg := args[0]
	// `winget install --silent <pkg>` — non-interactive flag survives
	// CI runs and scripted invocations.
	cmd := osexec.Command("winget", "install", "--silent", pkg)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*osexec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "aish: install: %v\n", err)
		return 127
	}
	return 0
}
