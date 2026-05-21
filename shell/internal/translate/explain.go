package translate

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// LLMEnricher is the optional interface aish uses to enrich an
// explanation with a paragraph from the inference plugin. The
// shell's cache.PluginClient.Infer satisfies this shape; tests
// pass a fake.
//
// The contract is: given the deterministic baseline explain text,
// return one or more sentences of prose that summarise the script
// at a higher level. Implementations MUST NOT call out to remote
// services unless the API key gate is satisfied — that's the
// caller's job to enforce.
type LLMEnricher interface {
	EnrichExplain(ctx context.Context, script string, baseline string) (string, error)
}

// ExplainOptions controls the explain engine. The zero value is a
// deterministic, offline, numbered-step explanation.
type ExplainOptions struct {
	// WithLLM, when true AND Enricher is non-nil, appends an LLM-
	// generated prose paragraph after the deterministic baseline.
	WithLLM  bool
	Enricher LLMEnricher
	// Source is the original script source; passed to the enricher
	// so it can summarise from raw input rather than baseline
	// (which omits the lexical detail of the original).
	Source string
}

// Explain walks script and writes a numbered, plain-language
// description to w. Output is deterministic for the same script;
// no map iteration, no goroutine ordering, no time-stamps.
func Explain(ctx context.Context, w io.Writer, script *Script, opts ExplainOptions) error {
	if script == nil {
		return fmt.Errorf("explain: nil script")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Script (%s, %d top-level statements):\n", script.Dialect, len(script.Statements))
	step := 1
	for _, st := range script.Statements {
		explainStatement(&b, st, &step, 0)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if opts.WithLLM && opts.Enricher != nil {
		summary, err := opts.Enricher.EnrichExplain(ctx, opts.Source, b.String())
		if err == nil && strings.TrimSpace(summary) != "" {
			fmt.Fprintf(w, "\nSummary:\n%s\n", strings.TrimSpace(summary))
		}
	}
	return nil
}

// explainStatement writes one node's prose into b. step is a pointer
// so child nodes (in a Cond's branch, a Loop's body, etc.) advance
// the same counter as the parent — every line of output is numbered
// once in the script.
func explainStatement(b *strings.Builder, st Statement, step *int, indent int) {
	prefix := strings.Repeat("  ", indent)
	switch v := st.(type) {
	case Comment:
		fmt.Fprintf(b, "%s%d. (line %d) Comment: %s\n", prefix, *step, v.Line, v.Text)
		*step++
	case Assign:
		exportLabel := ""
		if v.Exported {
			exportLabel = " (exported)"
		}
		fmt.Fprintf(b, "%s%d. (line %d) Set variable %s = %q%s\n", prefix, *step, v.Line, v.Name, v.Value, exportLabel)
		*step++
	case Command:
		fmt.Fprintf(b, "%s%d. (line %d) Run %s\n", prefix, *step, v.Line, formatCommand(v))
		*step++
	case Pipe:
		names := make([]string, len(v.Stages))
		for i, s := range v.Stages {
			names[i] = formatCommand(s)
		}
		fmt.Fprintf(b, "%s%d. (line %d) Pipeline: %s\n", prefix, *step, v.Line, strings.Join(names, " | "))
		*step++
	case Cond:
		for i, br := range v.Branches {
			label := "If"
			if i > 0 {
				label = "Else if"
			}
			fmt.Fprintf(b, "%s%d. (line %d) %s %s succeeds:\n", prefix, *step, v.Line, label, formatTest(br.Test))
			*step++
			for _, s := range br.Body {
				explainStatement(b, s, step, indent+1)
			}
		}
		if len(v.Else) > 0 {
			fmt.Fprintf(b, "%s%d. (line %d) Otherwise:\n", prefix, *step, v.Line)
			*step++
			for _, s := range v.Else {
				explainStatement(b, s, step, indent+1)
			}
		}
	case Loop:
		switch v.Kind {
		case LoopFor:
			fmt.Fprintf(b, "%s%d. (line %d) For each %s in [%s]:\n",
				prefix, *step, v.Line, v.Var, strings.Join(v.Words, " "))
		case LoopWhile:
			fmt.Fprintf(b, "%s%d. (line %d) While %s succeeds:\n",
				prefix, *step, v.Line, formatTest(v.Test))
		}
		*step++
		for _, s := range v.Body {
			explainStatement(b, s, step, indent+1)
		}
	case Case:
		fmt.Fprintf(b, "%s%d. (line %d) Match %q against:\n", prefix, *step, v.Line, v.Word)
		*step++
		for _, arm := range v.Arms {
			fmt.Fprintf(b, "%s  %d. Pattern [%s]:\n", prefix, *step, strings.Join(arm.Patterns, " | "))
			*step++
			for _, s := range arm.Body {
				explainStatement(b, s, step, indent+2)
			}
		}
	case FuncDef:
		fmt.Fprintf(b, "%s%d. (line %d) Define function %s:\n", prefix, *step, v.Line, v.Name)
		*step++
		for _, s := range v.Body {
			explainStatement(b, s, step, indent+1)
		}
	case Unknown:
		fmt.Fprintf(b, "%s%d. (line %d) UNSUPPORTED: %s — %q\n", prefix, *step, v.Line, v.Reason, v.Source)
		*step++
	default:
		fmt.Fprintf(b, "%s%d. (line %d) (unrecognised statement type)\n", prefix, *step, Line(st))
		*step++
	}
}

// formatCommand renders a Command back as a readable shell-style
// invocation, including redirects. Used in both Explain and Migrate.
func formatCommand(c Command) string {
	parts := []string{c.Name}
	for _, a := range c.Args {
		parts = append(parts, maybeQuote(a))
	}
	for _, r := range c.Redirects {
		parts = append(parts, redirectString(r))
	}
	return strings.Join(parts, " ")
}

func redirectString(r Redirect) string {
	switch r.Op {
	case RedirectIn:
		return "< " + r.Target
	case RedirectOut:
		return "> " + r.Target
	case RedirectAppend:
		return ">> " + r.Target
	case RedirectErrOut:
		return "2> " + r.Target
	case RedirectErrToOut:
		return "2>&1"
	}
	return "?"
}

// maybeQuote re-quotes a token if it contains shell-meaningful
// whitespace. The reader stripped outer quotes on tokenization;
// emitting un-quoted values that contain spaces would change
// behavior, so we re-quote here.
func maybeQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t|<>;") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// formatTest renders a test (Statement used as the test of an
// `if`/`while`) for human-readable output.
func formatTest(s Statement) string {
	switch v := s.(type) {
	case Command:
		return formatCommand(v)
	case Pipe:
		names := make([]string, len(v.Stages))
		for i, st := range v.Stages {
			names[i] = formatCommand(st)
		}
		return strings.Join(names, " | ")
	case Unknown:
		return "(unsupported: " + v.Reason + ")"
	}
	return "(complex test)"
}
