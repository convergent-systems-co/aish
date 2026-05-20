package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SessionsDirName is the basename of the directory under `~/.aish`
// that holds per-session JSON files. Public so tests can construct
// expected paths.
const SessionsDirName = "sessions"

// PendingDirName is the basename of the subdirectory inside the
// sessions directory that holds queued-for-upload payloads. Created
// only when `opt_in_aggregate=true`.
const PendingDirName = "pending"

// WriteSessionRow persists row to `dotAishDir/sessions/<id>.json`.
// Always called when consent.OptInLocal is true — these files are
// own-machine data backing `aish stats`.
//
// Uses an atomic-rename (.tmp → final) so a process crash mid-write
// leaves either the full row or no file at all; partial JSON would
// poison ListSessions.
func WriteSessionRow(dotAishDir string, row SessionRow) error {
	if dotAishDir == "" {
		return fmt.Errorf("telemetry: WriteSessionRow: empty dotAishDir")
	}
	if row.ID == "" {
		return fmt.Errorf("telemetry: WriteSessionRow: empty session ID")
	}
	dir := filepath.Join(dotAishDir, SessionsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("telemetry: WriteSessionRow: mkdir %q: %w", dir, err)
	}
	final := filepath.Join(dir, row.ID+".json")
	tmp := final + ".tmp"
	data, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		return fmt.Errorf("telemetry: WriteSessionRow: marshal: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("telemetry: WriteSessionRow: write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("telemetry: WriteSessionRow: rename %q: %w", final, err)
	}
	return nil
}

// WritePending persists row to
// `dotAishDir/sessions/pending/<id>.json` — the aggregate-dashboard
// queue. ONLY called when consent.OptInAggregate is true. v0.2's
// transport work drains this directory.
//
// Same atomic-rename discipline as WriteSessionRow.
func WritePending(dotAishDir string, row SessionRow) error {
	if dotAishDir == "" {
		return fmt.Errorf("telemetry: WritePending: empty dotAishDir")
	}
	if row.ID == "" {
		return fmt.Errorf("telemetry: WritePending: empty session ID")
	}
	dir := filepath.Join(dotAishDir, SessionsDirName, PendingDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("telemetry: WritePending: mkdir %q: %w", dir, err)
	}
	final := filepath.Join(dir, row.ID+".json")
	tmp := final + ".tmp"
	data, err := json.Marshal(row) // compact for queue payloads
	if err != nil {
		return fmt.Errorf("telemetry: WritePending: marshal: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("telemetry: WritePending: write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("telemetry: WritePending: rename %q: %w", final, err)
	}
	return nil
}

// ListSessions reads up to `limit` of the most-recent session rows
// from `dotAishDir/sessions/`. Sorted newest-first by StartedAt; on a
// tie, by filename (lexical, which is roughly chronological since
// session IDs include random bits but not timestamps — the file
// mtime is the real tie-breaker).
//
// Files in the `pending/` subdirectory are NOT included — those are
// the outbound queue, not the history list.
//
// Missing directory returns an empty slice without error — a brand
// new install legitimately has no sessions.
//
// Limit <= 0 means "return all available rows."
func ListSessions(dotAishDir string, limit int) ([]SessionRow, error) {
	if dotAishDir == "" {
		return nil, nil
	}
	dir := filepath.Join(dotAishDir, SessionsDirName)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: ListSessions: readdir %q: %w", dir, err)
	}

	rows := make([]SessionRow, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue // skip pending/
		}
		name := e.Name()
		// Only consume <id>.json — skip .tmp leftovers and anything
		// else a future version might write.
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // best-effort — a single bad file doesn't break the list
		}
		var row SessionRow
		if err := json.Unmarshal(data, &row); err != nil {
			continue
		}
		if row.SchemaVersion > CurrentSchemaVersion {
			continue // forward-incompat row from a future writer
		}
		rows = append(rows, row)
	}

	// Newest first: StartedAt desc, then ID asc as a stable tie-break.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartedAt != rows[j].StartedAt {
			return rows[i].StartedAt > rows[j].StartedAt
		}
		return rows[i].ID < rows[j].ID
	})

	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
