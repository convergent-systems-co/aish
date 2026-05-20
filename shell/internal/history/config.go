package history

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// DefaultSnapshotMaxBytes is the per-file size limit applied when
// ~/.aish/config.toml does not specify a value. Sourced from the v0.1-4
// acceptance criteria: 100 MB.
const DefaultSnapshotMaxBytes int64 = 100 * 1024 * 1024

// Config is the typed projection of ~/.aish/config.toml's [history]
// section. New keys are added here; the rest of the package consumes
// the typed struct rather than poking at TOML.
type Config struct {
	// SnapshotMaxBytes is the maximum per-file size that the
	// Snapshotter will copy. Anything larger is recorded as
	// OpSkipped with SkipReason == ReasonOversize.
	SnapshotMaxBytes int64
}

// raw mirrors the TOML schema; it exists because Config is the
// canonical exported shape, while the wire shape uses snake_case keys
// nested under [history].
type rawConfig struct {
	History struct {
		SnapshotMaxBytes int64 `toml:"snapshot_max_bytes"`
	} `toml:"history"`
}

// LoadConfig reads ~/.aish/config.toml (the homeDir argument is the
// directory holding the config file — typically `$HOME/.aish`). A
// missing file, an unreadable file, or a malformed TOML body all
// degrade to defaults; the shell never fails to start because of a
// config defect.
func LoadConfig(homeDotAish string) Config {
	cfg := Config{SnapshotMaxBytes: DefaultSnapshotMaxBytes}
	if homeDotAish == "" {
		return cfg
	}
	data, err := os.ReadFile(filepath.Join(homeDotAish, "config.toml"))
	if err != nil {
		return cfg
	}
	var raw rawConfig
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return cfg
	}
	if raw.History.SnapshotMaxBytes > 0 {
		cfg.SnapshotMaxBytes = raw.History.SnapshotMaxBytes
	}
	return cfg
}
