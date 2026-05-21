package shell

import (
	"github.com/convergent-systems-co/aish/shell/internal/parser"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// tagPipelineTaint walks pl and sets Command.Tainted / Pipeline.Tainted
// based on (a) exact-match lookups against reg and (b) the
// direct-secret-get heuristic. Pure function — exercised in unit tests
// without spinning up a Shell.
//
// The function MUTATES pl through the pointer receiver because the
// shell's call site retains the pre-walked Pipeline and we want the
// downstream interceptor loop to see the updated bits without an
// extra copy step. parser.Pipeline is a small value type (slice +
// two bools) so the mutation has no aliasing concerns.
//
// reg may be nil — a nil registry contributes zero argv-match taint
// (the direct-secret-get heuristic still runs).
func tagPipelineTaint(pl *parser.Pipeline, reg *secrets.TaintedRegistry) {
	if pl == nil {
		return
	}
	anyTainted := false
	for i := range pl.Commands {
		c := &pl.Commands[i]
		// Direct secret-get heuristic: `secret get NAME ...` always
		// taints its own stage regardless of registry.
		if isSecretGetStage(c) {
			c.Tainted = true
			anyTainted = true
			continue
		}
		// Argv-match: any element whose literal exact-matches a
		// registered tainted value flips the bit.
		if argvMatchesTainted(c.Name, c.Args, reg) {
			c.Tainted = true
			anyTainted = true
		}
	}
	if anyTainted {
		pl.Tainted = true
	}
}

// isSecretGetStage returns true when c's invocation is the
// `secret get NAME` built-in form.
func isSecretGetStage(c *parser.Command) bool {
	if c == nil {
		return false
	}
	if c.Name != "secret" {
		return false
	}
	if len(c.Args) < 1 {
		return false
	}
	return c.Args[0] == "get"
}

// argvMatchesTainted returns true when name or any arg exact-matches
// a literal registered as tainted. reg.Has is nil-safe.
func argvMatchesTainted(name string, args []string, reg *secrets.TaintedRegistry) bool {
	if reg.Has(name) {
		return true
	}
	for _, a := range args {
		if reg.Has(a) {
			return true
		}
	}
	return false
}
