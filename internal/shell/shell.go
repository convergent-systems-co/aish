// Package shell is the top-level aish runtime. The minimum shell (v0.1-1)
// reads commands from stdin, dispatches them, and surfaces output. Later
// epics add the intent cache, plugin contract, history engine, and personas.
package shell

import "io"

// Shell holds runtime state across REPL iterations: working directory,
// environment, last exit code, and (later) cache/plugin handles.
type Shell struct{}

// New returns a Shell ready to Run.
func New() *Shell {
	return &Shell{}
}

// Run drives the REPL until stdin closes. The full implementation lands
// in the v0.1-1 coder sub-tasks (parser/exec, env+cwd+prompt, stream-detect);
// the seed commit only exposes the entry shape so cmd/aish/main.go compiles.
func (s *Shell) Run(stdin io.Reader, stdout, stderr io.Writer) error {
	return nil
}
