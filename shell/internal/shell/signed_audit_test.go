package shell

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/secrets"
	_ "modernc.org/sqlite" // driver for the test-only read-back of events
)

// TestSignedAudit_SecretGet_HasSignature — v0.3-fu-secrets #106
// strengthening pass: a `secret get` invocation with the history
// engine wired and a signer attached MUST persist an event whose
// Signature column is non-empty.
//
// The shell's openHistory path wires a FileSigner by default
// (`history.NewFileSigner(DefaultKeyPath(dotAish))`); this test
// uses `t.Setenv` BEFORE `New()` so openHistory writes its keys + DB
// under the tempdir, not the developer's home.
func TestSignedAudit_SecretGet_HasSignature(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s := New()
	s.SetSecretKDFParamsForTesting(secrets.KDFParams{Time: 1, Memory: 8 * 1024, Parallelism: 1, KeyLen: secrets.KeySize})
	t.Cleanup(func() { _ = s.Close() })
	if s.history == nil {
		t.Skip("history engine unavailable in this environment")
	}
	if s.history.Store().Signer() == nil {
		t.Skip("no signer attached in this environment")
	}
	// Wire a no-op clipboard so secret get can complete on hosts
	// without `pbcopy` / `xclip` (CI containers, etc.).
	s.SetClipboardFnForTesting(func(_ []byte) error { return nil })

	// First set a secret so there's something to get.
	stdin := bytes.NewBufferString("test-fake-passphrase-AUDIT\ntest-fake-value-audit\n")
	var out, errBuf bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "AUDIT_KEY"}, stdin, &out, &errBuf); code != 0 {
		t.Fatalf("set AUDIT_KEY: code=%d, stderr=%q", code, errBuf.String())
	}
	// Drop cached passphrase so get re-prompts (mirrors a fresh session).
	s.SecretLockForTesting()

	stdin2 := bytes.NewBufferString("test-fake-passphrase-AUDIT\n")
	var out2, err2 bytes.Buffer
	if code := s.secretBuiltin([]string{"get", "AUDIT_KEY"}, stdin2, &out2, &err2); code != 0 {
		t.Fatalf("get AUDIT_KEY: code=%d, stderr=%q", code, err2.String())
	}
	if strings.Contains(out2.String(), "test-fake-value-audit") {
		t.Fatalf("get leaked the value to stdout:\n%s", out2.String())
	}

	// Query the events table directly for kind=secret.get.
	sig, signerID := readLastSignature(t, s, "secret.get")
	if sig == "" {
		t.Fatalf("secret.get event has no signature; expected a signed audit row")
	}
	if signerID == "" {
		t.Fatalf("secret.get event has no signer_id; expected aish-local")
	}
}

// TestSignedAudit_PersonaUse_HasSignature — v0.3-fu-secrets #106
// adds the `persona.use` event; assert it's signed too.
func TestSignedAudit_PersonaUse_HasSignature(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if s.history == nil {
		t.Skip("history engine unavailable in this environment")
	}
	if s.history.Store().Signer() == nil {
		t.Skip("no signer attached in this environment")
	}
	// personaBuiltin requires the personas registry. New() opens it
	// from the bundled set, so we have a "default" + bundled personas.
	if s.personas == nil {
		t.Skip("persona registry not loaded in this environment")
	}

	var out, errBuf bytes.Buffer
	code := s.personaBuiltin([]string{"set", "default"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("persona set default: code=%d, stderr=%q", code, errBuf.String())
	}
	sig, signerID := readLastSignature(t, s, "persona.use")
	if sig == "" {
		t.Fatalf("persona.use event has no signature; recordPersonaUse should have signed it")
	}
	if signerID == "" {
		t.Fatalf("persona.use event has no signer_id")
	}
}

// readLastSignature queries the most-recent event of the given kind
// and returns its (signature, signer_id) columns. The shell's
// openHistory wires the DB at ~/.aish/history.db; we open a
// read-only connection alongside (modernc/sqlite WAL allows
// concurrent readers) so the assertion does not race the production
// store's writer.
func readLastSignature(t *testing.T, s *Shell, kind string) (string, string) {
	t.Helper()
	home := homeDir(s.env)
	dbPath := filepath.Join(home, ".aish", "history.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open history DB: %v", err)
	}
	defer db.Close()
	row := db.QueryRow(
		`SELECT signature, signer_id FROM events
		 WHERE kind = ?
		 ORDER BY ts DESC, id DESC LIMIT 1`, kind)
	var sig, signerID string
	if err := row.Scan(&sig, &signerID); err != nil {
		t.Fatalf("query last %s event: %v", kind, err)
	}
	return sig, signerID
}
