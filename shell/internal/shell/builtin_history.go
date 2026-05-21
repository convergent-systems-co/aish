package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
)

// historyBuiltin implements `history` — the v0.3-4 queryable history
// surface. Subcommands:
//
//	history list [N]              — list the N most-recent events.
//	history show <id>             — print the full payload of one event.
//	history search <query>        — FTS5 / substring search.
//	history purge --before <ts>   — remove events older than <ts>.
//	history checkpoint <name>     — write a named checkpoint.
//	history rollback <name>       — roll back to a named checkpoint.
//
// Returns the exit code; the shell dispatcher binds the result onto
// s.lastExit.
func (s *Shell) historyBuiltin(args []string, stdout, stderr io.Writer) int {
	if s.history == nil {
		fmt.Fprintln(stderr, "aish: history: history not available")
		return 1
	}
	if len(args) == 0 {
		// Bare `history` is a shortcut for `history list`.
		return s.historyList(nil, stdout, stderr)
	}
	switch args[0] {
	case "list":
		return s.historyList(args[1:], stdout, stderr)
	case "show":
		return s.historyShow(args[1:], stdout, stderr)
	case "search":
		return s.historySearch(args[1:], stdout, stderr)
	case "purge":
		return s.historyPurge(args[1:], stdout, stderr)
	case "checkpoint":
		return s.historyCheckpoint(args[1:], stdout, stderr)
	case "rollback":
		return s.historyRollback(args[1:], stdout, stderr)
	case "reindex":
		return s.historyReindex(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		s.historyUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "aish: history: unknown subcommand %q\n", args[0])
		s.historyUsage(stderr)
		return 2
	}
}

func (s *Shell) historyUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  history list [N]")
	fmt.Fprintln(w, "  history show <id>")
	fmt.Fprintln(w, "  history search <query>")
	fmt.Fprintln(w, "  history purge --before <RFC3339-timestamp>")
	fmt.Fprintln(w, "  history checkpoint <name>")
	fmt.Fprintln(w, "  history rollback <name>")
	fmt.Fprintln(w, "  history reindex")
}

// historyReindex implements `aish history reindex` — the v0.3-4 #112
// backfill subcommand. Walks every event and (re-)embeds it under
// the active EmbeddingProvider, skipping tainted commands and rows
// already at the current model_id. Idempotent and resumable; safe
// to interrupt and re-run.
//
// No flags in v0.3 — the subcommand is "do it" or "don't run it."
// Future v0.4 work may add --batch-size and --concurrent (filed as
// #203 follow-up).
func (s *Shell) historyReindex(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "aish: history reindex: usage: history reindex")
		return 2
	}
	store := s.history.Store()
	n, err := store.Reindex(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "aish: history reindex: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "reindexed %d event(s)\n", n)
	return 0
}

func (s *Shell) historyList(args []string, stdout, stderr io.Writer) int {
	limit := 20
	if len(args) >= 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			fmt.Fprintf(stderr, "aish: history list: invalid N %q\n", args[0])
			return 2
		}
		limit = n
	}
	events, err := s.history.Store().List(limit)
	if err != nil {
		fmt.Fprintf(stderr, "aish: history list: %v\n", err)
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "(no history)")
		return 0
	}
	for _, e := range events {
		s.printEventOneLine(stdout, e)
	}
	return 0
}

func (s *Shell) historyShow(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "aish: history show: usage: history show <id>")
		return 2
	}
	ev, err := s.history.Store().Get(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "aish: history show: %v\n", err)
		return 1
	}
	if ev == nil {
		fmt.Fprintf(stderr, "aish: history show: no event with id %q\n", args[0])
		return 1
	}
	s.printEventDetailed(stdout, ev)
	return 0
}

