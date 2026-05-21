package shell

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/term"
	_ "modernc.org/sqlite"
)

// import_modernc_sqlite is a no-op that exists only to anchor the
// blank import above so the linter doesn't strip it on go fmt /
// goimports invocations.
func import_modernc_sqlite() {}

// sqlOpen is a tiny wrapper around database/sql.Open for the
// modernc.org/sqlite driver. Centralises the driver name so a future
// switch to a different SQLite backend touches one site.
func sqlOpen(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

// communityHashIntent mirrors the bundle-side hashIntent so the
// stageBundle test helper can insert rows under the same key the
// runtime will look up.
//
// Lower + trim + SHA-256 must stay locked to the L1 and L3
// hashIntent helpers. The cross-package contract is asserted by
// community.TestHashIntentNormalizesInput.
func communityHashIntent(intent string) string {
	norm := strings.ToLower(strings.TrimSpace(intent))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// tierBuiltinExpected returns the term.Tier value the production
// builtin tier resolves to. Exposed so dispatch tests can match
// without re-importing term at every call site.
func tierBuiltinExpected() term.Tier { return term.TierBuiltin }
