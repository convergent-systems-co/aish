package theme

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDirName is the per-user aish config directory under $HOME.
const ConfigDirName = ".aish"

// ConfigFileName is the active-theme persistence file inside ConfigDirName.
const ConfigFileName = "config.toml"

// ReadActiveTheme returns the active theme name persisted under
// $HOME/.aish/config.toml, or "" if the file is missing / unparseable /
// the key isn't set. Never errors — the consumer falls through to the
// default theme when this returns empty.
//
// Minimal TOML reader: handles `[section]` headers and `key = "value"`
// lines, with `#` comments and blank lines tolerated. NOT a full TOML
// parser; we'll swap it for the real thing in v0.3-1's RC-file work.
func ReadActiveTheme(homeDir string) string {
	if homeDir == "" {
		return ""
	}
	path := filepath.Join(homeDir, ConfigDirName, ConfigFileName)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	currentSection := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if currentSection != "theme" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "active" {
			// Strip surrounding double quotes; bare values are also fine.
			v = strings.TrimSpace(v)
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			return v
		}
	}
	return ""
}

// WriteActiveTheme persists the active theme name to
// $HOME/.aish/config.toml. The directory is created with 0700 if it
// doesn't exist; the file is written with 0644.
//
// Limitation (#80 minimum-viable): this writer OVERWRITES the file with
// only the [theme] section. Other keys/sections present in the file are
// lost. A full RFC-conformant TOML reader+writer arrives with v0.3-1's
// RC-file loading; until then, don't hand-edit config.toml alongside
// `aish theme set`. The next iteration here will merge instead of
// overwrite.
func WriteActiveTheme(homeDir, name string) error {
	if homeDir == "" {
		return errors.New("theme: $HOME not set; cannot persist active theme")
	}
	if name == "" {
		return errors.New("theme: empty name")
	}
	dir := filepath.Join(homeDir, ConfigDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("theme: create config dir: %w", err)
	}
	path := filepath.Join(dir, ConfigFileName)
	content := fmt.Sprintf("# aish config — managed by `aish theme set`\n\n[theme]\nactive = %q\n", name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("theme: write config: %w", err)
	}
	return nil
}
