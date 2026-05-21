package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteActiveTheme_PreservesSiblingSections — #80 acceptance:
// writing the theme must not delete sibling sections. Pre-seed a
// config.toml with [telemetry] and a leading comment; assert both
// survive after WriteActiveTheme.
func TestWriteActiveTheme_PreservesSiblingSections(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".aish")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ConfigFileName)

	initial := `# user comment at top

[telemetry]
opt_in_local = true
opt_in_aggregate = false

[theme]
active = "default"
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteActiveTheme(tmp, "nord-powerline"); err != nil {
		t.Fatalf("WriteActiveTheme: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)

	if !strings.Contains(content, "# user comment at top") {
		t.Errorf("top comment lost; got:\n%s", content)
	}
	if !strings.Contains(content, "[telemetry]") {
		t.Errorf("[telemetry] section lost; got:\n%s", content)
	}
	if !strings.Contains(content, "opt_in_local = true") {
		t.Errorf("telemetry.opt_in_local lost; got:\n%s", content)
	}
	if !strings.Contains(content, `active = "nord-powerline"`) {
		t.Errorf("new theme not written; got:\n%s", content)
	}
	if strings.Contains(content, `active = "default"`) {
		t.Errorf("old theme value still present; got:\n%s", content)
	}
}

// TestWriteActiveTheme_AppendsThemeSectionWhenAbsent — pre-seed a file
// that has [telemetry] but no [theme]; assert [theme] is appended.
func TestWriteActiveTheme_AppendsThemeSectionWhenAbsent(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".aish")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ConfigFileName)

	initial := `[telemetry]
opt_in_local = true
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveTheme(tmp, "monokai"); err != nil {
		t.Fatalf("WriteActiveTheme: %v", err)
	}
	got := ReadActiveTheme(tmp)
	if got != "monokai" {
		t.Errorf("ReadActiveTheme = %q, want monokai", got)
	}
	// Sanity-check the existing section is intact.
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "opt_in_local = true") {
		t.Errorf("[telemetry] section lost after append; got:\n%s", raw)
	}
}

// TestWriteActiveTheme_IdempotentRoundTrip — writing the same theme
// twice produces byte-identical output the second time. Round-trip
// stability is a non-negotiable for any merge-aware writer.
func TestWriteActiveTheme_IdempotentRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := WriteActiveTheme(tmp, "nord-powerline"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	path := filepath.Join(tmp, ".aish", ConfigFileName)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveTheme(tmp, "nord-powerline"); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestWriteActiveTheme_PreservesUnknownKeysInThemeSection — a future
// `[theme] cursor = "..."` key shouldn't get nuked by writing
// `active`. The writer touches `active = ...` only.
func TestWriteActiveTheme_PreservesUnknownKeysInThemeSection(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".aish")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ConfigFileName)

	initial := `[theme]
active = "default"
nerd_fonts = true
custom_glyph = "★"
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveTheme(tmp, "monokai"); err != nil {
		t.Fatalf("WriteActiveTheme: %v", err)
	}
	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !strings.Contains(content, "nerd_fonts = true") {
		t.Errorf("nerd_fonts key lost; got:\n%s", content)
	}
	if !strings.Contains(content, `custom_glyph = "★"`) {
		t.Errorf("custom_glyph key lost; got:\n%s", content)
	}
	if !strings.Contains(content, `active = "monokai"`) {
		t.Errorf("active not updated; got:\n%s", content)
	}
}