// historySearch routes `history search [--mode={keyword,semantic,
// hybrid}] <query>` to the matching Store method.
//
//   - keyword (FTS5-only) — pre-#112 behavior; #113's surface.
//   - semantic (cosine-only) — v0.3-4 #112; requires an attached
//     embedder + vector store. Empty vector store → "run
//     `aish history reindex`" hint.
//   - hybrid (default) — RRF k=60 fusion of FTS + cosine. Degrades
//     to keyword when no embedder / vec is attached.
//
// Default mode is `hybrid` per AC5; a binary with no embedder
// configured therefore lands on the FTS path with no error, which
// matches the pre-#112 user experience.
func (s *Shell) historySearch(args []string, stdout, stderr io.Writer) int {
	mode := "hybrid"
	rest := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--mode="):
			mode = strings.TrimPrefix(a, "--mode=")
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "aish: history search: usage: history search [--mode={keyword,semantic,hybrid}] <query>")
		return 2
	}
	switch mode {
	case "keyword", "semantic", "hybrid":
		// ok
	default:
		fmt.Fprintf(stderr, "aish: history search: unknown mode %q (want keyword, semantic, or hybrid)\n", mode)
		return 2
	}
	// Join multi-word queries so `history search rm tmp` works without
	// requiring the user to quote.
	query := strings.Join(rest, " ")
	store := s.history.Store()

	var (
		events []*history.Event
		err    error
	)
	switch mode {
	case "keyword":
		events, err = store.Search(query, 50)
	case "semantic":
		events, err = store.SemanticSearch(query, 50)
		if errors.Is(err, history.ErrNoVectors) {
			// Friendly hint rather than a raw error dump. The exit
			// code is non-zero so scripts can branch on "feature
			// not enabled yet" without parsing strings.
			fmt.Fprintln(stderr, "aish: history search: semantic mode requires vectors — run `aish history reindex` after configuring an embedder")
			return 1
		}
	case "hybrid":
		events, err = store.HybridSearch(query, 50)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aish: history search: %v\n", err)
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "(no matches)")
		return 1
	}
	for _, e := range events {
		s.printEventOneLine(stdout, e)
	}
	return 0
}

