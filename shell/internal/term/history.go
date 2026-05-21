package term

// HistorySource is the abstract sequence of prior commands the editor
// can recall. Both ghost-text and history-up/down read from this.
//
// For v0.2-1 the production wiring uses *MemorySource, populated by
// the shell as each command is accepted. A future change can swap in
// a disk-backed implementation (e.g. wrapping history.Store) without
// touching the editor — that's why this is an interface.
type HistorySource interface {
	// Len returns the number of recorded entries.
	Len() int
	// At returns the entry at index i (0 is oldest, Len()-1 newest).
	At(i int) string
	// Append records a new entry as the newest.
	Append(line string)
}

// MemorySource is a slice-backed HistorySource. Safe for use within a
// single goroutine; the REPL is single-threaded.
type MemorySource struct {
	entries []string
}

// NewMemorySource returns a MemorySource seeded with the given lines
// in order (lines[0] becomes the oldest entry).
func NewMemorySource(initial []string) *MemorySource {
	cp := make([]string, len(initial))
	copy(cp, initial)
	return &MemorySource{entries: cp}
}

func (m *MemorySource) Len() int           { return len(m.entries) }
func (m *MemorySource) At(i int) string    { return m.entries[i] }
func (m *MemorySource) Append(line string) { m.entries = append(m.entries, line) }

// HistoryNav holds the editor's position in a HistorySource for one
// ReadLine session. The navigation cursor is held separately from the
// underlying source so concurrent Appends (in theory) don't corrupt
// it — though in practice the editor is single-threaded.
//
// State machine:
//
//   - idx == Len() — "at the stash" (the pre-nav buffer); Down here is
//     a no-op.
//   - 0 <= idx < Len() — pointing at a history entry; Up decrements,
//     Down increments.
//
// Stash is the buffer text the user had typed before pressing Up.
// Down past the newest entry restores the stash.
type HistoryNav struct {
	src   HistorySource
	idx   int // Len() means "at the stash"
	stash string
}

// NewHistoryNav returns a navigator over src with idx positioned at
// the stash slot.
func NewHistoryNav(src HistorySource) *HistoryNav {
	return &HistoryNav{src: src, idx: src.Len()}
}

// Stash records the pre-nav buffer text. Call this before the first Up
// so a later Down can restore what the user had typed.
func (h *HistoryNav) Stash(s string) {
	h.stash = s
	h.idx = h.src.Len()
}

// Up walks one step back in history. Returns the line at the new
// position and whether there was anywhere to walk to (false on an
// empty source).
//
// Walking past the oldest entry clamps at the oldest.
func (h *HistoryNav) Up() (string, bool) {
	n := h.src.Len()
	if n == 0 {
		return "", false
	}
	if h.idx > 0 {
		h.idx--
	}
	return h.src.At(h.idx), true
}

// Down walks one step forward in history. Returns the line at the new
// position and whether there was anywhere to walk to. Walking past the
// newest entry restores the stash and returns (stash, true). A second
// Down past the stash returns ("", false).
func (h *HistoryNav) Down() (string, bool) {
	n := h.src.Len()
	if n == 0 {
		return "", false
	}
	if h.idx >= n {
		// Already at the stash — no further forward motion.
		return "", false
	}
	h.idx++
	if h.idx == n {
		return h.stash, true
	}
	return h.src.At(h.idx), true
}
