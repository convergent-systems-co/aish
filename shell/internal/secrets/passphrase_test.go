package secrets

import (
	"bytes"
	"strings"
	"testing"
)

// TestReadPassphraseFrom_StripsTrailingNewline — when reading from a
// non-TTY (a *bytes.Buffer in this test), a trailing \n MUST be
// stripped. The user typed "pass<enter>"; the passphrase is "pass".
func TestReadPassphraseFrom_StripsTrailingNewline(t *testing.T) {
	in := bytes.NewBufferString("test-fake-passphrase-A\n")
	got, err := ReadPassphraseFrom(in)
	if err != nil {
		t.Fatalf("ReadPassphraseFrom: %v", err)
	}
	defer Zero(got)
	if string(got) != "test-fake-passphrase-A" {
		t.Errorf("got %q; want %q", got, "test-fake-passphrase-A")
	}
}

// TestReadPassphraseFrom_StripsCRLF — Windows / paste-from-Notepad
// can give \r\n. Strip both.
func TestReadPassphraseFrom_StripsCRLF(t *testing.T) {
	in := bytes.NewBufferString("test-fake-passphrase-B\r\n")
	got, err := ReadPassphraseFrom(in)
	if err != nil {
		t.Fatalf("ReadPassphraseFrom: %v", err)
	}
	defer Zero(got)
	if string(got) != "test-fake-passphrase-B" {
		t.Errorf("got %q; want %q", got, "test-fake-passphrase-B")
	}
}

// TestReadPassphraseFrom_RejectsEmpty — empty input MUST be rejected
// at the reader. Defense in depth — the KDF refuses empty passphrases
// too, but the reader gives a clearer error.
func TestReadPassphraseFrom_RejectsEmpty(t *testing.T) {
	in := bytes.NewBufferString("\n")
	_, err := ReadPassphraseFrom(in)
	if err == nil {
		t.Fatalf("ReadPassphraseFrom accepted empty input; want error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty'; got %v", err)
	}
}

// TestReadValueFrom — for `secret set NAME`, the value is read from
// stdin with the same trim discipline as the passphrase.
func TestReadValueFrom_StripsTrailingNewline(t *testing.T) {
	in := bytes.NewBufferString("test-fake-value-A\n")
	got, err := ReadValueFrom(in)
	if err != nil {
		t.Fatalf("ReadValueFrom: %v", err)
	}
	defer Zero(got)
	if string(got) != "test-fake-value-A" {
		t.Errorf("got %q; want %q", got, "test-fake-value-A")
	}
}
