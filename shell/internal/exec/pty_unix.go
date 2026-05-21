//go:build !windows

package exec

import (
	"context"
	"io"
	"os"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// runPTY is the Unix entrypoint. Stubbed for the failing-test
// phase — replaced with the real creack/pty implementation in the
// next commit. Returning errPTYUnsupported keeps the build green
// while the tests that exercise PTY behavior all fail (the desired
// red state per TDD).
func runPTY(
	ctx context.Context,
	cmd parser.Command,
	env []string,
	stdin, stdout *os.File,
	stderr io.Writer,
) (int, error) {
	return 0, errPTYUnsupported
}
