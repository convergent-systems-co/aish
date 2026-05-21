package persona

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BindingFileName is the TOML file holding identity → persona
// mappings. One key per identity name, value is the persona name to
// activate when that identity is selected.
//
// Lives at ~/.aish/identity-persona.toml. The persona package owns
// this file end-to-end so the v0.3-5.1 follow-up can ship without
// extending the secrets.Identity struct (which is TL_KEYCHAIN's
// territory in parallel — boundary-respecting workaround documented
// in .artifacts/plans/v0.3-fu-persona.md alternatives table C).
const BindingFileName = "identity-persona.toml"

// ReadBinding returns the persona name bound to identityName, or ""
// when no binding exists. Never errors — a missing file, malformed
// row, or unknown identity all return "".
//
// Behaviour matches ReadActivePersona's posture: the read side of
// the persona engine is forgiving so the shell never refuses to
// start because of one mis-edited config.
func ReadBinding(homeDir, identityName string) string {
	if homeDir == "" || identityName == "" {
		return ""
	}
	bindings, err := readAllBindings(homeDir)
	if err != nil {
		return ""
	}
	return bindings[identityName]
}

// WriteBinding persists the identityName → personaName binding,
// preserving every other key in the file. An empty personaName
// removes the binding. The file is created when absent; its parent
// directory is created with 0o700.
//
// Validation: identityName + personaName MUST satisfy the persona
// name regex (which also matches the identity regex's character set
// minus the leading-letter rule — bindings are identity-keyed but
// stored in persona-namespace).
func WriteBinding(homeDir, identityName, personaName string) error {
	if homeDir == "" {
		return errors.New("persona binding: $HOME not set")
	}
	if identityName == "" {
		return errors.New("persona binding: empty identity name")
	}
	// Allow either-naming-style identities (the secrets package's
	// regex permits leading underscore/digit in some forms). The
	// persona side validates personaName against persona's own regex
	// (lowercase-letters-digits-dashes) — that's the canonical
	// persona name shape.
	if personaName != "" && !nameRe.MatchString(personaName) {
		return fmt.Errorf("persona binding: invalid persona name %q", personaName)
	}
	bindings, err := readAllBindings(homeDir)
	if err != nil {
		return err
	}
	if personaName == "" {
		delete(bindings, identityName)
	} else {
		bindings[identityName] = personaName
	}
	dir := filepath.Join(homeDir, ConfigDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("persona binding: create dir: %w", err)
	}
	path := filepath.Join(dir, BindingFileName)

	// Render the file. Header comment + one key=value per line.
	var sb strings.Builder
	sb.WriteString("# aish identity → persona bindings\n")
	sb.WriteString("# Written by `aish identity use <name> --persona <p>`.\n\n")
	// Stable ordering — bindings are small; sort by identity name so
	// diffs are predictable.
	names := make([]string, 0, len(bindings))
	for k := range bindings {
		names = append(names, k)
	}
	sortStrings(names)
	for _, n := range names {
		sb.WriteString(fmt.Sprintf("%s = %q\n", n, bindings[n]))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

// AllBindings returns a copy of every identity → persona mapping in
// the bindings file. Exposed for `persona show` integrations and
// (future) `identity show` displays. Returns an empty map when the
// file is absent.
func AllBindings(homeDir string) (map[string]string, error) {
	if homeDir == "" {
		return map[string]string{}, nil
	}
	return readAllBindings(homeDir)
}

// readAllBindings is the line-oriented TOML reader for
// ~/.aish/identity-persona.toml. The format is intentionally
// minimal (one `key = "value"` per line, comments via `#`) so the
// shell does not need to pull in a TOML dependency in the
// secret-package-adjacent path. Mirrors secrets.parseTOMLString's
// posture.
func readAllBindings(homeDir string) (map[string]string, error) {
	out := map[string]string{}
	path := filepath.Join(homeDir, ConfigDirName, BindingFileName)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, fmt.Errorf("persona binding: open: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// No section headers in the bindings file; ignore them if any
		// turn up (forward-compat).
		if strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	return out, scanner.Err()
}

// sortStrings is a tiny helper so the bindings file is written in a
// stable order. Stdlib sort would be fine; this avoids importing
// sort just to call sort.Strings in one place.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
