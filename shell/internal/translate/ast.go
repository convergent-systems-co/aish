// Package translate reads scripts in supported shell dialects (bash,
// zsh, fish) into a shared AST, and provides three downstream engines
// over that AST:
//
//   - Run     — execute each statement via a Runner abstraction (the
//               shell wires this to its normal dispatch tier).
//   - Explain — emit a deterministic, numbered, plain-language
//               description (optional LLM enrichment is gated).
//   - Migrate — emit an aish-native script that, when run via
//               `aish run`, reproduces the observable behavior of
//               the input script.
//
// MVP scope ("the 80% list") is documented in the package plan at
// .artifacts/plans/v0.2-4.md and surfaced through `Unknown` nodes
// for everything outside it — so downstream engines never silently
// drop a construct.
package translate

// Dialect identifies the source shell of a script. Used by the
// reader-dispatch (translate.Read) and by Migrate so it can emit
// dialect-aware translations when needed.
type Dialect string

const (
	DialectBash Dialect = "bash"
	DialectZsh  Dialect = "zsh"
	DialectFish Dialect = "fish"
)

// Script is a parsed source file. Dialect is preserved so downstream
// engines can branch (rarely, but e.g. Migrate's variable-assignment
// translation differs between fish's `set` form and bash's `VAR=val`).
type Script struct {
	Dialect    Dialect
	Statements []Statement
}

// Statement is the sum-type interface every AST node implements.
// Line is the 1-indexed source line of the statement's first token —
// used in error messages ("line N: cannot translate (heredoc
// unsupported)") and in numbered explain output.
type Statement interface {
	stmtLine() int
}

// BaseStmt provides the embedded Line field every node needs without
// repeating the boilerplate stmtLine() implementation in every type.
// Exported so the reader package can populate it.
type BaseStmt struct {
	Line int
}

func (b BaseStmt) stmtLine() int { return b.Line }

// Comment is a `# …` line preserved verbatim through migrate. At run
// time it is a no-op.
type Comment struct {
	BaseStmt
	Text string // includes the leading `#` and any internal whitespace
}

// Assign is a variable assignment. In bash/zsh this is `VAR=value`
// (no spaces around `=`); in fish it is `set [-l|-g|-x] VAR value`.
// Exported records whether the variable is exported to children
// (fish: `-x`; bash/zsh: only `export VAR=value` sets this — bare
// `VAR=value` does not export).
type Assign struct {
	BaseStmt
	Name     string
	Value    string
	Exported bool
}

// Command is a single program invocation. Args are post-quote-strip
// tokens; Redirects (if any) are attached. CommandSubs holds any
// nested `$(...)` substitutions detected at parse time — the run
// engine evaluates them recursively. Empty when none were present.
type Command struct {
	BaseStmt
	Name      string
	Args      []string
	Redirects []Redirect
}

// Redirect is one stream-redirection operator on a command.
type Redirect struct {
	Op     RedirectOp
	Target string // filename (or fd for `>&2`, etc. — not in MVP)
}

// RedirectOp enumerates the redirection forms we recognise. Anything
// outside this set (heredocs, here-strings, fd-to-fd) is surfaced as
// an Unknown statement instead of a partially-parsed Redirect.
type RedirectOp int

const (
	RedirectIn       RedirectOp = iota // `<  file`
	RedirectOut                        // `>  file`
	RedirectAppend                     // `>> file`
	RedirectErrOut                     // `2> file`
	RedirectErrToOut                   // `2>&1`
)

// Pipe is a sequence of two or more Commands joined by `|`. The
// reader emits a Pipe only when n >= 2; a single command stays a
// Command.
type Pipe struct {
	BaseStmt
	Stages []Command
}

// Cond is `if … then … [elif …] [else …] fi` (bash/zsh) or
// `if … <body> [else if …] [else …] end` (fish). Each branch carries
// its test command (Test) and the body executed when the test
// succeeds; Else (if non-empty) runs when no branch matched.
type Cond struct {
	BaseStmt
	Branches []CondBranch
	Else     []Statement
}

// CondBranch is one `if` or `elif` arm. Test is the command line whose
// exit status decides whether Body runs. For fish we still phrase it
// the same way — `if test -f foo` works in both dialects.
type CondBranch struct {
	Test Statement // typically a Command; can be a Pipe
	Body []Statement
}

// Loop covers `for VAR in WORDS; do … done` and `while CMD; do …
// done`. Kind distinguishes them so Explain and Migrate can speak
// the right English / emit the right form.
type Loop struct {
	BaseStmt
	Kind  LoopKind
	Var   string      // empty for while
	Words []string    // empty for while
	Test  Statement   // populated for while; nil for for
	Body  []Statement // either kind
}

type LoopKind int

const (
	LoopFor LoopKind = iota
	LoopWhile
)

// Case is `case WORD in PAT) … ;; … esac`. Patterns are the literal
// glob expressions before `)`; the parser does NOT expand them.
type Case struct {
	BaseStmt
	Word  string
	Arms  []CaseArm
}

type CaseArm struct {
	Patterns []string // ["*.go", "*.sh"] for `*.go|*.sh)`
	Body     []Statement
}

// FuncDef is a named function. Body is the statements between the
// braces (bash/zsh) or between `function … end` (fish).
type FuncDef struct {
	BaseStmt
	Name string
	Body []Statement
}

// Unknown is the bookkeeping node for anything the reader saw but
// could not classify: heredocs, arrays, `[[ … ]]`, `&&`/`||` chains,
// process substitution, etc. Reason names the construct so downstream
// surface messages are actionable; Source is the original line(s) so
// Migrate can echo them as TODO comments.
//
// The MVP rule is: Unknown nodes propagate. Run reports them and
// aborts (cleanly, with exit 2); Explain says "(line N) unsupported
// construct: <reason>"; Migrate emits a `# aish: MIGRATE-TODO:`
// comment with the original source.
type Unknown struct {
	BaseStmt
	Reason string
	Source string
}

// WithLine sets the source line on every node. Returned by value so
// the reader can chain it in a single expression. This keeps the
// reader's prose readable — `translate.Comment{Text: …}.WithLine(n)`
// reads top-to-bottom.

func (n Comment) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n Assign) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n Command) WithLine(line int) Command {
	n.BaseStmt.Line = line
	return n
}

func (n Pipe) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n Cond) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n Loop) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n Case) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n FuncDef) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

func (n Unknown) WithLine(line int) Statement {
	n.BaseStmt.Line = line
	return n
}

// Line returns the source line number of a statement. Used by
// downstream engines for messages and ordering.
func Line(s Statement) int {
	return s.stmtLine()
}
