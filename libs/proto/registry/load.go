package registry

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirName is the standard subdirectory under ~/.aish for plugin
// manifests. Both the shell-side loader and the admin CLI use this
// constant so the layout stays consistent across modules.
const DirName = "plugins"

// Entry is one resolved manifest plus the directory it lives in.
type Entry struct {
	Manifest Manifest
	// Dir is the per-plugin directory (root/<name>) so callers can
	// surface a useful path in diagnostics.
	Dir string
}

// Load walks root for plugin subdirectories, parses + signature-
// verifies each manifest, and returns the validated entries. A
// manifest that fails to parse or fails signature verification is
// skipped with a warning written to warnings (when non-nil) — load
// is "best-effort" so one bad manifest cannot brick the shell's
// startup.
//
// Returned entries are sorted by Manifest.Name for stable list
// output.
//
// If root does not exist, Load returns an empty slice and nil error —
// "no plugins installed" is a normal state, not a failure.
//
// Load runs only the signature check (VerifyManifestSignature), NOT
// the binary-hash recompute (VerifyManifestAgainstBinary). The
// binary check happens lazily at spawn time — at boot we don't want
// to read every plugin binary off disk just to enumerate them. A
// tampered binary surfaces when the user actually invokes inference,
// or via the `plugin verify` built-in.
func Load(root string, warnings io.Writer) ([]Entry, error) {
	st, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry: stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("registry: %s is not a directory", root)
	}

	dirEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", root, err)
	}

	var out []Entry
	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		pluginDir := filepath.Join(root, de.Name())
		manifestPath := filepath.Join(pluginDir, ManifestFileName)
		m, err := ReadManifest(manifestPath)
		if err != nil {
			if warnings != nil {
				fmt.Fprintf(warnings, "aish: registry: skip %s: %v\n", de.Name(), err)
			}
			continue
		}
		if m.Name != de.Name() && warnings != nil {
			fmt.Fprintf(warnings,
				"aish: registry: manifest %s: name=%q does not match directory %q\n",
				manifestPath, m.Name, de.Name())
		}
		if err := VerifyManifestSignature(m); err != nil {
			if warnings != nil {
				fmt.Fprintf(warnings, "aish: registry: skip %s: %v\n", de.Name(), err)
			}
			continue
		}
		out = append(out, Entry{Manifest: m, Dir: pluginDir})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.Name < out[j].Manifest.Name
	})
	return out, nil
}

// SelectByKind returns the first entry whose manifest advertises kind,
// or (Entry{}, false) when nothing matches. Tie-break: alphabetical by
// name (Load already returns sorted entries).
func SelectByKind(entries []Entry, kind Kind) (Entry, bool) {
	for _, e := range entries {
		if e.Manifest.HasKind(kind) {
			return e, true
		}
	}
	return Entry{}, false
}
