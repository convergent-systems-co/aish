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
// Merge-aware (#80): when the file already exists, only the
// `[theme] active = ...` key is rewritten. Sibling sections
// (`[telemetry]`, etc.), comments, blank lines, key order, and
// custom keys inside `[theme]` (e.g. `nerd_fonts`, `cursor`) are
// preserved byte-for-byte. When the file doesn't exist, a fresh
// file is bootstrapped with a header comment + the `[theme]` section.
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

	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("theme: read config: %w", err)
		}
		// Fresh file: bootstrap with header + [theme] section.
		content := fmt.Sprintf("# aish config — managed by `aish theme set`\n\n[theme]\nactive = %q\n", name)
		if werr := os.WriteFile(path, []byte(content), 0o644); werr != nil {
			return fmt.Errorf("theme: write config: %w", werr)
		}
		return nil
	}

	merged := mergeActiveTheme(string(existing), name)
	if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("theme: write config: %w", err)
	}
	return nil
}

// mergeActiveTheme rewrites the `[theme] active = ...` key inside an
// existing config.toml text, preserving every other byte. Three cases:
//
//  1. `[theme]` section + `active` key present → replace the active line
//     in place.
//  2. `[theme]` section present, no `active` key → insert `active = "<name>"`
//     immediately after the `[theme]` header.
//  3. No `[theme]` section → append a new `[theme]` section at EOF,
//     separated from prior content by a blank line.
//
// Comments, blank lines, key order, and sibling sections survive
// unchanged. The line-by-line approach (rather than a full TOML
// round-trip) deliberately matches the existing ReadActiveTheme
// reader's strategy; a full TOML library lands with v0.3-1's RC work.
func mergeActiveTheme(existing, name string) string {
	newActive := fmt.Sprintf("active = %q", name)

	lines := strings.Split(existing, "\n")
	themeStart := -1
	activeLineIdx := -1
	currentSection := ""

	for i, raw := range lines {
		trim := strings.TrimSpace(raw)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			currentSection = strings.TrimSpace(trim[1 : len(trim)-1])
			if currentSection == "theme" && themeStart == -1 {
				themeStart = i
			}
			continue
		}
		if currentSection != "theme" {
			continue
		}
		k, _, ok := strings.Cut(trim, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == "active" {
			activeLineIdx = i
			break
		}
	}

	switch {
	case activeLineIdx >= 0:
		lines[activeLineIdx] = newActive
		return strings.Join(lines, "\n")

	case themeStart >= 0:
		// [theme] exists but no `active` key — insert after header.
		insertAt := themeStart + 1
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAt]...)
		out = append(out, newActive)
		out = append(out, lines[insertAt:]...)
		return strings.Join(out, "\n")

	default:
		// No [theme] section at all — append it cleanly.
		text := existing
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if strings.TrimSpace(text) != "" {
			text += "\n"
		}
		text += "[theme]\n" + newActive + "\n"
		return text
	}
}
