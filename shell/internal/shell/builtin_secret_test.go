package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// secretTestShell returns a Shell with $HOME pointed at a tempdir and
// the secrets KDF clamped to a unit-test-fast cost. Returns the home
// path for assertions.
func secretTestShell(t *testing.T) (*Shell, string) {
	t.Helper()
	home := t.TempDir()
	s := New()
	if err := s.env.Set("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	s.SetSecretKDFParamsForTesting(secrets.KDFParams{Time: 1, Memory: 8 * 1024, Parallelism: 1, KeyLen: secrets.KeySize})
	return s, home
}

// TestSecret_Usage — bare `secret` prints usage to stdout, exits 0.
func TestSecret_Usage(t *testing.T) {
	s, _ := secretTestShell(t)
	var stdout, stderr bytes.Buffer
	code := s.secretBuiltin(nil, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("secret (no args) exit = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "set NAME") {
		t.Errorf("usage should mention `set NAME`; got:\n%s", stdout.String())
	}
}

// TestSecret_Set_ReadsValueFromStdin — `secret set DEMO` consumes the
// value from stdin, prints a non-value confirmation to stdout, and
// the resulting vault file is 0600.
func TestSecret_Set_ReadsValueFromStdin(t *testing.T) {
	s, home := secretTestShell(t)
	stdin := bytes.NewBufferString("test-fake-passphrase-A\ntest-fake-value-A\n")
	var stdout, stderr bytes.Buffer
	code := s.secretBuiltin([]string{"set", "DEMO_KEY"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("set DEMO_KEY exit = %d; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), "test-fake-value-A") {
		t.Fatalf("stdout leaked the value:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "test-fake-value-A") {
		t.Fatalf("stderr leaked the value:\n%s", stderr.String())
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(home, ".aish", "vault", "vault.json"))
		if err != nil {
			t.Fatalf("stat vault: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("vault perm = %o; want 0600", perm)
		}
	}
}

// TestSecret_List_AfterSet — set two entries, list returns them
// sorted, no values appear anywhere.
func TestSecret_List_AfterSet(t *testing.T) {
	s, _ := secretTestShell(t)
	for _, name := range []string{"BETA", "ALPHA"} {
		stdin := bytes.NewBufferString("test-fake-passphrase-B\ntest-fake-value-list\n")
		var stdout, stderr bytes.Buffer
		if code := s.secretBuiltin([]string{"set", name}, stdin, &stdout, &stderr); code != 0 {
			t.Fatalf("set %s exit %d: %s", name, code, stderr.String())
		}
	}

	// Drop the cached key so list re-prompts (mirrors what would
	// happen in a fresh session). The convention: SecretLock clears.
	s.SecretLockForTesting()

	stdin := bytes.NewBufferString("test-fake-passphrase-B\n")
	var stdout, stderr bytes.Buffer
	code := s.secretBuiltin([]string{"list"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list exit = %d; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "ALPHA") || !strings.Contains(out, "BETA") {
		t.Errorf("list output missing names; got:\n%s", out)
	}
	if strings.Contains(out, "test-fake-value-list") {
		t.Errorf("list leaked value:\n%s", out)
	}
	// Names appear in sorted order.
	if i := strings.Index(out, "ALPHA"); i < 0 || i > strings.Index(out, "BETA") {
		t.Errorf("list not sorted:\n%s", out)
	}
}

// TestSecret_Rm — set then rm; list shows nothing.
func TestSecret_Rm(t *testing.T) {
	s, _ := secretTestShell(t)
	stdin := bytes.NewBufferString("test-fake-passphrase-C\ntest-fake-value-rm\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "GONE"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("set: %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := s.secretBuiltin([]string{"rm", "GONE"}, bytes.NewBufferString("test-fake-passphrase-C\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("rm: %d %s", code, stderr.String())
	}
	s.SecretLockForTesting()
	stdout.Reset()
	stderr.Reset()
	if code := s.secretBuiltin([]string{"list"}, bytes.NewBufferString("test-fake-passphrase-C\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("list: %d %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "GONE") {
		t.Errorf("rm did not remove; list:\n%s", stdout.String())
	}
}

// TestSecret_Get_DoesNotEchoValue — the core security gate. `secret
// get NAME` MUST NOT write the value to stdout or stderr under any
// circumstances. The clipboard call is stubbed; the test asserts that
// no value-bearing byte slice escapes through stdio.
func TestSecret_Get_DoesNotEchoValue(t *testing.T) {
	s, _ := secretTestShell(t)
	// Stub the clipboard so the test doesn't depend on pbcopy etc.
	// The stub just captures the value into a local buffer so we can
	// assert it was delivered to the clipboard layer (not to stdio).
	var clipboardCaptured []byte
	s.SetClipboardFnForTesting(func(value []byte) error {
		clipboardCaptured = append([]byte(nil), value...)
		return nil
	})

	stdin := bytes.NewBufferString("test-fake-passphrase-D\ntest-fake-value-get\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "DEMO"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("set: %d %s", code, stderr.String())
	}

	// New session: clear the cached key so the test exercises the
	// full unlock path on get.
	s.SecretLockForTesting()

	stdout.Reset()
	stderr.Reset()
	stdin = bytes.NewBufferString("test-fake-passphrase-D\n")
	code := s.secretBuiltin([]string{"get", "DEMO"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("get exit = %d; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	if strings.Contains(stdout.String(), "test-fake-value-get") {
		t.Fatalf("stdout leaked value:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "test-fake-value-get") {
		t.Fatalf("stderr leaked value:\n%s", stderr.String())
	}
	if !bytes.Equal(clipboardCaptured, []byte("test-fake-value-get")) {
		t.Errorf("clipboard captured = %q; want %q", clipboardCaptured, "test-fake-value-get")
	}
	if !strings.Contains(stdout.String(), "clipboard") {
		t.Errorf("get should confirm via clipboard message; stdout:\n%s", stdout.String())
	}
}

// TestSecret_Get_WrongPassphrase — wrong passphrase exits non-zero
// with a uniform message and never decrypts.
func TestSecret_Get_WrongPassphrase(t *testing.T) {
	s, _ := secretTestShell(t)
	var captured []byte
	s.SetClipboardFnForTesting(func(value []byte) error {
		captured = value
		return nil
	})
	stdin := bytes.NewBufferString("test-fake-passphrase-E\ntest-fake-value-pp\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "DEMO"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("set: %d %s", code, stderr.String())
	}
	s.SecretLockForTesting()

	stdout.Reset()
	stderr.Reset()
	stdin = bytes.NewBufferString("test-fake-passphrase-WRONG\n")
	if code := s.secretBuiltin([]string{"get", "DEMO"}, stdin, &stdout, &stderr); code == 0 {
		t.Fatalf("get with wrong passphrase exit = 0; want non-zero")
	}
	if len(captured) != 0 {
		t.Errorf("clipboard was written with wrong passphrase: %q", captured)
	}
	if strings.Contains(stderr.String(), "test-fake-value-pp") {
		t.Errorf("stderr leaked plaintext on wrong-passphrase path:\n%s", stderr.String())
	}
}

// TestSecret_Get_MissingName — exits non-zero with a clear error,
// does not touch the clipboard.
func TestSecret_Get_MissingName(t *testing.T) {
	s, _ := secretTestShell(t)
	var captured []byte
	s.SetClipboardFnForTesting(func(value []byte) error {
		captured = value
		return nil
	})
	stdin := bytes.NewBufferString("test-fake-passphrase-F\ntest-fake-value-mn\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "EXISTING"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("set: %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := s.secretBuiltin([]string{"get", "MISSING"}, bytes.NewBufferString(""), &stdout, &stderr); code == 0 {
		t.Fatalf("get MISSING exit = 0; want non-zero")
	}
	if len(captured) != 0 {
		t.Errorf("clipboard was touched on missing-name path")
	}
}

// TestSecret_ResolveTier — `secret` is a built-in, so the
// syntax-highlight tier classifier should report TierBuiltin.
func TestSecret_ResolveTier(t *testing.T) {
	s, _ := secretTestShell(t)
	if got := s.ResolveTier("secret"); got != tierBuiltinExpected() {
		t.Errorf("ResolveTier(secret) = %v; want builtin", got)
	}
}

// TestSecret_ByteByByteStdinDiscipline — the secret built-ins MUST
// read stdin byte-by-byte (no bufio prefetch) so a follow-up REPL
// command in the same input stream is preserved. The test drives the
// shell's full Run loop with a stdin that contains `secret set`, the
// passphrase, the value, AND a follow-up command — the follow-up
// command MUST still execute (proving the secret built-in did not
// over-read).
func TestSecret_ByteByByteStdinDiscipline(t *testing.T) {
	s, _ := secretTestShell(t)
	s.SetClipboardFnForTesting(func([]byte) error { return nil })
	// stdin: `secret set DEMO`, passphrase, value, `secret list`, EOF.
	// If readLineRaw works correctly, both `secret set DEMO` and
	// `secret list` execute. If we buffered too aggressively, the
	// list command would be lost in the bufio buffer of `set`.
	stdin := strings.NewReader("secret set DEMO\ntest-fake-passphrase-Z\ntest-fake-value-Z\nsecret list\n")
	var stdout, stderr bytes.Buffer
	if err := s.Run(stdin, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Confirm both commands ran. `set` writes "stored DEMO"; `list`
	// emits the name "DEMO".
	out := stdout.String()
	if !strings.Contains(out, "stored DEMO") {
		t.Errorf("set DEMO did not run (no 'stored DEMO'):\n%s", out)
	}
	if !strings.Contains(out, "DEMO") {
		t.Errorf("list did not run after set; full stdout:\n%s", out)
	}
	// More telling: `secret list` emits the name on its own line in
	// the stdout buffer; the prompt then re-renders for the next
	// REPL turn. Look for the name preceded by EITHER a newline or
	// the start of a line so we don't false-positive on a literal
	// "DEMO" inside a prompt string. The "stored DEMO" line comes
	// from `set` and is also valid evidence — but we want the SECOND
	// occurrence (the list output) to confirm both ran.
	if strings.Count(out, "DEMO") < 2 {
		t.Errorf("expected DEMO to appear twice (stored + list); got:\n%s", out)
	}
	if strings.Contains(out, "test-fake-value-Z") {
		t.Errorf("value leaked to stdout:\n%s", out)
	}
	if strings.Contains(stderr.String(), "test-fake-value-Z") {
		t.Errorf("value leaked to stderr:\n%s", stderr.String())
	}
}

// TestSecret_DispatchThroughShell — sending `secret set NAME` through
// the top-level dispatcher works end-to-end (proves the dispatch
// branch is wired).
func TestSecret_DispatchThroughShell(t *testing.T) {
	s, _ := secretTestShell(t)
	var captured []byte
	s.SetClipboardFnForTesting(func(value []byte) error {
		// Copy the bytes — the built-in zeroes the slice immediately
		// after the clipboard call returns.
		captured = append([]byte(nil), value...)
		return nil
	})
	stdin := bytes.NewBufferString("test-fake-passphrase-G\ntest-fake-value-disp\n")
	var stdout, stderr bytes.Buffer
	if err := s.dispatch("secret set DISPATCHED", stdin, &stdout, &stderr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if s.LastExit() != 0 {
		t.Fatalf("dispatch set exit = %d; stderr=%q", s.LastExit(), stderr.String())
	}
	// Now get via dispatch.
	s.SecretLockForTesting()
	stdout.Reset()
	stderr.Reset()
	if err := s.dispatch("secret get DISPATCHED", bytes.NewBufferString("test-fake-passphrase-G\n"), &stdout, &stderr); err != nil {
		t.Fatalf("dispatch get: %v", err)
	}
	if s.LastExit() != 0 {
		t.Fatalf("dispatch get exit = %d; stderr=%q", s.LastExit(), stderr.String())
	}
	if string(captured) != "test-fake-value-disp" {
		t.Errorf("clipboard captured = %q; want %q", captured, "test-fake-value-disp")
	}
}
