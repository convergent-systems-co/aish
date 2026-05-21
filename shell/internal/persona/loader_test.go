package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadBundled_FiveCuratedPersonas — the engine ships exactly five
// curated personas. The set is the cold-start floor; tests catch drift.
func TestLoadBundled_FiveCuratedPersonas(t *testing.T) {
	t.Parallel()
	got, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("LoadBundled: count = %d, want 5", len(got))
	}
	wantNames := []string{"default", "mentor", "terse-veteran", "playful", "socratic"}
	have := map[string]bool{}
	for _, p := range got {
		have[p.Name] = true
	}
	for _, n := range wantNames {
		if !have[n] {
			t.Errorf("LoadBundled: missing %q", n)
		}
	}
}

// TestLoadBundled_AllValid — every bundled persona must pass Validate().
// Catches malformed TOML or schema-drift at build time.
func TestLoadBundled_AllValid(t *testing.T) {
	t.Parallel()
	got, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, p := range got {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			if err := p.Validate(); err != nil {
				t.Fatalf("Validate(%s): %v", p.Name, err)
			}
		})
	}
}

// TestLoader_UserOverridesBundled — a persona on disk under
// ~/.aish/personas/<name>.toml takes priority over the bundled
// version of the same name.
func TestLoader_UserOverridesBundled(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	personasDir := filepath.Join(tmp, "personas")
	if err := os.MkdirAll(personasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := `
name = "default"
version = 1
system_prompt = "override from user dir"

[tone]
verbosity = "terse"
formality = "neutral"
`
	if err := os.WriteFile(filepath.Join(personasDir, "default.toml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	loader, err := NewLoader(personasDir)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	got, ok := loader.Get("default")
	if !ok {
		t.Fatal("loader.Get(default): not found")
	}
	if !strings.Contains(got.SystemPrompt, "override from user dir") {
		t.Errorf("user override did not win: SystemPrompt = %q", got.SystemPrompt)
	}
}

// TestLoader_RejectsDuplicateInUserDir — if a user accidentally has two
// files declaring the same name field, Loader rejects to avoid a silent
// override.
func TestLoader_RejectsDuplicateInUserDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	personasDir := filepath.Join(tmp, "personas")
	if err := os.MkdirAll(personasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := `
name = "twin"
version = 1
system_prompt = "a"
[tone]
verbosity = "medium"
formality = "neutral"
`
	b := `
name = "twin"
version = 1
system_prompt = "b"
[tone]
verbosity = "medium"
formality = "neutral"
`
	if err := os.WriteFile(filepath.Join(personasDir, "one.toml"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personasDir, "two.toml"), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewLoader(personasDir); err == nil {
		t.Fatalf("NewLoader: nil error on duplicate user persona names; want rejection")
	}
}

// TestLoader_UnknownPersonaReturnsClearError
func TestLoader_UnknownPersonaReturnsClearError(t *testing.T) {
	t.Parallel()
	loader, err := NewLoader("")
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	_, ok := loader.Get("does-not-exist")
	if ok {
		t.Fatalf("Get(does-not-exist): ok=true, want false")
	}
}

// TestLoader_List_SortedByName — predictable output for the `persona
// list` built-in.
func TestLoader_List_SortedByName(t *testing.T) {
	t.Parallel()
	loader, err := NewLoader("")
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	got := loader.List()
	if len(got) < 5 {
		t.Fatalf("List: got %d, want >= 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Errorf("List not sorted: %q before %q", got[i-1].Name, got[i].Name)
		}
	}
}
