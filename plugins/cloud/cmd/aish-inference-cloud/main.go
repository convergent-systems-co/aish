// Command aish-inference-cloud is the Anthropic Cloud inference plugin
// for aish. It reads JSON-RPC requests on stdin, dispatches to handlers,
// and writes NDJSON responses on stdout.
//
// Configuration:
//
//	ANTHROPIC_API_KEY   required; auth for api.anthropic.com
//	ANTHROPIC_BASE_URL  optional; override the base URL (test stubs etc.)
//	AISH_COST_LOG       optional; path to the JSONL cost log
//
// See libs/proto/inference for the wire-protocol types.
package main

import (
	"fmt"
	"os"

	"github.com/convergent-systems-co/aish/plugins/cloud/internal/rpc"
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
			fmt.Printf("aish-inference-cloud %s (built %s)\n", version, buildTime)
			return
		case "--help", "-h":
			fmt.Println("aish-inference-cloud — Anthropic Cloud inference plugin for aish")
			fmt.Println("")
			fmt.Println("Usage: aish-inference-cloud")
			fmt.Println("")
			fmt.Println("Reads JSON-RPC requests on stdin (NDJSON), writes responses on stdout.")
			fmt.Println("")
			fmt.Println("Env vars:")
			fmt.Println("  ANTHROPIC_API_KEY    required")
			fmt.Println("  ANTHROPIC_BASE_URL   optional (override endpoint)")
			fmt.Println("  AISH_COST_LOG        optional (default ~/.aish/cost-log.jsonl)")
			return
		}
	}

	d := rpc.NewDispatcher(os.Stdin, os.Stdout, os.Stderr)
	if err := d.Run(); err != nil {
		// Errors from the dispatcher are unrecoverable I/O on our pipes.
		// Per Common.md §4, never leak the API key into the error path —
		// the dispatcher does not see it; only the anthropic client does.
		fmt.Fprintf(os.Stderr, "aish-inference-cloud: %v\n", err)
		os.Exit(1)
	}
}
