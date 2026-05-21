package persona

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDirName mirrors theme.ConfigDirName — the per-user aish
// config directory under $HOME.
const ConfigDirName = ".aish"

// ConfigFileName is the shared aish config file. The persona engine
// reads/writes the `[persona]` section; sibling sections (`[theme]`,
// `[telemetry]`) are preserved byte-for-byte.
const ConfigFileName = "config.toml"

// ReadActivePersona returns the active persona name persisted under
// $HOME/.aish/config.toml, or "" when the file is missing / the
// section / key isn't set. Never errors — the consumer falls through
// to the default persona on empty.
//
// Minimal line-oriented reader (no full TOML parse) — symmetric with
// theme.ReadActiveTheme. A full TOML round-trip lands in v0.3-1's
// RC-file work.
func ReadActivePersona(homeDir string) string {
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
		if currentSection != "persona" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "active" {
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			return v
		}
	}
	return ""
}

// WriteActivePersona persists the active persona name into
// $HOME/.aish/config.toml's [persona] section.
//
// Merge-aware: when the file already exists, only the
// `[persona] active = ...` key is rewritten. Sibling sections
// (`[theme]`, `[telemetry]`, etc.), comments, blank lines, key order,
// and other keys within `[persona]` are preserved byte-for-byte.
//
// When the file does not exist, a fresh file is bootstrapped with a
// header comment and the `[persona]` section.
func WriteActivePersona(homeDir, name string) error {
	if homeDir == "" {
		return errors.New("persona: $HOME not set; cannot persist active persona")
	}
	if name == "" {
		return errors.New("persona: empty name")
	}
	dir := filepath.Join(homeDir, ConfigDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("persona: create config dir: %w", err)
	}
	path := filepath.Join(dir, ConfigFileName)

	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("persona: read config: %w", err)
		}
		content := fmt.Sprintf("# aish config\n\n[persona]\nactive = %q\n", name)
		if werr := os.WriteFile(path, []byte(content), 0o644); werr != nil {
			return fmt.Errorf("persona: write config: %w", werr)
		}
		return nil
	}

	merged := mergeActivePersona(string(existing), name)
	if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("persona: write config: %w", err)
	}
	return nil
}

// mergeActivePersona rewrites the `[persona] active = ...` key inside
// an existing config.toml text, preserving every other byte. Same
// three-case shape as theme.mergeActiveTheme:
//
//  1. `[persona]` section + `active` key present → replace the
//     active line in place.
//  2. `[persona]` section present, no `active` key → insert
//     `active = "<name>"` immediately after the `[persona]` header.
//  3. No `[persona]` section → append a new `[persona]` section at
//     EOF, separated from prior content by a blank line.
func mergeActivePersona(existing, name string) string {
	newActive := fmt.Sprintf("active = %q", name)

	lines := strings.Split(existing, "\n")
	sectionStart := -1
	activeLineIdx := -1
	currentSection := ""

	for i, raw := range lines {
		trim := strings.TrimSpace(raw)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			currentSection = strings.TrimSpace(trim[1 : len(trim)-1])
			if currentSection == "persona" && sectionStart == -1 {
				sectionStart = i
			}
			continue
		}
		if currentSection != "persona" {
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

	case sectionStart >= 0:
		insertAt := sectionStart + 1
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAt]...)
		out = append(out, newActive)
		out = append(out, lines[insertAt:]...)
		return strings.Join(out, "\n")

	default:
		text := existing
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if strings.TrimSpace(text) != "" {
			text += "\n"
		}
		text += "[persona]\n" + newActive + "\n"
		return text
	}
}
