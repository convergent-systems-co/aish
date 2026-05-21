package term

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CompletionContext is what the editor passes to a Completer: the
// token under the cursor, whether the cursor is on the first token of
// the line, and the line buffer's text for any completers that need
// wider context (none yet).
type CompletionContext struct {
	Token      string
	FirstToken bool
	Line       string
}

// Completer is the abstract source of completions. Multiple completers
// compose via NewComposite.
type Completer interface {
	// Complete returns the candidate completions for ctx. The boolean
	// is reserved for future "did this completer have anything to
	// say?" semantics — for now any non-empty slice is also true.
	Complete(ctx CompletionContext) ([]string, bool)
}

// BuiltinCompleter completes the aish built-in command names on the
// first-token slot.
type BuiltinCompleter struct{}

// builtinNames is the canonical list of v0.1/v0.2 built-ins exposed
// by shell.Run.dispatch. Kept in sync by hand for v0.2-1; a future
// change can have the shell package register them on a singleton.
var builtinNames = []string{
	"cache",
	"cd",
	"export",
	"restore",
	"stats",
	"theme",
	"undo",
}

func (BuiltinCompleter) Complete(ctx CompletionContext) ([]string, bool) {
	if !ctx.FirstToken {
		return nil, false
	}
	var out []string
	for _, n := range builtinNames {
		if strings.HasPrefix(n, ctx.Token) {
			out = append(out, n)
		}
	}
	return out, len(out) > 0
}

// BinaryCompleter completes the names of executables found on $PATH
// when the cursor is on the first token. Constructed with an explicit
// dir list so tests don't depend on the real PATH.
type BinaryCompleter struct {
	dirs []string
}

// NewBinaryCompleter returns a completer that scans the given
// directories. Pass the shell env's $PATH (split on os.PathListSeparator)
// in production.
func NewBinaryCompleter(dirs []string) *BinaryCompleter {
	return &BinaryCompleter{dirs: dirs}
}

func (c *BinaryCompleter) Complete(ctx CompletionContext) ([]string, bool) {
	if !ctx.FirstToken {
		return nil, false
	}
	seen := map[string]struct{}{}
	for _, d := range c.dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				continue
			}
			if !strings.HasPrefix(name, ctx.Token) {
				continue
			}
			// Best-effort: assume executable if the bit is set, but
			// don't stat on Windows where the bit means nothing — for
			// v0.2-1 the macOS/Linux path is all that matters.
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.Mode().Perm()&0o111 == 0 {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, len(out) > 0
}

// PathCompleter completes filesystem paths. Directories are returned
// with a trailing `/` so a second Tab can recurse in immediately.
//
// `~/...` paths expand to $HOME; the returned candidate keeps the
// `~/` prefix so the user sees what they typed echoed back.
type PathCompleter struct {
	cwd string
}

// NewPathCompleter binds the completer to a working directory. The
// editor passes the shell's cwd (s.Cwd()) at construction.
func NewPathCompleter(cwd string) *PathCompleter {
	return &PathCompleter{cwd: cwd}
}

func (c *PathCompleter) Complete(ctx CompletionContext) ([]string, bool) {
	tok := ctx.Token
	// Resolve dir-to-list + remaining prefix.
	dir, prefix, displayDir := c.resolve(tok)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		candidate := displayDir + name
		if e.IsDir() {
			candidate += "/"
		}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out, len(out) > 0
}

// resolve splits the user-typed token into (dir-to-list,
// remaining-prefix-to-match, display-prefix-to-prepend).
//
// Examples (cwd = "/home/u"):
//
//	"be"           → ("/home/u",        "be", "")
//	"sub/in"       → ("/home/u/sub",    "in", "sub/")
//	"~/hello"      → ("/home/u",        "hello", "~/")  (with HOME=/home/u)
//	"/etc/pa"      → ("/etc",           "pa", "/etc/")
func (c *PathCompleter) resolve(tok string) (dir, prefix, displayDir string) {
	if strings.HasPrefix(tok, "~/") {
		home := os.Getenv("HOME")
		if home == "" {
			home = c.cwd
		}
		rest := tok[2:]
		idx := strings.LastIndex(rest, "/")
		if idx == -1 {
			return home, rest, "~/"
		}
		return filepath.Join(home, rest[:idx]), rest[idx+1:], "~/" + rest[:idx+1]
	}
	if filepath.IsAbs(tok) {
		idx := strings.LastIndex(tok, "/")
		if idx == 0 {
			return "/", tok[1:], "/"
		}
		return tok[:idx], tok[idx+1:], tok[:idx+1]
	}
	idx := strings.LastIndex(tok, "/")
	if idx == -1 {
		return c.cwd, tok, ""
	}
	return filepath.Join(c.cwd, tok[:idx]), tok[idx+1:], tok[:idx+1]
}

// Composite wraps an ordered set of Completers. Complete invokes each
// and returns the de-duplicated, sorted union.
type Composite struct {
	inner []Completer
}

// NewComposite builds a Composite. The order of wrapped completers is
// not preserved in the result — outputs are merged and re-sorted.
func NewComposite(cs ...Completer) *Composite {
	return &Composite{inner: cs}
}

func (c *Composite) Complete(ctx CompletionContext) ([]string, bool) {
	seen := map[string]struct{}{}
	for _, sub := range c.inner {
		matches, _ := sub.Complete(ctx)
		for _, m := range matches {
			seen[m] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out, len(out) > 0
}

// NewDefaultCompleter wires up the production Composite: built-ins +
// $PATH binaries + filesystem paths. cwd is the shell's working dir;
// pathEnv is the shell's $PATH (already split via filepath.SplitList).
func NewDefaultCompleter(cwd string, pathDirs []string) *Composite {
	return NewComposite(
		BuiltinCompleter{},
		NewBinaryCompleter(pathDirs),
		NewPathCompleter(cwd),
	)
}
