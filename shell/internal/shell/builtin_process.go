package shell

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/convergent-systems-co/aish/shell/internal/exec"
)

// processBuiltin implements `aish process <list|kill>` — v1.0-2 task
// #139.
//
// MVP columns: PID, ParentPID, Name. CPU% and memory require per-PID
// QueryFullProcessImageName + GetProcessMemoryInfo calls on Windows,
// or sampling intervals to compute CPU% — both deferred to v1.1.
//
// `kill` opens the process with PROCESS_TERMINATE and calls
// TerminateProcess. The Win32 error (typically ERROR_ACCESS_DENIED
// when the user lacks SeDebugPrivilege) is surfaced verbatim.
func (s *Shell) processBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(stdout, "usage: process <list|kill> [pid]")
		fmt.Fprintln(stdout, "  list           — PID, ParentPID, ProcessName")
		fmt.Fprintln(stdout, "  kill <pid>     — terminate process (may require elevation)")
		return 0
	}
	switch args[0] {
	case "list":
		entries, err := exec.ListProcesses()
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: process: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: process: list: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%-8s %-8s %s\n", "PID", "PPID", "NAME")
		for _, p := range entries {
			fmt.Fprintf(stdout, "%-8d %-8d %s\n", p.PID, p.ParentPID, p.Name)
		}
		return 0
	case "kill":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "aish: process: kill: usage: kill <pid>")
			return 2
		}
		pid64, err := strconv.ParseUint(args[1], 10, 32)
		if err != nil {
			fmt.Fprintf(stderr, "aish: process: kill: invalid pid %q\n", args[1])
			return 2
		}
		err = exec.KillProcess(uint32(pid64))
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: process: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: process: kill: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "process %d: terminated\n", pid64)
		return 0
	default:
		fmt.Fprintf(stderr, "aish: process: unknown subcommand %q (try `process help`)\n", args[0])
		return 2
	}
}