func (s *Shell) historyPurge(args []string, stdout, stderr io.Writer) int {
	// Single supported selector: --before <RFC3339>. Additional
	// selectors (count, size, age) are filed as v0.3-4.2 follow-ups
	// per the plan.
	if len(args) != 2 || args[0] != "--before" {
		fmt.Fprintln(stderr, "aish: history purge: usage: history purge --before <RFC3339-timestamp>")
		return 2
	}
	ts, err := time.Parse(time.RFC3339, args[1])
	if err != nil {
		fmt.Fprintf(stderr, "aish: history purge: invalid timestamp: %v\n", err)
		return 2
	}
	n, err := s.history.Store().Purge(ts)
	if err != nil {
		fmt.Fprintf(stderr, "aish: history purge: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "purged %d event(s)\n", n)
	return 0
}

func (s *Shell) historyCheckpoint(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(stderr, "aish: history checkpoint: usage: history checkpoint <name>")
		return 2
	}
	cp, err := s.history.Store().Checkpoint(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "aish: history checkpoint: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "checkpoint %q recorded (id=%s)\n", cp.Name, cp.ID)
	return 0
}

func (s *Shell) historyRollback(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(stderr, "aish: history rollback: usage: history rollback <name>")
		return 2
	}
	name := args[0]
	cp, err := s.history.Store().CheckpointByName(name)
	if err != nil {
		fmt.Fprintf(stderr, "aish: history rollback: %v\n", err)
		return 1
	}
	if cp == nil {
		fmt.Fprintf(stderr, "aish: history rollback: no checkpoint named %q\n", name)
		return 1
	}
	events, err := s.history.Store().EventsSinceCheckpoint(cp)
	if err != nil {
		fmt.Fprintf(stderr, "aish: history rollback: %v\n", err)
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintf(stdout, "rollback: no events after checkpoint %q\n", name)
		return 0
	}
	// EventsSinceCheckpoint returns newest-first; we apply
	// newest-to-oldest so each restore reverses the immediately-prior
	// destructive operation. This mirrors how `undo` walks one event
	// at a time, just multiple events in sequence.
	restored := 0
	failed := 0
	for _, ev := range events {
		if err := s.history.RestoreEvent(ev); err != nil {
			fmt.Fprintf(stderr, "aish: history rollback: event %s: %v\n", ev.ID, err)
			failed++
			continue
		}
		restored++
	}
	fmt.Fprintf(stdout, "rollback: restored %d event(s), %d failed\n", restored, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// ---- formatting helpers ----

// printEventOneLine writes one event as a compact list row. Shape:
//
//	<ts> <kind> <id> <command>  (persona=<name>)
//
// Timestamps render in the host's local time at second resolution
// for readability; the full RFC3339 form is available via `history
// show <id>`.
//
// v0.3-5.1 (#125): the trailing "(persona=…)" suffix surfaces the
// persona that was active when the event was recorded. Events older
// than the sidecar (pre-v0.3-5.1 rows) render as "(persona=?)" —
// "default" means a persona was recorded; "?" means no row exists.
func (s *Shell) printEventOneLine(w io.Writer, e *history.Event) {
	if e == nil {
		return
	}
	ts := e.Timestamp.Local().Format("2006-01-02 15:04:05")
	kind := string(e.Kind)
	if kind == "" {
		kind = "?"
	}
	fmt.Fprintf(w, "%s  %-10s  %s  %s  (persona=%s)\n",
		ts, kind, e.ID, e.Command, s.eventPersonaTag(e.ID))
}

// eventPersonaTag returns the persona attribution string suitable for
// inline display. "?" when no sidecar row exists for the event; the
// recorded name otherwise (which is "default" for events recorded
// with no active persona).
func (s *Shell) eventPersonaTag(eventID string) string {
	if s == nil || s.personaMeta == nil {
		return "?"
	}
	if name, ok := s.personaMeta.Lookup(eventID); ok {
		return name
	}
	return "?"
}

// printEventDetailed dumps every field of an event in a human-readable
// form. Used by `history show <id>`. Signature is reported as
// "(unsigned)" when missing so the user knows pre-v0.3-4 events
// migrated forward unsigned.
//
// v0.3-5.1 (#125): an additional "persona:" line renders the persona
// active at command time, or "(none recorded)" for pre-v0.3-5.1 events.
func (s *Shell) printEventDetailed(w io.Writer, e *history.Event) {
	if e == nil {
		return
	}
	fmt.Fprintf(w, "id:        %s\n", e.ID)
	fmt.Fprintf(w, "timestamp: %s\n", e.Timestamp.Format(time.RFC3339Nano))
	fmt.Fprintf(w, "kind:      %s\n", e.Kind)
	if e.Name != "" {
		fmt.Fprintf(w, "name:      %s\n", e.Name)
	}
	fmt.Fprintf(w, "command:   %s\n", e.Command)
	if tag := s.eventPersonaTag(e.ID); tag != "?" {
		fmt.Fprintf(w, "persona:   %s\n", tag)
	} else {
		fmt.Fprintf(w, "persona:   (none recorded)\n")
	}
	if e.Cwd != "" {
		fmt.Fprintf(w, "cwd:       %s\n", e.Cwd)
	}
	if e.ExitCode != nil {
		fmt.Fprintf(w, "exit_code: %d\n", *e.ExitCode)
	} else {
		fmt.Fprintf(w, "exit_code: (in-flight)\n")
	}
	if e.DurationMS > 0 {
		fmt.Fprintf(w, "duration:  %dms\n", e.DurationMS)
	}
	if e.Signature != "" {
		fmt.Fprintf(w, "signer:    %s\n", e.SignerID)
		fmt.Fprintf(w, "signature: %s\n", e.Signature)
	} else {
		fmt.Fprintf(w, "signature: (unsigned)\n")
	}
	if len(e.Affected) > 0 {
		fmt.Fprintln(w, "affected:")
		for _, a := range e.Affected {
			fmt.Fprintf(w, "  - %s  op=%s", a.Path, a.Op)
			if a.RenameTarget != "" {
				fmt.Fprintf(w, "  -> %s", a.RenameTarget)
			}
			if a.SkipReason != "" {
				fmt.Fprintf(w, "  (%s)", a.SkipReason)
			}
			if a.Bytes > 0 {
				fmt.Fprintf(w, "  bytes=%d", a.Bytes)
			}
			fmt.Fprintln(w)
		}
	}
}
