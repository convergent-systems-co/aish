// Command aish is the AI-native, shell-native, OS-insensitive, fully
// reversible shell. v0.1-1 ships the minimum exec path; later epics layer
// the intent cache, plugin contract, history engine, and personas on top.
//
// v0.3-1 adds login-shell capabilities: `-l` / `--login` / dash-argv[0]
// trigger RC sourcing and POSIX env defaults; `logout` and `exec`
// built-ins terminate the shell cleanly via typed sentinels.
//
// See GOALS.md for the full architecture.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/shell"
)

// Build-time identity, populated via -ldflags by the Makefile.
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	// v0.3-1 login-shell detection. Three triggers, any of which
	// flips the shell into login mode:
	//
	//   1. argv[0] starts with `-` (POSIX login(8) convention).
	//   2. `-l` is present in argv.
	//   3. `--login` is present in argv.
	//
	// `--version` / `--help` short-circuits still win — they're
	// pre-existing behavior we don't want to break.
	loginMode := false
	if len(os.Args) > 0 && len(os.Args[0]) > 0 {
		// Strip directory components so `/bin/-aish` and `-aish`
		// both register. Then check the basename's first byte.
		base := os.Args[0]
		if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
			base = base[i+1:]
		}
		if len(base) > 0 && base[0] == '-' {
			loginMode = true
		}
	}
	for _, a := range os.Args[1:] {
		switch a {
		case "--version", "-v":
			fmt.Printf("aish %s (built %s)\n", version, buildTime)
			return
		case "--help", "-h":
			fmt.Println("aish — AI-native, OS-insensitive, reversible shell")
			fmt.Println("Usage: aish [--version|--help|-l|--login]")
			return
		case "-l", "--login":
			loginMode = true
		}
	}

	s := shell.NewWithOptions(shell.Options{
		Login:   loginMode,
		Version: version,
		Stderr:  os.Stderr,
	})
	defer s.Close()
	err := s.Run(os.Stdin, os.Stdout, os.Stderr)
	// v0.3-1: `logout [n]` and (Windows) `exec` propagate typed
	// sentinels that carry an exit code. Honor them so the parent
	// sees the right $? — bash does the same.
	if code, ok := shell.IsLogout(err); ok {
		os.Exit(code)
	}
	if code, ok := shell.IsExecReplaced(err); ok {
		os.Exit(code)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "aish: %v\n", err)
		os.Exit(1)
	}
}
