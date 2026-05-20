package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadConfig(dir)
	if cfg.SnapshotMaxBytes != DefaultSnapshotMaxBytes {
		t.Errorf("default SnapshotMaxBytes = %d, want %d", cfg.SnapshotMaxBytes, DefaultSnapshotMaxBytes)
	}
}

func TestConfigReadsTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `[history]
snapshot_max_bytes = 2048
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig(dir)
	if cfg.SnapshotMaxBytes != 2048 {
		t.Errorf("got %d want 2048", cfg.SnapshotMaxBytes)
	}
}

// TestConfigSurvivesMalformedTOML verifies that a broken config file
// degrades gracefully to defaults rather than poisoning shell startup.
// This is consistent with how the cache opens on a broken DB (returns
// nil cache, shell still runs).
func TestConfigSurvivesMalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not = valid toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig(dir)
	if cfg.SnapshotMaxBytes != DefaultSnapshotMaxBytes {
		t.Errorf("malformed TOML should default, got %d", cfg.SnapshotMaxBytes)
	}
}
