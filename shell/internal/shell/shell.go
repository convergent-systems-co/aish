// Package shell is the top-level aish runtime. The minimum shell (v0.1-1)
// reads commands from stdin, dispatches them, and surfaces output. Later
// epics add the intent cache, plugin contract, history engine, and personas.
package shell

import (
	"io"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

// Shell holds runtime state across REPL iterations: working directory,
// environment, last exit code, and (later) cache/plugin handles.
type Shell struct {
	// cwd is the shell's current working directory. Built-in `cd` updates
	// this; child processes inherit it as their starting cwd.
	cwd string
	// env owns env-var storage and $VAR/$? expansion.
	env *env.Env
	// lastExit is the exit code of the most recent foreground pipeline.
	// Expanded into `$?` and `${?}` by env.Expand.
	lastExit int
}

// New returns a Shell with cwd initialised to the current process working
// directory and an empty env. Errors from os.Getwd surface to the caller
// so the REPL never starts in an unknown directory.
func New() *Shell {
	return &Shell{}
}

// Run drives the REPL until stdin closes.
//
// Loop shape (owned by v0.1-1 coder T2):
//  1. render prompt to stdout
//  2. read one line from stdin
//  3. if line starts with `cd `, call Cd; loop
//  4. if line starts with `export `, call SetEnv via parsed KEY=VAL; loop
//  5. otherwise expand $VAR/$? then parse + exec; capture exit code via
//     SetLastExit; loop
//
// Returns nil on clean EOF, non-nil on unrecoverable I/O failures.
func (s *Shell) Run(stdin io.Reader, stdout, stderr io.Writer) error {
	return nil
}

// Cwd returns the shell's current working directory.
func (s *Shell) Cwd() string {
	return ""
}

// Cd changes the shell's working directory. A relative path resolves
// against the current Cwd; `~` is expanded to $HOME if set. Returns a
// non-nil error if the target does not exist or is not a directory.
func (s *Shell) Cd(path string) error {
	return nil
}

// SetEnv binds name=value in the shell env.
func (s *Shell) SetEnv(name, value string) error {
	return nil
}

// GetEnv returns the bound value of name and whether it was set.
func (s *Shell) GetEnv(name string) (string, bool) {
	return "", false
}

// LastExit returns the exit code of the most recent foreground pipeline.
// Zero before any command has run.
func (s *Shell) LastExit() int {
	return 0
}

// SetLastExit records code as the most recent pipeline's exit code.
// Visible to subsequent input via `$?` and `${?}` substitution.
func (s *Shell) SetLastExit(code int) {
}

// Prompt renders the prompt string aish writes before each REPL read.
// v0.1-1 format: "<cwd-shortened> > " where `~` substitutes for the
// $HOME prefix. No git segment, no Nerd Font.
func (s *Shell) Prompt() string {
	return ""
}
