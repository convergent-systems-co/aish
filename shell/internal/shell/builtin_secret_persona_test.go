package shell

import (
	"bytes"
	"strings"
	"testing"
)

// TestSecret_PersonaBound_SetTagsActivePersona — when an active
// persona is set, `secret set NAME` tags the new entry with that
// persona name. The confirmation message MUST mention the scope.
func TestSecret_PersonaBound_SetTagsActivePersona(t *testing.T) {
	s, _ := secretTestShell(t)
	s.activePersona = "work"

	stdin := bytes.NewBufferString("test-fake-passphrase-PER\ntest-fake-value-A\n")
	var stdout, stderr bytes.Buffer
	code := s.secretBuiltin([]string{"set", "WORK_KEY"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("set WORK_KEY exit = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "(persona work)") {
		t.Errorf("set confirmation should name the persona; got:\n%s", stdout.String())
	}
}

// TestSecret_PersonaBound_ListFiltersByPersona — set one secret under
// `work`, switch to `personal`, list MUST NOT include the work
// secret. `--all` overrides.
func TestSecret_PersonaBound_ListFiltersByPersona(t *testing.T) {
	s, _ := secretTestShell(t)
	s.activePersona = "work"

	stdin := bytes.NewBufferString("test-fake-passphrase-FILTER\ntest-fake-value-A\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "WORK_KEY"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("set WORK_KEY exit = %d", code)
	}

	// Switch persona, list (the filter MUST hide the work entry).
	s.activePersona = "personal"

	s.SecretLockForTesting()
	stdin2 := bytes.NewBufferString("test-fake-passphrase-FILTER\n")
	var out2, err2 bytes.Buffer
	if code := s.secretBuiltin([]string{"list"}, stdin2, &out2, &err2); code != 0 {
		t.Fatalf("list exit = %d; stderr=%q", code, err2.String())
	}
	if strings.Contains(out2.String(), "WORK_KEY") {
		t.Errorf("personal-persona list leaked WORK_KEY:\n%s", out2.String())
	}

	// --all overrides the filter.
	s.SecretLockForTesting()
	stdin3 := bytes.NewBufferString("test-fake-passphrase-FILTER\n")
	var out3, err3 bytes.Buffer
	if code := s.secretBuiltin([]string{"list", "--all"}, stdin3, &out3, &err3); code != 0 {
		t.Fatalf("list --all exit = %d; stderr=%q", code, err3.String())
	}
	if !strings.Contains(out3.String(), "WORK_KEY") {
		t.Errorf("--all should include WORK_KEY; got:\n%s", out3.String())
	}
}

// TestSecret_PersonaBound_UnlabeledEntriesStayVisible — entries
// created BEFORE the persona engine was active (no labels) MUST
// remain visible under any active persona. The backwards-compat
// guarantee in the threat-model table.
func TestSecret_PersonaBound_UnlabeledEntriesStayVisible(t *testing.T) {
	s, _ := secretTestShell(t)
	// No active persona — store an entry unlabeled.
	s.activePersona = ""
	stdin := bytes.NewBufferString("test-fake-passphrase-LEGACY\ntest-fake-value-A\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "LEGACY_KEY"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("set LEGACY_KEY exit = %d", code)
	}

	// Now activate a persona and list.
	s.activePersona = "work"
	s.SecretLockForTesting()
	stdin2 := bytes.NewBufferString("test-fake-passphrase-LEGACY\n")
	var out2, err2 bytes.Buffer
	if code := s.secretBuiltin([]string{"list"}, stdin2, &out2, &err2); code != 0 {
		t.Fatalf("list under work persona: exit %d, stderr=%q", code, err2.String())
	}
	if !strings.Contains(out2.String(), "LEGACY_KEY") {
		t.Errorf("legacy unlabeled entry should be visible under any persona; got:\n%s", out2.String())
	}
}

// TestSecret_PersonaBound_ReSetPreservesLabels — overwriting an
// existing entry under a different active persona MUST NOT silently
// re-scope it to the new persona. Confirmation message names the
// preserved labels.
func TestSecret_PersonaBound_ReSetPreservesLabels(t *testing.T) {
	s, _ := secretTestShell(t)
	s.activePersona = "work"
	stdin := bytes.NewBufferString("test-fake-passphrase-RESET\ntest-fake-value-A\n")
	var stdout, stderr bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "SHARED"}, stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("initial set: exit %d, stderr=%q", code, stderr.String())
	}

	// Switch persona, re-set the same name. Labels should be preserved.
	s.activePersona = "personal"
	s.SecretLockForTesting()
	stdin2 := bytes.NewBufferString("test-fake-passphrase-RESET\ntest-fake-value-B\n")
	var out2, err2 bytes.Buffer
	if code := s.secretBuiltin([]string{"set", "SHARED"}, stdin2, &out2, &err2); code != 0 {
		t.Fatalf("re-set: exit %d, stderr=%q", code, err2.String())
	}
	confirm := out2.String()
	if !strings.Contains(confirm, "labels preserved") {
		t.Errorf("re-set confirmation should mention preserved labels; got:\n%s", confirm)
	}
	if !strings.Contains(confirm, "work") {
		t.Errorf("re-set confirmation should name the preserved persona 'work'; got:\n%s", confirm)
	}
}
