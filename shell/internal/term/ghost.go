package term

import "strings"

// Suggest returns the suffix of the most-recent history entry that
// has the given input as a prefix. Returns "" when:
//
//   - input is empty,
//   - no entry has input as a prefix,
//   - the matching entry is exactly equal to input (no suffix to show).
//
// Most-recent wins: we walk the source from newest to oldest and stop
// at the first match.
//
// TODO(v0.2-2): Augment with cache-driven suggestions when
// shell/internal/cache grows an FTS index over intents. See the
// "Alternatives Table — ghost-text suggestion source" in the v0.2-1
// plan for the trade-off and the rejected debounce design.
func Suggest(src HistorySource, input string) string {
	if input == "" || src == nil {
		return ""
	}
	for i := src.Len() - 1; i >= 0; i-- {
		entry := src.At(i)
		if entry == input {
			return ""
		}
		if strings.HasPrefix(entry, input) {
			return entry[len(input):]
		}
	}
	return ""
}
