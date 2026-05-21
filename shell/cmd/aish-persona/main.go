// Command aish-persona builds + signs a persona bundle from a
// directory of persona TOML files (or a single JSONL seed). Mirrors
// the shape of cmd/aish-community.
//
// Usage:
//
//	aish-persona build \
//	    -src data/personas/community \
//	    -out dist/persona-bundles/community \
//	    -id  community-pack \
//	    -version 1
//
// On success, writes:
//
//	<out>/manifest.toml
//	<out>/personas.jsonl
//	<out>/trust-anchors.toml  (informational copy)
//
// The bundle is signed with the dev Ed25519 keypair from
// shell/internal/persona/trust.go (PersonaDevPrivateKey). NOT for
// production use — production signing happens out-of-band.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "aish-persona: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: aish-persona build [flags]")
	}
	switch args[0] {
	case "build":
		return runBuild(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (try `build`)", args[0])
	}
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	srcDir := fs.String("src", "data/personas/community", "directory of *.toml persona files (one per file)")
	outDir := fs.String("out", "dist/persona-bundles/community", "output directory for manifest + personas.jsonl")
	bundleID := fs.String("id", "community-pack", "bundle_id (must match [a-z0-9][a-z0-9-]{0,63})")
	version := fs.Int("version", 1, "bundle_version (monotonic per signer)")
	trustAnchorsCopy := fs.String("trust-anchors", "",
		"path to trust-anchors.toml to copy into the bundle dir (informational; default skips copy)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version <= 0 {
		return fmt.Errorf("-version must be > 0")
	}

	personas, err := readPersonasFromDir(*srcDir)
	if err != nil {
		return err
	}
	if len(personas) == 0 {
		return fmt.Errorf("source dir %s contained no *.toml personas", *srcDir)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir out: %w", err)
	}
	payloadPath := filepath.Join(*outDir, persona.BundlePersonasFileName)
	_ = os.Remove(payloadPath)
	if err := persona.WritePersonasJSONL(personas, payloadPath); err != nil {
		return err
	}
	priv := persona.PersonaDevPrivateKey()
	sig, sha, err := persona.SignPersonasJSONL(priv, payloadPath)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	m := persona.BundleManifest{
		FormatVersion: 1,
		BundleVersion: *version,
		BundleID:      *bundleID,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		PersonaCount:  len(personas),
		SignerID:      "aish-persona-dev",
		Signature:     sig,
		SHA256:        sha,
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("manifest validate: %w", err)
	}
	body, err := persona.EncodeBundleManifest(m)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(*outDir, persona.BundleManifestFileName)
	if err := os.WriteFile(manifestPath, body, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if *trustAnchorsCopy != "" {
		dst := filepath.Join(*outDir, persona.BundleTrustAnchorsFileName)
		if err := copyFile(*trustAnchorsCopy, dst); err != nil {
			fmt.Fprintf(os.Stderr, "aish-persona: warning: copy trust-anchors.toml: %v\n", err)
		}
	}
	fmt.Printf("aish-persona: built %s v%d with %d personas (signer=aish-persona-dev, sha256=%s...)\n",
		*bundleID, *version, len(personas), sha[:12])
	fmt.Printf("aish-persona: artifacts in %s\n", *outDir)
	return nil
}

// readPersonasFromDir reads every *.toml file in dir, parses + validates
// it, and returns them sorted by name. Per-file failures are reported
// with the path so the user can fix the offender.
func readPersonasFromDir(dir string) ([]persona.Persona, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read src dir %s: %w", dir, err)
	}
	var out []persona.Persona
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		p, err := persona.ParseTOML(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("validate %s: %w", path, err)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
