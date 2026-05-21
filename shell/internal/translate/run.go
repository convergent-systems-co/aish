package translate

import (
	"context"
	"fmt"
	"io"
	"os"
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
		// Apply any redirects on this command at the engine layer
		// (open files, wire them to the runner's streams) so the
		// runner only ever sees the bare invocation. This keeps the
		// runner's parser-feed independent of redirect syntax.
		stdin, stdout, stderr, cleanup, err := applyRedirects(v.Redirects, opts.Stdin, opts.Stdout, opts.Stderr)
		if err != nil {
			fmt.Fprintf(opts.Stderr, "aish: run: line %d: %v\n", v.Line, err)
			return 1, nil
		}
		code, runErr := r.Run(ctx, formatCommandNoRedirect(v), stdin, stdout, stderr)
		cleanup()
		return code, runErr
	case Pipe:
		// Apply each stage's redirects, then re-render the pipeline
		// without redirect syntax so the existing pipeline parser
		// doesn't have to know about it. Only the LAST stage gets
		// the caller's stdout; intermediate redirects on inner
		// stages aren't handled in MVP (and would be unusual —
		// stdout redirect on a non-last stage usually doesn't make
		// sense in a pipeline). Inner-stage redirects are flagged
		// to stderr as a warning but not fatal.
		stages := make([]string, len(v.Stages))
		var cleanup func()
		stdin, stdout, stderr := opts.Stdin, opts.Stdout, opts.Stderr
		for i, c := range v.Stages {
			if i == len(v.Stages)-1 && len(c.Redirects) > 0 {
				ns, no, ne, cl, err := applyRedirects(c.Redirects, stdin, stdout, stderr)
				if err != nil {
					fmt.Fprintf(opts.Stderr, "aish: run: line %d: %v\n", c.Line, err)
					return 1, nil
				}
				stdin, stdout, stderr = ns, no, ne
				cleanup = cl
			} else if i < len(v.Stages)-1 && len(c.Redirects) > 0 {
				fmt.Fprintf(opts.Stderr, "aish: run: line %d: redirects on non-terminal pipeline stage ignored\n", c.Line)
			}
			stages[i] = formatCommandNoRedirect(c)
		}
		code, runErr := r.Run(ctx, strings.Join(stages, " | "), stdin, stdout, stderr)
		if cleanup != nil {
			cleanup()
		}
		return code, runErr
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

// formatCommandNoRedirect is like formatCommand but omits the
// redirect operators. The engine handles redirects at the stream
// layer (applyRedirects), so the underlying runner / parser never
// needs to understand `>` syntax.
func formatCommandNoRedirect(c Command) string {
	parts := []string{c.Name}
	for _, a := range c.Args {
		parts = append(parts, maybeQuote(a))
	}
	return strings.Join(parts, " ")
}

// applyRedirects opens each redirect's target file and returns the
// updated (stdin, stdout, stderr) plus a cleanup closure the caller
// MUST invoke. Opening fails → error returned, original streams
// unchanged. On success, cleanup closes every file we opened.
func applyRedirects(rs []Redirect, stdin io.Reader, stdout, stderr io.Writer) (io.Reader, io.Writer, io.Writer, func(), error) {
	opened := []*os.File{}
	cleanup := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	for _, r := range rs {
		switch r.Op {
		case RedirectIn:
			f, err := os.Open(r.Target)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, fmt.Errorf("open %s: %w", r.Target, err)
			}
			opened = append(opened, f)
			stdin = f
		case RedirectOut:
			f, err := os.Create(r.Target)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, fmt.Errorf("create %s: %w", r.Target, err)
			}
			opened = append(opened, f)
			stdout = f
		case RedirectAppend:
			f, err := os.OpenFile(r.Target, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, fmt.Errorf("open-append %s: %w", r.Target, err)
			}
			opened = append(opened, f)
			stdout = f
		case RedirectErrOut:
			f, err := os.Create(r.Target)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, fmt.Errorf("create %s: %w", r.Target, err)
			}
			opened = append(opened, f)
			stderr = f
		case RedirectErrToOut:
			stderr = stdout
		}
	}
	return stdin, stdout, stderr, cleanup, nil
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
