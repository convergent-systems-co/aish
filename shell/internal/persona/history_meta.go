package persona

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// HistoryMetaFileName is the on-disk JSONL sidecar that maps history
// event IDs to the persona that was active when the event was
// recorded. Lives alongside ~/.aish/history.db so it is purgeable in
// lockstep with the database it annotates.
//
// One JSON object per line ({"event_id": "evt_…", "persona": "…",
// "ts": "RFC3339"}); the file is append-only — purge happens by
// rewriting it from scratch via PurgeKeepingIDs.
const HistoryMetaFileName = "persona-events.jsonl"

// HistoryMeta is the typed shape of one sidecar row. Exported so
// callers (the shell's history-display path) can decode rows without
// rolling their own parser.
type HistoryMeta struct {
	EventID string `json:"event_id"`
	Persona string `json:"persona"`
	// TS is RFC3339; informational. Not used for lookup.
	TS string `json:"ts,omitempty"`
}

// MetaStore is the append-only JSONL writer + read-side index for the
// persona-events sidecar. Concurrency-safe; reads are served from an
// in-memory map populated on Open / Record.
//
// The store is deliberately separate from the history package. The
// FU plan forbids extending history's Append API; this sidecar is
// the workaround that keeps cross-package coupling one-way (persona
// reads history.Event.ID, history never reads persona meta).
type MetaStore struct {
	path string

	mu    sync.Mutex
	cache map[string]string // event_id → persona name
}

// OpenMetaStore opens (or creates) the sidecar at
// dotAish/persona-events.jsonl. A missing file is fine: it starts
// empty and the first Record writes the first row.
//
// dotAish must point at the per-user .aish directory; the caller is
// the shell's openPersona path. An empty dotAish disables the store
// (Open returns a usable but read-only MetaStore that no-ops on
// Record), matching the rest of persona's degradation posture.
func OpenMetaStore(dotAish string) (*MetaStore, error) {
	if dotAish == "" {
		return &MetaStore{cache: map[string]string{}}, nil
	}
	if err := os.MkdirAll(dotAish, 0o700); err != nil {
		return nil, fmt.Errorf("persona meta: ensure dir: %w", err)
	}
	path := filepath.Join(dotAish, HistoryMetaFileName)
	ms := &MetaStore{path: path, cache: map[string]string{}}
	if err := ms.loadCache(); err != nil {
		return nil, err
	}
	return ms, nil
}

// loadCache reads the entire sidecar file into the in-memory map.
// Cheap — the file is JSONL with ~one row per destructive command.
// A malformed row is skipped (we'd rather lose attribution than
// crash the shell).
func (m *MetaStore) loadCache() error {
	if m.path == "" {
		return nil
	}
	f, err := os.Open(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("persona meta: open %s: %w", m.path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 8*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row HistoryMeta
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row.EventID == "" {
			continue
		}
		m.cache[row.EventID] = row.Persona
	}
	return scanner.Err()
}

// Record appends a row to the sidecar and updates the in-memory
// index. eventID is the history.Event.ID; persona is the active
// persona name at recording time. A blank persona writes "default"
// so the row is unambiguous on read (an empty persona field would
// look like "we didn't record this" rather than "no persona was
// active").
//
// Errors are returned but never abort the caller — the dispatch path
// treats persona attribution as best-effort, the destructive command
// runs unconditionally. The shell-side caller already drops the
// error per the same posture as history.Append's snapshot writes.
func (m *MetaStore) Record(eventID, persona, ts string) error {
	if m == nil {
		return nil
	}
	if eventID == "" {
		return errors.New("persona meta: empty event_id")
	}
	if persona == "" {
		persona = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[eventID] = persona
	if m.path == "" {
		return nil
	}
	row := HistoryMeta{EventID: eventID, Persona: persona, TS: ts}
	raw, err := json.Marshal(&row)
	if err != nil {
		return fmt.Errorf("persona meta: marshal: %w", err)
	}
	f, err := os.OpenFile(m.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("persona meta: open append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("persona meta: write: %w", err)
	}
	return nil
}

// Lookup returns the persona name recorded against eventID, or ""
// when no record exists. ok is false for the "no record" case so
// callers can distinguish "no persona was active" (which is recorded
// as "default") from "we have no row for this event" (which renders
// as "?"). The distinction matters for events recorded before the
// sidecar was wired (pre-v0.3-5.1 history rows).
func (m *MetaStore) Lookup(eventID string) (name string, ok bool) {
	if m == nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.cache[eventID]
	return v, ok
}

// PurgeKeepingIDs rewrites the sidecar in place, retaining only the
// rows whose event_id is in keep. Used after history.Purge so the
// sidecar does not retain rows for events that no longer exist.
// Caller is the shell's purge path (a future follow-up); the
// function is exposed now so the sidecar matures with the history
// engine.
func (m *MetaStore) PurgeKeepingIDs(keep map[string]bool) error {
	if m == nil || m.path == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Rewrite atomically: write a new file then rename.
	tmp := m.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("persona meta: open tmp: %w", err)
	}
	w := bufio.NewWriter(f)
	for id, p := range m.cache {
		if !keep[id] {
			continue
		}
		raw, err := json.Marshal(HistoryMeta{EventID: id, Persona: p})
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("persona meta: marshal: %w", err)
		}
		if _, err := w.Write(append(raw, '\n')); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("persona meta: write: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("persona meta: flush: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona meta: close tmp: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona meta: rename: %w", err)
	}
	// Rebuild the in-memory cache from the kept set.
	for id := range m.cache {
		if !keep[id] {
			delete(m.cache, id)
		}
	}
	return nil
}
