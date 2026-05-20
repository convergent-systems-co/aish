package cache

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	// modernc.org/sqlite is a pure-Go SQLite driver (no CGO). It
	// registers a `sqlite` database/sql driver via init.
	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed persistence layer for the intent cache.
// Methods are safe for concurrent use because they delegate to *sql.DB,
// which serialises writes through SQLite's own locking. The single-file
// DB at the caller-supplied path is the canonical state; nothing
// in-memory survives a process restart.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and applies the
// DDL idempotently. WAL is enabled so concurrent readers do not block
// writers — important once the v0.1-3 background indexer lands. The
// busy_timeout pragma keeps transient lock contention from surfacing as
// "database is locked" errors on a hot machine.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("cache: Open: path is empty")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("cache: open %s: %w", path, err)
	}
	// Conservative concurrency posture: one writer at a time so the
	// SQLite "database is locked" failure mode never reaches the user.
	// Readers still run in parallel through WAL.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cache: pragma: %w", err)
	}
	if _, err := db.Exec(DDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cache: apply DDL: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle. Idempotent — a second Close on a
// closed Store is a no-op.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// hashIntent returns the SHA-256 of the normalized intent. Normalization
// is TrimSpace + ToLower so cosmetic differences ("List Files" vs
// "list files ") collapse to a single cache key.
func hashIntent(intent string) string {
	norm := strings.ToLower(strings.TrimSpace(intent))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// Lookup returns the cached invocation for (intent, os) if the (hash,
// os) tuple exists. The boolean second return is true on a hit, false
// on a miss; the error channel is for I/O failures only.
//
// On a hit, hit_count is incremented, last_used is bumped to the
// current timestamp, and the stats counters (`hits`, `total_queries`)
// advance. On a miss the stats counters (`misses`, `total_queries`)
// advance and the row is left untouched.
//
// Lookup is the single accounting site for query counters; Write does
// NOT touch them (a miss is already accounted for here by the time the
// caller decides to populate the row).
func (s *Store) Lookup(intent, os string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, errors.New("cache: Lookup: store is closed")
	}
	h := hashIntent(intent)

	tx, err := s.db.Begin()
	if err != nil {
		return "", false, fmt.Errorf("cache: Lookup: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var invocation string
	row := tx.QueryRow(`SELECT invocation FROM intents WHERE intent_hash = ? AND os = ?`, h, os)
	scanErr := row.Scan(&invocation)
	hit := scanErr == nil
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return "", false, fmt.Errorf("cache: Lookup: scan: %w", scanErr)
	}

	if hit {
		if _, err := tx.Exec(
			`UPDATE intents SET hit_count = hit_count + 1, last_used = CURRENT_TIMESTAMP WHERE intent_hash = ? AND os = ?`,
			h, os,
		); err != nil {
			return "", false, fmt.Errorf("cache: Lookup: bump hit_count: %w", err)
		}
		if _, err := tx.Exec(`UPDATE stats SET value = value + 1 WHERE key IN ('hits', 'total_queries')`); err != nil {
			return "", false, fmt.Errorf("cache: Lookup: bump stats (hit): %w", err)
		}
	} else {
		if _, err := tx.Exec(`UPDATE stats SET value = value + 1 WHERE key IN ('misses', 'total_queries')`); err != nil {
			return "", false, fmt.Errorf("cache: Lookup: bump stats (miss): %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("cache: Lookup: commit: %w", err)
	}
	return invocation, hit, nil
}

// Write upserts an (intent, os) row. Idempotent: a second Write for the
// same key replaces invocation + confidence and leaves hit_count and
// created_at intact (so we don't lose the original learn date or reset
// frequency on a re-fetch). last_used is bumped to mark recent activity.
//
// confidence is clamped to [0, 1] defensively — a plugin that returns
// 1.5 or -0.1 is buggy but should not corrupt the schema.
//
// Write does NOT touch stats; counters are owned by Lookup (see its
// docstring for the why).
func (s *Store) Write(intent, os, invocation string, confidence float64) error {
	if s == nil || s.db == nil {
		return errors.New("cache: Write: store is closed")
	}
	if intent == "" {
		return errors.New("cache: Write: intent is empty")
	}
	if os == "" {
		return errors.New("cache: Write: os is empty")
	}
	if invocation == "" {
		return errors.New("cache: Write: invocation is empty")
	}
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}
	h := hashIntent(intent)
	_, err := s.db.Exec(
		`INSERT INTO intents (intent_hash, os, intent, invocation, confidence)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(intent_hash, os) DO UPDATE SET
		   invocation = excluded.invocation,
		   confidence = excluded.confidence,
		   last_used  = CURRENT_TIMESTAMP`,
		h, os, intent, invocation, confidence,
	)
	if err != nil {
		return fmt.Errorf("cache: Write: upsert: %w", err)
	}
	return nil
}

// Stats is the snapshot returned by Store.Stats. It carries cumulative
// query counters plus the current row count — the four numbers a user
// sees behind `aish cache stats`.
type Stats struct {
	// Hits is the count of Lookup calls that returned a hit since the
	// store was created (or last Cleared).
	Hits int64
	// Misses is the count of Lookup calls that returned a miss.
	Misses int64
	// TotalQueries is Hits+Misses. Stored explicitly so an integrity check
	// can flag drift between the counters (which would indicate a bug).
	TotalQueries int64
	// Entries is the current number of rows in the intents table — a
	// live count, not a counter, so it tracks deletions automatically.
	Entries int64
}

// Stats returns the four-field snapshot consumed by `aish cache stats`.
// Wraps two cheap queries; not a hot-path call.
func (s *Store) Stats() (Stats, error) {
	if s == nil || s.db == nil {
		return Stats{}, errors.New("cache: Stats: store is closed")
	}
	var stats Stats
	rows, err := s.db.Query(`SELECT key, value FROM stats`)
	if err != nil {
		return Stats{}, fmt.Errorf("cache: Stats: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value int64
		if err := rows.Scan(&key, &value); err != nil {
			return Stats{}, fmt.Errorf("cache: Stats: scan: %w", err)
		}
		switch key {
		case "hits":
			stats.Hits = value
		case "misses":
			stats.Misses = value
		case "total_queries":
			stats.TotalQueries = value
		}
	}
	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("cache: Stats: rows: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intents`).Scan(&stats.Entries); err != nil {
		return Stats{}, fmt.Errorf("cache: Stats: count entries: %w", err)
	}
	return stats, nil
}

// Clear truncates every intent row and resets all stats counters to
// zero. This is the destructive operation behind `aish cache clear`;
// the shell built-in is responsible for confirming with the user before
// calling it (per Common.md §2.2 destructive-action discipline).
func (s *Store) Clear() error {
	if s == nil || s.db == nil {
		return errors.New("cache: Clear: store is closed")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("cache: Clear: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM intents`); err != nil {
		return fmt.Errorf("cache: Clear: delete intents: %w", err)
	}
	if _, err := tx.Exec(`UPDATE stats SET value = 0`); err != nil {
		return fmt.Errorf("cache: Clear: reset stats: %w", err)
	}
	return tx.Commit()
}
