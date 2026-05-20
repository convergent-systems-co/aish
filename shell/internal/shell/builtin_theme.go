package shell

import (
	"fmt"
	"io"
	"strings"

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
	home, _ := s.env.Get("HOME")
	if home == "" {
		fmt.Fprintln(stderr, "aish: theme set: $HOME unset; theme active for this session only")
		return 2
	}
	if err := theme.WriteActiveTheme(home, name); err != nil {
		fmt.Fprintf(stderr, "aish: theme set: persist: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "theme: active = %s\n", name)
	return 0
}

func (s *Shell) themePreviewCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aish: theme preview: missing <name>")
		return 1
	}
	name := args[0]
	t, ok := s.themes.Lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "aish: theme preview: no such theme %q (try `theme list`)\n", name)
		return 1
	}
	cwd := "~/projects/aish"
	promptChar := t.Glyph("prompt_char", ">")
	fmt.Fprintf(stdout, "%s %s echo hello\n", t.ColorPrompt(cwd), promptChar)
	fmt.Fprintln(stdout, "hello")
	fmt.Fprintf(stdout, "%s %s\n", t.ColorPrompt(cwd), promptChar)
	return 0
}
