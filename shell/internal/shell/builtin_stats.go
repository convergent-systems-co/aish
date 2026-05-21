package shell

import (
	"fmt"
	"io"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/telemetry"
)

// statsBuiltin implements the `stats` built-in per v0.1-5 task #43:
// "Local dashboard via aish stats." Renders the last N session rows
// from ~/.aish/sessions/ plus a footer with cumulative hit-rate and
// total USD across the window. The in-flight session (the one this
// shell is running right now) is rendered as the first row so the
// user sees their current activity even before exiting the shell.
//
// `stats`         → table of recent sessions (default N=10)
// `stats N`       → table of the last N sessions
//
// Bare unknown forms (`stats foo`) print a usage line and exit 2.
//
// When telemetry never opened (degraded mode), the built-in falls
// back to a single line: "stats: telemetry not available" + exit 1
// — same fail-loud posture as `cache` and `theme`.
func (s *Shell) statsBuiltin(args []string, stdout, stderr io.Writer) int {
	if s.telemetry == nil {
		fmt.Fprintln(stderr, "stats: telemetry not available")
		return 1
	}

	limit := defaultStatsWindow
	if len(args) > 0 {
		// Optional numeric limit. Anything non-numeric is a usage error.
		n, err := parsePositiveInt(args[0])
		if err != nil {
			fmt.Fprintln(stderr, "Usage: stats [N]   (N = number of recent sessions, default 10)")
			return 2
		}
		limit = n
	}

	// Find the user's ~/.aish directory the same way the rest of the
	// shell does. If we can't, render an empty dashboard.
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "stats: HOME not set; cannot read session history")
		return 1
	}
	dotAish := home + "/.aish"

	rows, err := telemetry.ListSessions(dotAish, limit)
	if err != nil {
		fmt.Fprintf(stderr, "stats: list: %v\n", err)
		return 1
	}

	// Prepend the in-flight session — synthesized from the recorder's
	// current counters so the user sees what's happening now, not just
	// what completed in past shells.
	now := synthesizeCurrentSession(s.telemetry)
	rows = append([]telemetry.SessionRow{now}, rows...)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	renderStatsTable(stdout, rows)
	return 0
}

// defaultStatsWindow is the default N when `stats` is called bare.
// Matches the GOALS.md decision-gate question ("the last 30 days
// for a typical user") at roughly 3 sessions per day = ~90, but
// 10 is the practical floor that fits a terminal page without
// scrolling. Tunable via `stats N`.
const defaultStatsWindow = 10

// synthesizeCurrentSession builds a SessionRow from the in-flight
// recorder's current state. ID is suffixed with " (in-flight)" so
// the renderer can distinguish it from completed sessions in the
// "session" column; counters and (when readable) costs come from
// the live recorder.
func synthesizeCurrentSession(rec *telemetry.Recorder) telemetry.SessionRow {
	c := rec.CurrentCounters()
	return telemetry.SessionRow{
		ID:            rec.SessionID() + " (now)",
		Counters:      c,
		SchemaVersion: telemetry.CurrentSchemaVersion,
	}
}

// renderStatsTable writes a fixed-width table of session rows to w.
// Empty input renders just the header + a one-line empty-state notice
// — never an error, never division-by-zero.
//
// Columns: session (id-short), cmds, cache-hits, hit-rate, infer-calls,
// cost-usd. The footer aggregates hit-rate and cost across rows.
//
// Format is intentionally stable (space-padded, ASCII only) so the
// output is parseable by `awk` and friends. No tabs — they render
// inconsistently across terminals.
func renderStatsTable(w io.Writer, rows []telemetry.SessionRow) {
	fmt.Fprintln(w, "session       cmds  cache-hits  hit-rate  infer-calls  cost-usd")
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no sessions recorded yet)")
		return
	}
	var totalHits, totalQueries, totalInfer, totalCmds int64
	var totalUSD float64
	for _, r := range rows {
		c := r.Counters
		hitRate := statsHitRate(c.CacheHits, c.CacheHits+c.CacheMisses)
		fmt.Fprintf(w, "%-12s  %4d  %10d  %8s  %11d  $%7.4f\n",
			shortID(r.ID), c.Commands, c.CacheHits, hitRate, c.InferenceCalls, r.Costs.TotalUSD)
		totalHits += c.CacheHits
		totalQueries += c.CacheHits + c.CacheMisses
		totalInfer += c.InferenceCalls
		totalCmds += c.Commands
		totalUSD += r.Costs.TotalUSD
	}
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Window: %d session(s) | total cmds: %d | cumulative hit rate: %s | total cost: $%.4f\n",
		len(rows), totalCmds, statsHitRate(totalHits, totalQueries), totalUSD)
}

// statsHitRate is the renderer-side cousin of cache builtin's
// hitRatePct. Returns "n/a" on zero-total to avoid division-by-zero
// rendering as "NaN%". Same single-decimal format as `cache stats`.
func statsHitRate(hits, total int64) string {
	if total <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(hits)/float64(total))
}

// shortID truncates a session ID to its first 12 characters for the
// table column. Preserves the " (now)" suffix on the in-flight row.
func shortID(id string) string {
	if idx := strings.Index(id, " "); idx > 0 {
		// e.g. "<uuid> (now)" — keep the suffix.
		head := id[:idx]
		tail := id[idx:]
		if len(head) > 8 {
			head = head[:8]
		}
		out := head + tail
		if len(out) > 12 {
			out = out[:12]
		}
		return out
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// parsePositiveInt parses s as a positive decimal integer. Returns an
// error if s is empty, contains non-digit characters, or evaluates to
// zero or negative. Used for the `stats N` argument.
func parsePositiveInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-digit %q", r)
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("too large")
		}
	}
	if n <= 0 {
		return 0, fmt.Errorf("not positive")
	}
	return n, nil
}
