// Package shell — `jobs` built-in.
//
// v0.3-1 follow-up #83. Lists the live JobTable. No-op on an empty
// table (matches bash; stdout silent, exit 0). Output format:
//
//	[1]+  Running    sleep 30
//	[2]-  Stopped    yes | head
//	[3]   Done       echo hi
//
// The `+` / `-` markers identify the "current" and "previous" jobs
// per bash's notion (last-backgrounded / second-last-backgrounded).
package shell

import (
	"fmt"
	"io"
)

func (s *Shell) jobsBuiltin(args []string, stdout, stderr io.Writer) int {
	if s.jobTable == nil {
		fmt.Fprintln(stderr, "aish: jobs: job control not available")
		return 1
	}
	// Reject extra args. POSIX `jobs` accepts flags (-l, -p) we
	// don't implement yet; emit a clear error rather than silently
	// ignoring them so users notice.
	if len(args) > 0 {
		fmt.Fprintf(stderr, "aish: jobs: unexpected argument(s): %v (only bare `jobs` is supported)\n", args)
		return 2
	}
	for _, line := range s.jobTable.List() {
		fmt.Fprintln(stdout, line)
	}
	return 0
}
