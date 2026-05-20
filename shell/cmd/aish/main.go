// Command aish is the AI-native, shell-native, OS-insensitive, fully
// reversible shell. v0.1-1 ships the minimum exec path; later epics layer
// the intent cache, plugin contract, history engine, and personas on top.
//
// See GOALS.md for the full architecture.
package main

import (
	"fmt"
	"os"

	"github.com/convergent-systems-co/aish/shell/internal/shell"
)

// Build-time identity, populated via -ldflags by the Makefile.
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("aish %s (built %s)\n", version, buildTime)
			return
		case "--help", "-h":
			fmt.Println("aish — AI-native, OS-insensitive, reversible shell")
			fmt.Println("Usage: aish [--version|--help]")
			return
		}
	}
	s := shell.New()
	if err := s.Run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "aish: %v\n", err)
		os.Exit(1)
	}
}
