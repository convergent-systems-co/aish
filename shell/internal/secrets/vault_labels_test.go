package secrets

import (
	"slices"
	"testing"
)

// TestVault_SetWithLabels_RoundTrip — setting an entry with labels
// persists the labels and exposes them via LabelsFor.
func TestVault_SetWithLabels_RoundTrip(t *testing.T) {
	home := t.TempDir()
	v := openTestVault(t, home)
	defer v.Close()

	const name = "API_KEY_A"
	if err := v.SetWithLabels(name, []byte("[REDACTED:test-value-A]"), []string{"work"}); err != nil {
		t.Fatalf("SetWithLabels: %v", err)
	}
	labels, err := v.LabelsFor(name)
	if err != nil {
		t.Fatalf("LabelsFor: %v", err)
	}
	if !slices.Equal(labels, []string{"work"}) {
		t.Fatalf("LabelsFor = %v, want [work]", labels)
	}
}

// TestVault_ListWithLabel_FiltersAndIncludesUnlabeled — visibility
// rules from v0.3-fu-secrets §"#105 Persona-bound secrets":
//   - unlabeled entries are always visible
//   - labeled entries are visible only under matching label
//   - labeled entries are hidden under non-matching label
func TestVault_ListWithLabel_FiltersAndIncludesUnlabeled(t *testing.T) {
	home := t.TempDir()
	v := openTestVault(t, home)
	defer v.Close()

	mustSetWithLabels(t, v, "WORK_KEY", "[REDACTED:test-value-W]", []string{"work"})
	mustSetWithLabels(t, v, "PERSONAL_KEY", "[REDACTED:test-value-P]", []string{"personal"})
	mustSet(t, v, "LEGACY_KEY", "[REDACTED:test-value-L]") // no labels

	workVisible := v.ListWithLabel("work")
	if !slices.Equal(workVisible, []string{"LEGACY_KEY", "WORK_KEY"}) {
		t.Errorf("ListWithLabel(work) = %v, want [LEGACY_KEY WORK_KEY]", workVisible)
	}
	personalVisible := v.ListWithLabel("personal")
	if !slices.Equal(personalVisible, []string{"LEGACY_KEY", "PERSONAL_KEY"}) {
		t.Errorf("ListWithLabel(personal) = %v, want [LEGACY_KEY PERSONAL_KEY]", personalVisible)
	}
	all := v.ListWithLabel("")
	if !slices.Equal(all, []string{"LEGACY_KEY", "PERSONAL_KEY", "WORK_KEY"}) {
		t.Errorf("ListWithLabel(\"\") = %v, want full list", all)
	}
}

// TestVault_Set_PreservesLabelsOnReWrite — plain Set on an existing
// labeled entry preserves the labels (the "rotate value, not scope"
// contract).
func TestVault_Set_PreservesLabelsOnReWrite(t *testing.T) {
	home := t.TempDir()
	v := openTestVault(t, home)
	defer v.Close()

	mustSetWithLabels(t, v, "K", "[REDACTED:test-value-1]", []string{"work"})
	mustSet(t, v, "K", "[REDACTED:test-value-2]")

	labels, err := v.LabelsFor("K")
	if err != nil {
		t.Fatalf("LabelsFor: %v", err)
	}
	if !slices.Equal(labels, []string{"work"}) {
		t.Fatalf("plain Set wiped labels; LabelsFor = %v, want [work]", labels)
	}
}

// TestVault_SetWithLabels_NilClearsLabels — explicit clear via
// SetWithLabels with empty/nil slice.
func TestVault_SetWithLabels_NilClearsLabels(t *testing.T) {
	home := t.TempDir()
	v := openTestVault(t, home)
	defer v.Close()

	mustSetWithLabels(t, v, "K", "[REDACTED:test-value-1]", []string{"work"})
	if err := v.SetWithLabels("K", []byte("[REDACTED:test-value-2]"), nil); err != nil {
		t.Fatalf("SetWithLabels nil: %v", err)
	}
	labels, _ := v.LabelsFor("K")
	if len(labels) != 0 {
		t.Errorf("SetWithLabels(nil) should clear labels; got %v", labels)
	}
}

// openTestVault opens a vault under home with fast KDF params for
// unit-test latency.
func openTestVault(t *testing.T, home string) *Vault {
	t.Helper()
	v, err := OpenVault(home, []byte("test-passphrase-labels"),
		KDFParams{Time: 1, Memory: 8 * 1024, Parallelism: 1, KeyLen: KeySize})
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	return v
}

func mustSet(t *testing.T, v *Vault, name, val string) {
	t.Helper()
	if err := v.Set(name, []byte(val)); err != nil {
		t.Fatalf("Set %s: %v", name, err)
	}
}

func mustSetWithLabels(t *testing.T, v *Vault, name, val string, labels []string) {
	t.Helper()
	if err := v.SetWithLabels(name, []byte(val), labels); err != nil {
		t.Fatalf("SetWithLabels %s: %v", name, err)
	}
}
