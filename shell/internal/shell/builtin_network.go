package shell

import (
	"errors"
	"fmt"
	"io"

	"github.com/convergent-systems-co/aish/shell/internal/exec"
)

// networkBuiltin implements `aish network <interfaces|routes>` —
// v1.0-2 task #141.
//
// MVP:
//
//   - `network interfaces` lists every adapter with its name, MAC,
//     first IPv4 (when present) and operational status. Multi-IP
//     adapters surface only the first IPv4 address; the full list
//     belongs to a future detail view.
//
// Deferred:
//
//   - `network routes` requires GetIpForwardTable / GetIpForwardTable2,
//     plus a column model the MVP doesn't need. Prints "not yet
//     implemented" until v1.1.
//
// On non-Windows hosts every subcommand surfaces a clear "not
// supported on <GOOS>" via exec.ErrUnsupported and exits 2.
func (s *Shell) networkBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(stdout, "usage: network <interfaces|routes>")
		fmt.Fprintln(stdout, "  interfaces       — name, MAC, first IPv4, status")
		fmt.Fprintln(stdout, "  routes           — (v1.1: not yet implemented)")
		return 0
	}
	switch args[0] {
	case "interfaces":
		adapters, err := exec.ListNetworkInterfaces()
		if errors.Is(err, exec.ErrUnsupported) {
			fmt.Fprintln(stderr, "aish: network: not supported on this host (Windows only)")
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "aish: network: interfaces: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%-30s %-18s %-16s %s\n", "NAME", "MAC", "IPV4", "STATUS")
		for _, a := range adapters {
			status := "down"
			if a.Operational {
				status = "up"
			}
			fmt.Fprintf(stdout, "%-30s %-18s %-16s %s\n", a.Name, a.MAC, a.IPv4, status)
		}
		return 0
	case "routes":
		fmt.Fprintln(stderr, "aish: network: routes: not yet implemented (v1.1)")
		return 2
	default:
		fmt.Fprintf(stderr, "aish: network: unknown subcommand %q (try `network help`)\n", args[0])
		return 2
	}
}
