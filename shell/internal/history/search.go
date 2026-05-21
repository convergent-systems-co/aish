package history

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// List returns up to `limit` events ordered newest-first. A limit ≤ 0
// is treated as a sane default (50) so the built-in's "show me
// recent history" path does not require the caller to plumb a
// magic-number through. Returns an empty slice (not nil) when no
// events exist, so the caller can range over the result safely.
func (s *Store) List(limit int) ([]*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: List: store is closed")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT payload FROM events
		   ORDER BY ts DESC, id DESC
		   LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("history: List: %w", err)
	}
	defer rows.Close()
	out := make([]*Event, 0, limit)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
}

// Get returns the event with the supplied id, or (nil, nil) when no
// such event exists. ids are the `evt_<hex>` form produced by
// NewEventID.
func (s *Store) Get(id string) (*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: Get: store is closed")
	}
	if id == "" {
		return nil, errors.New("history: Get: empty id")
	}
	row := s.db.QueryRow(`SELECT payload FROM events WHERE id = ?`, id)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: Get: %w", err)
	}
	var ev Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return nil, fmt.Errorf("history: Get unmarshal: %w", err)
	}
	return &ev, nil
}

// Search runs an FTS5 match against the events index. When the query
// is a valid FTS5 expression (single term, phrase, prefix, boolean)
// the index returns matching events newest-first. When FTS5 rejects
// the query as malformed (special chars the user didn't escape),
// Search falls back to a substring scan over events.command so the
// user's typo doesn't produce a confusing parse error — they
// typically just wanted "find this fragment."
//
// limit ≤ 0 → default 50.
func (s *Store) Search(query string, limit int) ([]*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: Search: store is closed")
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("history: Search: empty query")
	}
	if limit <= 0 {
		limit = 50
	}
	// Prepare an FTS5-friendly form: quote bare alphanumeric terms,
	// leave phrase queries (`"x y"`) intact. Quoting keeps `mv` (which
	// looks like a column-prefix in FTS5 syntax) safe.
	ftsQuery := quoteFTSQuery(query)
	rows, err := s.db.Query(
		`SELECT e.payload
		   FROM events_fts f
		   JOIN events e ON e.rowid = f.rowid
		  WHERE events_fts MATCH ?
		  ORDER BY e.ts DESC, e.id DESC
		  LIMIT ?`,
		ftsQuery, limit,
	)
	if err != nil {
		return s.searchSubstringFallback(query, limit)
	}
	defer rows.Close()
	out := make([]*Event, 0, limit)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, &ev)
	}
	if err := rows.Err(); err != nil {
		// Late error during iteration — FTS5 reject can land here
		// instead of at Query() depending on the SQLite version. Fall
		// back rather than 500 the user.
		return s.searchSubstringFallback(query, limit)
	}
	if len(out) == 0 {
		// Empty FTS result: try the substring path too. Useful when
		// the user typed a fragment of a filename that the tokenizer
		// would not have indexed as a whole word.
		return s.searchSubstringFallback(query, limit)
	}
	return out, nil
}

// searchSubstringFallback runs a LIKE-based scan over events.command
// and events.name when FTS5 rejects or empties the query. Used as the
// fallback path inside Search.
func (s *Store) searchSubstringFallback(query string, limit int) ([]*Event, error) {
	pat := "%" + strings.ReplaceAll(strings.ReplaceAll(query, "%", `\%`), "_", `\_`) + "%"
	rows, err := s.db.Query(
		`SELECT payload FROM events
		   WHERE command LIKE ? ESCAPE '\'
		      OR name    LIKE ? ESCAPE '\'
		   ORDER BY ts DESC, id DESC
		   LIMIT ?`,
		pat, pat, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("history: Search fallback: %w", err)
	}
	defer rows.Close()
	out := make([]*Event, 0, limit)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, &ev)
	}
	return out, rows.Err()
}

// quoteFTSQuery makes a user-supplied search term safe for an FTS5
// MATCH expression. The rule is conservative: if the query already
// contains an FTS5-special character (`"`, `*`, `(`, `)`, `:`, `^`),
// pass it through unchanged so phrase / prefix / column-targeted
// queries work; otherwise wrap each whitespace-separated token in
// double quotes so the tokenizer treats it as a literal term.
//
// Examples:
//
//	mv          → "mv"
//	rm /tmp/x   → "rm" "/tmp/x"
//	"rm -rf"    → "rm -rf" (passed through; phrase intended)
func quoteFTSQuery(q string) string {
	q = strings.TrimSpace(q)
	if strings.ContainsAny(q, `"*():^`) {
		return q
	}
	parts := strings.Fields(q)
	for i, p := range parts {
		// Strip any stray double-quotes already in the token so the
		// re-wrap doesn't double them up.
		p = strings.ReplaceAll(p, `"`, "")
		parts[i] = `"` + p + `"`
	}
	return strings.Join(parts, " ")
}

// Purge deletes events older than `before` and returns the count of
// rows removed. The matching snapshot rows are deleted via the
// foreign-key relationship; the on-disk snapshot directories are NOT
// removed here — the caller (the `history purge` built-in) sweeps
// the snapshot root separately so unreferenced bytes don't sit
// around.
//
// A zero `before` means "purge everything" — the caller should
// confirm with the user before invoking it.
func (s *Store) Purge(before time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("history: Purge: store is closed")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("history: Purge begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`DELETE FROM snapshots WHERE event_id IN
		   (SELECT id FROM events WHERE ts < ?)`,
		before.UTC(),
	); err != nil {
		return 0, fmt.Errorf("history: Purge snapshots: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM events WHERE ts < ?`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("history: Purge events: %w", err)
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("history: Purge commit: %w", err)
	}
	return n, nil
}
