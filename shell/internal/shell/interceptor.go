package shell

import (
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// Interceptor is the seam the Shell exposes for v0.1 cross-cutting
// concerns: structured history (v0.1-4) wires the first concrete
// implementation; telemetry (v0.1-5, TL2) wires the second on the
// same slice without touching this file.
//
// Contract:
//
//	Before(pipeline, line) runs immediately before exec.Run is called.
//	An error returned here is logged to stderr but does NOT abort the
//	command — "snapshot is best-effort, command is mandatory."
//
//	After(pipeline, line, exitCode, duration) runs immediately after
//	exec.Run returns, even on parse or exec failure. The implementation
//	is responsible for tolerating a missing Before (e.g. a tier that
//	skips the seam altogether).
//
// Order: Before is called in registration order; After is called in
// reverse registration order so each interceptor's After observes
// the state produced by interceptors registered before it.
type Interceptor interface {
	Before(pipeline *parser.Pipeline, line string) error
	After(pipeline *parser.Pipeline, line string, exitCode int, dur time.Duration)
}
