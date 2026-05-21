// Package plugins is the shell-side runtime loader for the v0.3-2
// plugin registry.
//
// The shell calls Select(kind, dotAish, warn) at boot to find an
// installed plugin binary for the requested kind. When the registry
// is empty or absent — the default state on a fresh aish install —
// Select returns ok=false and the caller falls back to the
// pre-v0.3-2 PATH lookup (DefaultPluginBinary). This keeps existing
// users working without intervention while opt-in registry usage
// rides on top.
//
// Discovery is filesystem-only per the plan's Alternatives Table; a
// centralised plugin server is deferred to v0.4 with the marketplace.
package plugins

import (
	"io"
	"path/filepath"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
)

// Selection is the result of a successful Select call.
type Selection struct {
	// BinaryPath is the absolute path to spawn. Comes from the
	// matched manifest's BinaryPath.
	BinaryPath string
	// Name is the manifest's plugin Name. Surfaced in diagnostics
	// (e.g. "aish: starting inference plugin <name>").
	Name string
	// Version is the manifest's reported version, informational.
	Version string
	// SignerID is the manifest's SignerID, informational.
	SignerID string
}

// Select walks the registry under dotAish/<proto.DirName>/ and returns
// the first plugin advertising the requested kind. Warnings about
// malformed or unverifiable manifests are written to warn (when
// non-nil). Returns ok=false on empty registry, missing directory,
// or no kind match.
//
// dotAish is the user's .aish directory; the registry lives at
// dotAish/plugins/ (i.e. ~/.aish/plugins/ in production).
func Select(kind proto.Kind, dotAish string, warn io.Writer) (Selection, bool) {
	if dotAish == "" {
		return Selection{}, false
	}
	root := filepath.Join(dotAish, proto.DirName)
	entries, err := proto.Load(root, warn)
	if err != nil {
		if warn != nil {
			_, _ = io.WriteString(warn, "aish: plugins: load registry: "+err.Error()+"\n")
		}
		return Selection{}, false
	}
	if len(entries) == 0 {
		return Selection{}, false
	}
	hit, ok := proto.SelectByKind(entries, kind)
	if !ok {
		return Selection{}, false
	}
	return Selection{
		BinaryPath: hit.Manifest.BinaryPath,
		Name:       hit.Manifest.Name,
		Version:    hit.Manifest.Version,
		SignerID:   hit.Manifest.SignerID,
	}, true
}

// All returns every entry in the registry under dotAish. Used by the
// `plugin list` shell built-in. Returns nil + warning to warn on
// directory-read failure (same best-effort posture as Select).
func All(dotAish string, warn io.Writer) []proto.Entry {
	if dotAish == "" {
		return nil
	}
	root := filepath.Join(dotAish, proto.DirName)
	entries, err := proto.Load(root, warn)
	if err != nil && warn != nil {
		_, _ = io.WriteString(warn, "aish: plugins: load registry: "+err.Error()+"\n")
	}
	return entries
}

// Root returns the canonical registry root for a given dotAish dir.
// Exposed so the shell's `plugin install <path>` / `plugin remove
// <name>` plumbing names the same location the loader walked.
func Root(dotAish string) string {
	if dotAish == "" {
		return ""
	}
	return filepath.Join(dotAish, proto.DirName)
}
