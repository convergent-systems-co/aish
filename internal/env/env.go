// Package env owns environment-variable storage and expansion for the
// shell runtime. It is intentionally separate from internal/shell so the
// expansion rules can be tested in isolation.
//
// v0.1-1 scope (sub-issues #8, #9):
//   - Set/Get over an os.Environ-shaped backing slice ([]string of "K=V").
//   - Expand $VAR and ${VAR} forms.
//   - Expand $? and ${?} to the textual last-exit-code.
//
// No backtick or $(cmd) substitution (deferred to v0.3-1).
// No glob, no brace expansion.
package env

// Env is a string-keyed environment table backed by an os.Environ-shaped
// []string for cheap handoff to internal/exec.
type Env struct {
	// vars holds entries in "KEY=VALUE" form so Environ() can return them
	// directly to os/exec without re-allocation.
	vars []string
}

// New returns an empty Env.
func New() *Env {
	return &Env{}
}

// FromSlice constructs an Env pre-populated from an os.Environ()-shaped
// slice. Entries that do not contain `=` are skipped silently per the
// convention of os/exec.
func FromSlice(initial []string) *Env {
	return &Env{}
}

// Set assigns name=value. If name already exists, its entry is replaced.
// An empty name is rejected (returns an error) — `export =foo` is a defect.
func (e *Env) Set(name, value string) error {
	return nil
}

// Get returns the current value of name and whether it was set. An unset
// var returns ("", false).
func (e *Env) Get(name string) (string, bool) {
	return "", false
}

// Unset removes name. Unsetting a name that was never set is a no-op.
func (e *Env) Unset(name string) {
}

// Environ returns the env in os.Environ shape ("KEY=VALUE" slice). Safe
// to pass directly to exec.Cmd.Env.
func (e *Env) Environ() []string {
	return nil
}

// Expand resolves $VAR, ${VAR}, $?, and ${?} forms in input. lastExit is
// the integer to substitute for $?. Unset variables expand to the empty
// string (POSIX default). A literal `$` followed by a non-identifier
// character is left unchanged.
//
// Implementation lives in the v0.1-1 coder T2 sub-task; this stub returns
// the input unchanged so the test file compiles.
func (e *Env) Expand(input string, lastExit int) string {
	return input
}
