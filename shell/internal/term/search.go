package term

import "strings"

// Search is the state machine behind Ctrl-R reverse history search.
//
// Lifecycle:
//
//  1. The editor constructs a Search on Ctrl-R, stashing the current
//     buffer.
//  2. Each typed rune is fed via AppendQuery; the search refines.
//  3. Ctrl-R re-pressed advances Next to the next-older match.
//  4. Enter accepts: editor reads Current and dispatches.
//  5. Esc cancels: editor restores the stash.
//
// Matching: case-insensitive substring. Match priority: most-recent
// entry first.
type Search struct {
	src   HistorySource
	query []rune
	// idx is the source index of the current match, or -1 when no
	// match.
	idx int
}

// NewSearch returns a Search starting in "no query, no match" state.
func NewSearch(src HistorySource) *Search {
	return &Search{src: src, idx: -1}
}

// Query returns the query buffer (for rendering the search prompt).
func (s *Search) Query() string { return string(s.query) }

// Current returns the matching history entry, or "" when there is
// no match (or the query is empty).
func (s *Search) Current() string {
	if s.idx < 0 || len(s.query) == 0 {
		return ""
	}
	return s.src.At(s.idx)
}

// AppendQuery extends the query and re-runs the search from the
// newest entry. Re-running from newest (not from the previous match)
// is the readline convention.
func (s *Search) AppendQuery(r rune) {
	s.query = append(s.query, r)
	s.idx = s.findFrom(s.src.Len() - 1)
}

// Backspace shortens the query by one rune. An empty query forces
// idx == -1 so the editor can render an empty result.
func (s *Search) Backspace() {
	if len(s.query) == 0 {
		s.idx = -1
		return
	}
	s.query = s.query[:len(s.query)-1]
	if len(s.query) == 0 {
		s.idx = -1
		return
	}
	s.idx = s.findFrom(s.src.Len() - 1)
}

// Next advances to the next-older match. Returns false when there is
// no further match (Ctrl-R is now a no-op).
func (s *Search) Next() bool {
	if s.idx <= 0 || len(s.query) == 0 {
		return false
	}
	next := s.findFrom(s.idx - 1)
	if next < 0 {
		return false
	}
	s.idx = next
	return true
}

// findFrom walks from index `start` toward 0, returning the first
// entry that contains the query (case-insensitive). -1 on no match.
func (s *Search) findFrom(start int) int {
	q := strings.ToLower(string(s.query))
	for i := start; i >= 0; i-- {
		entry := strings.ToLower(s.src.At(i))
		if strings.Contains(entry, q) {
			return i
		}
	}
	return -1
}
