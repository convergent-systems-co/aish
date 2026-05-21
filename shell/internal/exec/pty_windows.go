//go:build windows

package exec

import (
	"context"
	"io"
	"os"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// runPTY on Windows returns errPTYUnsupported. ConPTY plumbing is a
// separate epic (GOALS §"v1.0 — Windows Native"); the seam exists so
// that work can land without re-touching shell/runExternal.
func runPTY(
	ctx context.Context,
	cmd parser.Command,
	env []string,
	stdin, stdout *os.File,
	stderr io.Writer,
) (int, error) {
	return 0, errPTYUnsupported
}
