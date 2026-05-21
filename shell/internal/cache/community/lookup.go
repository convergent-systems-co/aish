package community

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	// modernc.org/sqlite is already a dependency of the parent
	// cache package; sharing the driver registration via init.
	_ "modernc.org/sqlite"
)

// BundleSchema is the SQL DDL applied to a freshly-built community
// bundle. It mirrors the L1 store's intents-table columns (so a row
// can be lifted into L1 with no transformation) but DROPS the stats
// rows + hit_count book-keeping — an L3 bundle is read-only by
// definition.
//
// The build tool (cmd/aish-community) applies this DDL once when
// creating a new bundle from a JSONL seed. At runtime the shell only
// runs SELECTs.
const BundleSchema = `
CREATE TABLE IF NOT EXISTS intents (
  intent_hash TEXT    NOT NULL,
  os          TEXT    NOT NULL,
  intent      TEXT    NOT NULL,
  invocation  TEXT    NOT NULL,
  confidence  REAL    NOT NULL DEFAULT 1.0,
  PRIMARY KEY (intent_hash, os)
);

CREATE INDEX IF NOT EXISTS idx_intents_os ON intents(os);
`

// bundleDB wraps the *sql.DB opened against a bundle.db file. Held
// internally by *Bundle; Lookup opens it lazily on first call.
type bundleDB struct {
	db *sql.DB
}

// openBundleDB opens a read-only handle on the SQLite file at path.
// `mode=ro` plus `immutable=1` tell modernc.org/sqlite to skip the
// journal-mode pragma — important because we never want to mutate
// the bundle (and on some platforms the file lives in a read-only
// system directory).
func openBundleDB(path string) (*bundleDB, error) {
	// Format: `file:<path>?mode=ro&immutable=1`. The modernc driver
	// accepts SQLite URI syntax verbatim.
	uri := fmt.Sprintf("file:%s?mode=ro&immutable=1", path)
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("community: open bundle.db: %w", err)
	}
	// Single conn — no concurrent writes possible against a read-
	// only DB, but cap connection count to avoid the driver opening
	// extras under contention.
	db.SetMaxOpenConns(1)
	// Smoke the connection so a malformed bundle surfaces at Open
	// rather than at the first Lookup.
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("community: ping bundle.db: %w", err)
	}
	return &bundleDB{db: db}, nil
}

// close releases the database handle. Idempotent.
func (b *bundleDB) close() error {
	if b == nil || b.db == nil {
		return nil
	}
	err := b.db.Close()
	b.db = nil
	return err
}

// lookup returns the invocation for (intent, os) if a row exists in
// the community bundle. Misses return (_, false, nil); errors are
// reserved for I/O failures.
//
// Normalization mirrors the L1 store: TrimSpace + ToLower on the
// intent. The build tool MUST apply the same normalization at sign
// time or rows will be unreachable at runtime.
func (b *bundleDB) lookup(intent, os string) (string, bool, error) {
	if b == nil || b.db == nil {
		return "", false, errors.New("community: bundle is closed")
	}
	h := hashIntent(intent)
	var invocation string
	err := b.db.QueryRow(
		`SELECT invocation FROM intents WHERE intent_hash = ? AND os = ?`,
		h, os,
	).Scan(&invocation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("community: lookup: %w", err)
	}
	return invocation, true, nil
}

// hashIntent must produce the same digest as
// shell/internal/cache.hashIntent or community rows are unreachable.
// Kept in lock-step by the test suite (TestHashIntentMatchesL1).
func hashIntent(intent string) string {
	norm := strings.ToLower(strings.TrimSpace(intent))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// HashIntentForBuild exposes the package-local hashIntent so the
// cmd/aish-community build tool can compute identical hashes when
// populating bundle.db at build time. Callers MUST NOT use this for
// anything but bundle construction — the runtime path uses the
// unexported helper.
func HashIntentForBuild(intent string) string {
	return hashIntent(intent)
}

// Lookup is the public entry point: open the underlying DB lazily,
// then run the read-only query. On any I/O failure the bundle is
// marked closed so subsequent calls return cleanly.
//
// Returns:
//
//	(invocation, true,  nil) — L3 hit
//	("",         false, nil) — L3 miss (or bundle not loaded)
//	("",         false, err) — I/O failure
func (b *Bundle) Lookup(intent, os string) (string, bool, error) {
	if b == nil || b.dbPath == "" {
		return "", false, nil
	}
	if b.db == nil {
		opened, err := openBundleDB(b.dbPath)
		if err != nil {
			return "", false, err
		}
		b.db = opened
	}
	return b.db.lookup(intent, os)
}

// Close releases the underlying DB handle if one was opened. Safe to
// call multiple times. Safe to call on a nil receiver.
func (b *Bundle) Close() error {
	if b == nil {
		return nil
	}
	if b.db == nil {
		return nil
	}
	err := b.db.close()
	b.db = nil
	return err
}

// IntentCount returns the live row count from bundle.db. Cheaper to
// trust the manifest's static count for `aish community info`, but
// exposed here so the built-in can detect manifest/DB drift.
func (b *Bundle) IntentCount() (int, error) {
	if b == nil || b.dbPath == "" {
		return 0, nil
	}
	if b.db == nil {
		opened, err := openBundleDB(b.dbPath)
		if err != nil {
			return 0, err
		}
		b.db = opened
	}
	var n int
	if err := b.db.db.QueryRow(`SELECT COUNT(*) FROM intents`).Scan(&n); err != nil {
		return 0, fmt.Errorf("community: count intents: %w", err)
	}
	return n, nil
}
