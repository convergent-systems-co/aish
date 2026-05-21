package shell

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/theme"
)

// themeBuiltin handles the `theme` REPL built-in. Returns the exit code
// to record in $?.
//
// Sub-commands:
//
//	(none)          print usage
//	list            list bundled + loaded themes, with an asterisk on active
//	show [<name>]   show details of <name> (or the active theme)
//	set <name>      atomic activation + persist to ~/.aish/config.toml
//	preview <name>  render a sample prompt with <name> without activating
//
// Errors go to stderr; usage goes to stdout. Exit codes: 0 success,
// 1 user error, 2 internal/persistence error.
func (s *Shell) themeBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, themeUsage())
		return 0
	}

	switch args[0] {
	case "list":
		return s.themeListCmd(stdout)
	case "show":
		return s.themeShowCmd(args[1:], stdout, stderr)
	case "set":
		return s.themeSetCmd(args[1:], stdout, stderr)
	case "preview":
		return s.themePreviewCmd(args[1:], stdout, stderr)
	case "sync":
		return s.themeSyncCmd(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, themeUsage())
		return 0
	default:
		fmt.Fprintf(stderr, "aish: theme: unknown sub-command %q\n", args[0])
		fmt.Fprintln(stderr, themeUsage())
		return 1
	}
}

func themeUsage() string {
	return strings.TrimSpace(`
Usage: theme <subcommand>

  list                  list installed themes (active is marked with *)
  show [<name>]         show details of a theme (defaults to the active one)
  set <name>            activate <name> and persist to ~/.aish/config.toml
  preview <name>        render a sample prompt with <name> without activating
  sync [<id>...]        fetch theme bundles from the Brand-Atoms registry
                        (default https://theme-atoms.com; override with
                        $AISH_BRAND_REGISTRY). With no args, fetches the
                        full catalog; otherwise fetches only the named IDs.
                        Cached at ~/.aish/themes/cache/<id>.toml.
`)
}

func (s *Shell) themeListCmd(stdout io.Writer) int {
	reg := s.themes
	active := reg.Active().Name()
	for _, name := range reg.List() {
		marker := "  "
		if name == active {
			marker = "* "
		}
		fmt.Fprintf(stdout, "%s%s\n", marker, name)
	}
	return 0
}

func (s *Shell) themeShowCmd(args []string, stdout, stderr io.Writer) int {
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		fmt.Fprintln(stdout, s.themes.Active().Inspect())
		return 0
	}
	t, ok := s.themes.Lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "aish: theme: no such theme %q (try `theme list`)\n", name)
		return 1
	}
	fmt.Fprintln(stdout, t.Inspect())
	return 0
}

func (s *Shell) themeSetCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aish: theme set: missing <name>")
		return 1
	}
	name := args[0]
	if err := s.themes.SetActive(name); err != nil {
		fmt.Fprintf(stderr, "aish: theme set: %v\n", err)
		return 1
	}
	// Persist. Failures here don't undo the in-process activation; they
	// just mean the next aish invocation won't remember the choice.
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "aish: theme set: $HOME / $USERPROFILE unset; theme active for this session only")
		return 2
	}
	if err := theme.WriteActiveTheme(home, name); err != nil {
		fmt.Fprintf(stderr, "aish: theme set: persist: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "theme: active = %s\n", name)
	return 0
}

func (s *Shell) themeSyncCmd(args []string, stdout, stderr io.Writer) int {
	url, _ := s.env.Get("AISH_BRAND_REGISTRY")
	c := theme.NewClient(url, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Fprintf(stdout, "syncing from %s ...\n", c.BaseURL())
	res, err := c.Sync(ctx, s.env.Environ(), s.themes, theme.SyncOptions{
		Only: args,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aish: theme sync: %v\n", err)
		return 2
	}

	if len(res.Cached) > 0 {
		ids := append([]string(nil), res.Cached...)
		sort.Strings(ids)
		fmt.Fprintf(stdout, "cached: %s\n", strings.Join(ids, ", "))
	}
	if len(res.Registered) > 0 {
		ids := append([]string(nil), res.Registered...)
		sort.Strings(ids)
		fmt.Fprintf(stdout, "registered: %s\n", strings.Join(ids, ", "))
	}
	if len(res.Errors) > 0 {
		// Sort for stable output.
		ids := make([]string, 0, len(res.Errors))
		for k := range res.Errors {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(stderr, "  %s: %v\n", id, res.Errors[id])
		}
		if len(res.Cached) == 0 {
			return 1
		}
	}
	return 0
}

// themePreviewCmd renders a self-contained, non-activating preview of
// a brand. The output (#79) includes the brand name as a header, all
// roles as swatches, the glyph table, the configured segments, and a
// sample prompt line. A leading `--plain` flag suppresses ANSI escapes
// so the preview can be captured into a snapshot test or diffed.
//
// Argument forms:
//
//	theme preview <name>
//	theme preview --plain <name>
//
// The preview MUST NOT mutate `s.themes.Active()` — that's the contract
// difference vs `theme set` (TestThemePreview_DoesNotActivate).
func (s *Shell) themePreviewCmd(args []string, stdout, stderr io.Writer) int {
	plain := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--plain" {
			plain = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "aish: theme preview: missing <name>")
		return 1
	}
	name := rest[0]
	t, ok := s.themes.Lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "aish: theme preview: no such theme %q (try `theme list`)\n", name)
		return 1
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "── theme preview: %s ──\n", t.Name())
	buf.WriteString(t.Inspect())

	cwd := "~/projects/aish"
	promptChar := t.Glyph("prompt_char", ">")
	fmt.Fprintf(&buf, "\nSample prompt:\n  %s %s echo hello\n", t.ColorPrompt(cwd), promptChar)
	buf.WriteString("  hello\n")

	out := buf.String()
	if plain {
		out = stripThemeANSI(out)
	}
	fmt.Fprint(stdout, out)
	return 0
}

// stripThemeANSI removes CSI sequences from s. Used by `theme preview
// --plain` so the output is capture-friendly and diff-stable. The
// theme renderer only emits 24-bit foreground escapes today; a minimal
// CSI scanner is sufficient.
func stripThemeANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j - 1
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
