package translate

import (
	"fmt"
	"io"
	"strings"
)

// Migrate emits an aish-native script equivalent to script. Comments
// are preserved verbatim; Unknown nodes become
//
//	# aish: MIGRATE-TODO: <reason> — <original line>
//
// so the user sees what didn't translate. Control-flow constructs
// emit aish-flavored syntax: bash-style `if … fi` and `for … done`
// stay as-is in MVP (aish's REPL parser is still POSIX-ish), but
// fish-source constructs are re-rendered with their bash equivalent
// so `aish run` can execute them through a single dispatch path.
//
// The migrate engine is deliberately rule-based, NOT LLM-driven —
// that keeps the output reproducible.
func Migrate(w io.Writer, script *Script) error {
	if script == nil {
		return fmt.Errorf("migrate: nil script")
	}
	fmt.Fprintln(w, "#!/usr/bin/env aish")
	fmt.Fprintf(w, "# aish: migrated from %s\n", script.Dialect)
	for _, st := range script.Statements {
		writeMigrateStmt(w, st, 0)
	}
	return nil
}

func writeMigrateStmt(w io.Writer, st Statement, indent int) {
	prefix := strings.Repeat("  ", indent)
	switch v := st.(type) {
	case Comment:
		fmt.Fprintf(w, "%s%s\n", prefix, v.Text)
	case Assign:
		if v.Exported {
			fmt.Fprintf(w, "%sexport %s=%s\n", prefix, v.Name, quoteForMigrate(v.Value))
		} else {
			fmt.Fprintf(w, "%s%s=%s\n", prefix, v.Name, quoteForMigrate(v.Value))
		}
	case Command:
		fmt.Fprintf(w, "%s%s\n", prefix, formatCommand(v))
	case Pipe:
		stages := make([]string, len(v.Stages))
		for i, s := range v.Stages {
			stages[i] = formatCommand(s)
		}
		fmt.Fprintf(w, "%s%s\n", prefix, strings.Join(stages, " | "))
	case Cond:
		for i, br := range v.Branches {
			kw := "if"
			if i > 0 {
				kw = "elif"
			}
			fmt.Fprintf(w, "%s%s %s; then\n", prefix, kw, formatTest(br.Test))
			for _, s := range br.Body {
				writeMigrateStmt(w, s, indent+1)
			}
		}
		if len(v.Else) > 0 {
			fmt.Fprintf(w, "%selse\n", prefix)
			for _, s := range v.Else {
				writeMigrateStmt(w, s, indent+1)
			}
		}
		fmt.Fprintf(w, "%sfi\n", prefix)
	case Loop:
		switch v.Kind {
		case LoopFor:
			fmt.Fprintf(w, "%sfor %s in %s; do\n", prefix, v.Var, strings.Join(quoteEach(v.Words), " "))
		case LoopWhile:
			fmt.Fprintf(w, "%swhile %s; do\n", prefix, formatTest(v.Test))
		}
		for _, s := range v.Body {
			writeMigrateStmt(w, s, indent+1)
		}
		fmt.Fprintf(w, "%sdone\n", prefix)
	case Case:
		fmt.Fprintf(w, "%scase %s in\n", prefix, quoteForMigrate(v.Word))
		for _, arm := range v.Arms {
			fmt.Fprintf(w, "%s  %s)\n", prefix, strings.Join(arm.Patterns, "|"))
			for _, s := range arm.Body {
				writeMigrateStmt(w, s, indent+2)
			}
			fmt.Fprintf(w, "%s  ;;\n", prefix)
		}
		fmt.Fprintf(w, "%sesac\n", prefix)
	case FuncDef:
		fmt.Fprintf(w, "%s%s() {\n", prefix, v.Name)
		for _, s := range v.Body {
			writeMigrateStmt(w, s, indent+1)
		}
		fmt.Fprintf(w, "%s}\n", prefix)
	case Unknown:
		fmt.Fprintf(w, "%s# aish: MIGRATE-TODO: %s — %s\n", prefix, v.Reason, v.Source)
	}
}

func quoteForMigrate(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"'$|<>;()") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func quoteEach(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteForMigrate(s)
	}
	return out
}
