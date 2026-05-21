package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadActivePersona_Empty — missing file returns "".
func TestReadActivePersona_Empty(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if got := ReadActivePersona(tmp); got != "" {
		t.Fatalf("ReadActivePersona(empty): got %q, want \"\"", got)
	}
}

// TestWriteActivePersona_Fresh — creates config.toml + [persona] section.
func TestWriteActivePersona_Fresh(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := WriteActivePersona(tmp, "mentor"); err != nil {
		t.Fatalf("WriteActivePersona: %v", err)
	}
	got := ReadActivePersona(tmp)
	if got != "mentor" {
		t.Fatalf("ReadActivePersona: got %q, want mentor", got)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, ".aish", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[persona]") {
		t.Fatalf("config.toml missing [persona] section:\n%s", raw)
	}
}

// TestWriteActivePersona_PreservesSiblings — when [theme] and
// [telemetry] sections already exist, writing the persona key must not
// disturb them. Mirrors v0.2-5's #80 contract for theming.
func TestWriteActivePersona_PreservesSiblings(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".aish")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `# user config
[theme]
active = "dracula"
# trailing comment

[telemetry]
enabled = true
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteActivePersona(tmp, "playful"); err != nil {
		t.Fatalf("WriteActivePersona: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, `active = "dracula"`) {
		t.Errorf("theme.active stripped:\n%s", got)
	}
	if !strings.Contains(got, "[telemetry]") {
		t.Errorf("[telemetry] section stripped:\n%s", got)
	}
	if !strings.Contains(got, "trailing comment") {
		t.Errorf("comments stripped:\n%s", got)
	}
	if !strings.Contains(got, `active = "playful"`) {
		t.Errorf("persona.active missing:\n%s", got)
	}
}

// TestWriteActivePersona_Idempotent — round-trip writes are byte-stable.
func TestWriteActivePersona_Idempotent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := WriteActivePersona(tmp, "mentor"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(tmp, ".aish", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteActivePersona(tmp, "mentor"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(tmp, ".aish", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("WriteActivePersona not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
