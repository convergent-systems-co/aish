package translate

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Runner is the abstraction the run engine uses to execute one
// command line. The shell's runExternal wraps this; tests provide
// a fake recorder.
//
// Run is intentionally a tier-agnostic command-string sink: by the
// time the engine calls it, the AST node has been re-rendered to
// a single line of text (re-using formatCommand from explain.go),
// and the shell decides which dispatch tier to take. This keeps
// the cache layer benefiting from real command runs at no extra
// cost.
type Runner interface {
	// Run executes one command line. The cmdline is post-AST and
	// is shaped like what an aish REPL user would type.
	// Returns the exit code and a non-nil error only on
	// unrecoverable I/O failure on the streams.
	Run(ctx context.Context, cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
}

// RunOptions controls the run engine.
type RunOptions struct {
	// Stdin/Stdout/Stderr are passed through to every executed
	// command. The first command in the script receives Stdin;
	// later ones inherit it too (no piping between top-level
	// statements is implied — that lives inside a Pipe node).
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Env is a copy of the parent shell's environment, supplied so
	// in-script Assign statements can mutate it without leaking back
	// to the shell. The runner is responsible for honoring it; the
	// engine passes it forward via the Runner's wired exec path.
	//
	// We don't ship a generalized env-substitution engine here — the
	// shell's existing env.Expand handles `$VAR` / `${VAR}` and runs
	// before dispatch. Assignments inside the script update a local
	// EnvSet (a callback the runner provides).
	EnvSet func(name, value string) // optional; nil disables in-script assign
}

// Run executes script through r. Unknown nodes are surfaced and
// abort the run with exit 2; subsequent statements do not execute.
// Returns the exit code of the last successfully-executed
// statement (or 2 on Unknown abort, or whatever the last command
// returned).
func Run(ctx context.Context, r Runner, script *Script, opts RunOptions) (int, error) {
	if script == nil {
		return 0, fmt.Errorf("run: nil script")
	}
	return runStatements(ctx, r, script.Statements, opts)
}

func runStatements(ctx context.Context, r Runner, stmts []Statement, opts RunOptions) (int, error) {
	last := 0
	for _, st := range stmts {
		code, err := runOne(ctx, r, st, opts)
		if err != nil {
			return code, err
		}
		last = code
		// Hard-abort on Unknown so we never half-run a script.
		if _, isUnknown := st.(Unknown); isUnknown {
			return code, nil
		}
	}
	return last, nil
}

func runOne(ctx context.Context, r Runner, st Statement, opts RunOptions) (int, error) {
	switch v := st.(type) {
	case Comment:
		return 0, nil
	case Assign:
		if opts.EnvSet != nil {
			opts.EnvSet(v.Name, v.Value)
		}
		return 0, nil
	case Command:
		return r.Run(ctx, formatCommand(v), opts.Stdin, opts.Stdout, opts.Stderr)
	case Pipe:
		stages := make([]string, len(v.Stages))
		for i, c := range v.Stages {
			stages[i] = formatCommand(c)
		}
		return r.Run(ctx, strings.Join(stages, " | "), opts.Stdin, opts.Stdout, opts.Stderr)
	case Cond:
		for _, br := range v.Branches {
			code, err := runOne(ctx, r, br.Test, opts)
			if err != nil {
				return code, err
			}
			if code == 0 {
				return runStatements(ctx, r, br.Body, opts)
			}
		}
		if len(v.Else) > 0 {
			return runStatements(ctx, r, v.Else, opts)
		}
		return 0, nil
	case Loop:
		switch v.Kind {
		case LoopFor:
			last := 0
			for _, w := range v.Words {
				if opts.EnvSet != nil {
					opts.EnvSet(v.Var, w)
				}
				code, err := runStatements(ctx, r, v.Body, opts)
				if err != nil {
					return code, err
				}
				last = code
			}
			return last, nil
		case LoopWhile:
			last := 0
			// Safety cap so a broken script doesn't spin forever
			// inside the test harness. 10k iterations is generous
			// for any reasonable while-loop.
			for i := 0; i < 10000; i++ {
				code, err := runOne(ctx, r, v.Test, opts)
				if err != nil {
					return code, err
				}
				if code != 0 {
					return last, nil
				}
				code, err = runStatements(ctx, r, v.Body, opts)
				if err != nil {
					return code, err
				}
				last = code
			}
			return last, fmt.Errorf("run: while-loop iteration cap exceeded (10000)")
		}
	case Case:
		// MVP: match each pattern as a literal string (no glob
		// expansion). The first matching arm executes; no
		// fallthrough.
		for _, arm := range v.Arms {
			for _, p := range arm.Patterns {
				if matchCasePattern(p, v.Word) {
					return runStatements(ctx, r, arm.Body, opts)
				}
			}
		}
		return 0, nil
	case FuncDef:
		// MVP: function definitions are recorded but not callable
		// from within the script — invoking a defined function
		// during the same `aish run` invocation is deferred. The
		// definition is preserved by Migrate.
		return 0, nil
	case Unknown:
		fmt.Fprintf(opts.Stderr, "aish: run: line %d: cannot translate (%s)\n", v.Line, v.Reason)
		return 2, nil
	}
	return 0, nil
}

// matchCasePattern is the MVP pattern matcher: literal equality and
// the trivial `*` wildcard. Anything more elaborate (character
// classes, `?`, multi-segment globs) is deferred.
func matchCasePattern(pat, word string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "*" {
		return true
	}
	if strings.HasPrefix(pat, "*") && strings.HasSuffix(pat, "*") {
		core := strings.Trim(pat, "*")
		return strings.Contains(word, core)
	}
	if strings.HasSuffix(pat, "*") {
		return strings.HasPrefix(word, strings.TrimSuffix(pat, "*"))
	}
	if strings.HasPrefix(pat, "*") {
		return strings.HasSuffix(word, strings.TrimPrefix(pat, "*"))
	}
	return pat == word
}
