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

import (
	"fmt"
	"strconv"
	"strings"
)

// Env is a string-keyed environment table backed by an os.Environ-shaped
// []string for cheap handoff to internal/exec.
type Env struct {
	// vars holds entries in "KEY=VALUE" form so Environ() can return them
	// directly to os/exec without re-allocation. Entries are stored in
	// insertion order; Set on an existing key updates the entry in place.
	vars []string
}

// New returns an empty Env.
func New() *Env {
	return &Env{}
}

// FromSlice constructs an Env pre-populated from an os.Environ()-shaped
// slice. Entries that do not contain `=` are skipped silently per the
// convention of os/exec. Duplicate keys keep the last value (consistent
// with how POSIX shells flatten the inherited env).
func FromSlice(initial []string) *Env {
	e := &Env{}
	for _, kv := range initial {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			// Skip entries missing `=` or starting with `=` (empty key).
			continue
		}
		name := kv[:eq]
		value := kv[eq+1:]
		// Use Set so duplicate keys overwrite rather than appending.
		_ = e.Set(name, value)
	}
	return e
}

// Set assigns name=value. If name already exists, its entry is replaced
// in place (insertion order preserved). An empty name is rejected — the
// shell treats `export =foo` as a syntax defect, not a silent no-op.
func (e *Env) Set(name, value string) error {
	if name == "" {
		return fmt.Errorf("env: empty variable name")
	}
	prefix := name + "="
	for i, kv := range e.vars {
		if strings.HasPrefix(kv, prefix) {
			e.vars[i] = prefix + value
			return nil
		}
	}
	e.vars = append(e.vars, prefix+value)
	return nil
}

// Get returns the current value of name and whether it was set. An unset
// var returns ("", false).
func (e *Env) Get(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	prefix := name + "="
	for _, kv := range e.vars {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):], true
		}
	}
	return "", false
}

// Unset removes name. Unsetting a name that was never set is a no-op.
func (e *Env) Unset(name string) {
	if name == "" {
		return
	}
	prefix := name + "="
	for i, kv := range e.vars {
		if strings.HasPrefix(kv, prefix) {
			e.vars = append(e.vars[:i], e.vars[i+1:]...)
			return
		}
	}
}

// Environ returns the env in os.Environ shape ("KEY=VALUE" slice). Safe
// to pass directly to exec.Cmd.Env. The returned slice is a fresh copy
// so the caller cannot mutate Env's backing store.
func (e *Env) Environ() []string {
	if len(e.vars) == 0 {
		return nil
	}
	out := make([]string, len(e.vars))
	copy(out, e.vars)
	return out
}

// Expand resolves $VAR, ${VAR}, $?, and ${?} forms in input. lastExit is
// the integer to substitute for $?. Unset variables expand to the empty
// string (POSIX default). A literal `$` followed by a non-identifier
// character is left unchanged — `price: $` stays as written.
//
// Quote handling (POSIX):
//
//   - Inside single quotes (`'…'`), nothing expands. `$VAR` is a literal
//     four characters.
//   - Inside double quotes (`"…"`), `$VAR` / `${VAR}` / `$?` expand normally;
//     a literal `'` is just a single-quote character.
//   - Quote characters themselves are preserved in the output so the
//     downstream parser can strip them during tokenisation.
//
// An unterminated quote is NOT this function's concern — parser.Parse
// reports it as a tokenisation error. Expand simply leaves the trailing
// in-quote region literal.
//
// Identifier rule: a bare `$` consumes a leading letter or underscore,
// then any run of letters, digits, or underscores. `$1` is left alone
// for v0.1-1 (positional params are deferred to v0.3-1).
func (e *Env) Expand(input string, lastExit int) string {
	// Fast path: no `$` and no quotes means the input passes through.
	if !strings.ContainsAny(input, "$'\"") {
		return input
	}

	var b strings.Builder
	b.Grow(len(input))
	runes := []rune(input)

	// inSingle / inDouble track which (if any) quote region we're inside.
	// At most one is true at a time — single quotes inside double quotes
	// are literal characters (and vice versa).
	var inSingle, inDouble bool

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Quote-state transitions. The quote character itself is preserved
		// in the output so the parser can do its own quote-aware
		// tokenisation downstream.
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			b.WriteRune(r)
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			b.WriteRune(r)
			continue
		}

		// Inside single quotes, nothing else is special — including `$`.
		if inSingle {
			b.WriteRune(r)
			continue
		}

		if r != '$' {
			b.WriteRune(r)
			continue
		}

		// Lookahead: nothing after `$` -> emit literal `$`.
		if i+1 >= len(runes) {
			b.WriteByte('$')
			continue
		}
		next := runes[i+1]
		switch {
		case next == '?':
			b.WriteString(strconv.Itoa(lastExit))
			i++ // consume '?'
		case next == '{':
			// Braced form ${NAME} or ${?}.
			end := -1
			for j := i + 2; j < len(runes); j++ {
				if runes[j] == '}' {
					end = j
					break
				}
			}
			if end == -1 {
				// Unterminated brace: preserve literal text and stop trying.
				b.WriteByte('$')
				continue
			}
			name := string(runes[i+2 : end])
			if name == "?" {
				b.WriteString(strconv.Itoa(lastExit))
			} else if isIdentifier(name) {
				if v, ok := e.Get(name); ok {
					b.WriteString(v)
				}
				// Unset -> empty string (no write).
			} else {
				// Invalid identifier inside braces: pass through literally.
				b.WriteString("${" + name + "}")
			}
			i = end // consume up to and including '}'
		case isIdentStart(next):
			// Bare $NAME: consume identifier chars greedily.
			j := i + 1
			for j < len(runes) && isIdentCont(runes[j]) {
				j++
			}
			name := string(runes[i+1 : j])
			if v, ok := e.Get(name); ok {
				b.WriteString(v)
			}
			i = j - 1 // i++ in the for-loop will advance past the last consumed rune
		default:
			// Lone `$` followed by non-identifier (digit, punctuation, space):
			// preserved verbatim per POSIX-ish "no expansion" rule.
			b.WriteByte('$')
		}
	}
	return b.String()
}

// isIdentStart reports whether r can begin a shell-variable name.
// POSIX names start with a letter or underscore.
func isIdentStart(r rune) bool {
	return r == '_' ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z')
}

// isIdentCont reports whether r can continue a shell-variable name.
// POSIX continues with letters, digits, or underscores.
func isIdentCont(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

// isIdentifier reports whether s is a syntactically valid POSIX variable
// name (used only for braced-form validation).
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isIdentStart(r) {
				return false
			}
			continue
		}
		if !isIdentCont(r) {
			return false
		}
	}
	return true
}
