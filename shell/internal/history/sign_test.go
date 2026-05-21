package history

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFileSigner_CreatesKeyAt0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.key")
	s, err := NewFileSigner(path)
	if err != nil {
		t.Fatalf("NewFileSigner: %v", err)
	}
	if s.SignerID() != LocalSignerID {
		t.Fatalf("SignerID = %q, want %q", s.SignerID(), LocalSignerID)
	}
	if got := s.PublicKey(); len(got) != 32 {
		t.Fatalf("PublicKey length = %d, want 32", len(got))
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if runtime.GOOS != "windows" {
		// Windows permission bits don't follow POSIX; the 0600 check
		// only makes sense on POSIX. The actual ACL on Windows is set
		// by the default file-creation mode there.
		if got := st.Mode().Perm(); got != 0o600 {
			t.Fatalf("key mode = %o, want 0600", got)
		}
	}
}

func TestFileSigner_ReusesExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.key")
	first, err := NewFileSigner(path)
	if err != nil {
		t.Fatalf("first NewFileSigner: %v", err)
	}
	second, err := NewFileSigner(path)
	if err != nil {
		t.Fatalf("second NewFileSigner: %v", err)
	}
	// Same key produces the same signature on the same message.
	msg := []byte("event-canon-bytes")
	sig1, err := first.Sign(msg)
	if err != nil {
		t.Fatalf("first.Sign: %v", err)
	}
	sig2, err := second.Sign(msg)
	if err != nil {
		t.Fatalf("second.Sign: %v", err)
	}
	if string(sig1) != string(sig2) {
		t.Fatalf("two signers from the same key file produced different signatures")
	}
}

func TestFileSigner_RejectsMalformedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.key")
	if err := os.WriteFile(path, []byte("not-hex"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := NewFileSigner(path); err == nil {
		t.Fatalf("NewFileSigner accepted malformed key file")
	}
}

func TestVerify_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSigner(filepath.Join(dir, "history.key"))
	if err != nil {
		t.Fatalf("NewFileSigner: %v", err)
	}
	ev := &Event{
		ID:        "evt_test",
		Timestamp: time.Unix(1700000000, 0).UTC(),
		Kind:      KindSnapshot,
		Command:   "rm /tmp/x",
	}
	msg, err := canonicalSigningMsg(ev)
	if err != nil {
		t.Fatalf("canonicalSigningMsg: %v", err)
	}
	sig, err := s.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(ev, sig, s.PublicKey()); err != nil {
		t.Fatalf("Verify on untouched event: %v", err)
	}
}

func TestVerify_TamperDetected(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSigner(filepath.Join(dir, "history.key"))
	if err != nil {
		t.Fatalf("NewFileSigner: %v", err)
	}
	ev := &Event{
		ID:        "evt_test",
		Timestamp: time.Unix(1700000000, 0).UTC(),
		Kind:      KindSnapshot,
		Command:   "rm /tmp/x",
	}
	msg, _ := canonicalSigningMsg(ev)
	sig, _ := s.Sign(msg)

	// Mutate Command after signing — the verifier MUST notice.
	ev.Command = "rm /etc/passwd"
	if err := Verify(ev, sig, s.PublicKey()); err == nil {
		t.Fatalf("Verify accepted a tampered event")
	}
}

func TestVerify_SignatureAndSignerIDAreNotPartOfTheSignedMessage(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSigner(filepath.Join(dir, "history.key"))
	if err != nil {
		t.Fatalf("NewFileSigner: %v", err)
	}
	ev := &Event{
		ID:        "evt_test",
		Timestamp: time.Unix(1700000000, 0).UTC(),
		Kind:      KindSnapshot,
		Command:   "rm /tmp/x",
	}
	msg, _ := canonicalSigningMsg(ev)
	sig, _ := s.Sign(msg)

	// Populate the carrier fields AFTER signing, mirroring what
	// store.Append does. Verify MUST still succeed because those
	// fields are blanked inside canonicalSigningMsg.
	ev.Signature = "BASE64-PLACEHOLDER"
	ev.SignerID = LocalSignerID
	if err := Verify(ev, sig, s.PublicKey()); err != nil {
		t.Fatalf("Verify failed after populating signature/signer_id: %v", err)
	}
}

func TestDefaultKeyPath(t *testing.T) {
	got := DefaultKeyPath("/home/x/.aish")
	want := "/home/x/.aish/history.key"
	if got != want {
		t.Fatalf("DefaultKeyPath = %q, want %q", got, want)
	}
}
