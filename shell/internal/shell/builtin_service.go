package shell

import (
	"errors"
	"fmt"
	"io"

	"github.com/convergent-systems-co/aish/shell/internal/exec"
)

// serviceBuiltin implements `aish service <list|status|start|stop>` —
// v1.0-2 task #138.
//
// MVP scope:
//
//   - `service list` walks the SCM via exec.ListServices.
//   - `service status <name>` reads one service's detail record.
//   - `service start|stop <name>` toggle the service through the SCM.
//     Both require an elevated runtime; we surface the Win32 error
//     verbatim and exit 1 when we don't have it.
//
// On non-Windows hosts every subcommand surfaces a clear
// "not supported" message via exec.ErrUnsupported and exits 2.
func (s *Shell) serviceBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(stdout, "usage: service <list|status|start|stop> [name]")
		fmt.Fprintln(stdout, "  list                  — enumerate Windows services")
		fmt.Fprintln(stdout, "  status <name>         — report status, start type, display name")
		fmt.Fprintln(stdout, "  start <name>          — request the SCM start <name> (requires admin)")
		fmt.Fprintln(stdout, "  stop  <name>          — request the SCM stop  <name> (requires admin)")
		return 0
	}
	switch args[0] {
	case "list":
		entries, err := exec.ListServices()
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: service: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: service: list: %v\n", err)
			return 1
		}
		// Column layout chosen to match the Windows `sc query` look —
		// status first, then the service name + display name.
		for _, svc := range entries {
			fmt.Fprintf(stdout, "%-16s %-40s %s\n", svc.Status, svc.Name, svc.DisplayName)
		}
		return 0
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "aish: service: status: usage: status <name>")
			return 2
		}
		svc, err := exec.ServiceStatus(args[1])
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: service: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: service: status: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "name:         %s\n", svc.Name)
		fmt.Fprintf(stdout, "display:      %s\n", svc.DisplayName)
		fmt.Fprintf(stdout, "status:       %s\n", svc.Status)
		fmt.Fprintf(stdout, "start type:   %s\n", svc.StartType)
		return 0
	case "start":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "aish: service: start: usage: start <name>")
			return 2
		}
		err := exec.StartService(args[1])
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: service: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: service: start: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "service %s: start requested\n", args[1])
		return 0
	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "aish: service: stop: usage: stop <name>")
			return 2
		}
		err := exec.StopService(args[1])
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: service: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: service: stop: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "service %s: stop requested\n", args[1])
		return 0
	default:
		fmt.Fprintf(stderr, "aish: service: unknown subcommand %q (try `service help`)\n", args[0])
		return 2
	}
}
