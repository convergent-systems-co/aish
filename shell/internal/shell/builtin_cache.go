package shell

import (
	"fmt"
	"io"
	"strings"
)

// cacheBuiltin implements `cache stats` and `cache clear` per v0.1-2
// task #21. Returns the exit code the dispatch loop should record.
//
// `cache stats`  → prints `Hits: N | Misses: M | Hit rate: P% | Entries: K`
// `cache clear`  → truncates the store and resets the stats counters
//
// Bare `cache` prints a one-line usage hint and exits 2 (POSIX
// convention for "usage error"). Unknown subcommands behave the same.
//
// When the shell has no cache (open failed at New time), every form
// returns "cache: not available" to stderr and exits 1 — the same
// fail-loud-fast posture the theme built-in uses.
func (s *Shell) cacheBuiltin(args []string, stdout, stderr io.Writer) int {
	if s.cacheStore == nil {
		fmt.Fprintln(stderr, "cache: not available (failed to open ~/.aish/cache.db)")
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: cache stats | clear")
		return 2
	}
	switch strings.ToLower(args[0]) {
	case "stats":
		st, err := s.cacheStore.Stats()
		if err != nil {
			fmt.Fprintf(stderr, "cache: stats: %v\n", err)
			return 1
		}
		rate := hitRatePct(st.Hits, st.TotalQueries)
		fmt.Fprintf(stdout, "Hits: %d | Misses: %d | Hit rate: %s | Entries: %d\n",
			st.Hits, st.Misses, rate, st.Entries)
		return 0
	case "clear":
		if err := s.cacheStore.Clear(); err != nil {
			fmt.Fprintf(stderr, "cache: clear: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "cache cleared")
		return 0
	default:
		fmt.Fprintf(stderr, "cache: unknown subcommand %q (try `cache stats` or `cache clear`)\n", args[0])
		return 2
	}
}

// hitRatePct returns the hit-rate as "P%" with one decimal place.
// Returns "n/a" when total is zero so the prompt doesn't print "NaN%".
func hitRatePct(hits, total int64) string {
	if total <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(hits)/float64(total))
}
