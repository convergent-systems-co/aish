// Command aish-community builds + signs a community-cache bundle
// from a JSONL seed file.
//
// Usage:
//
//	aish-community build \
//	    -seed data/community/seed.jsonl \
//	    -out  dist/community/ \
//	    -version 1
//
// On success, writes:
//
//	<out>/manifest.json
//	<out>/bundle.db
//	<out>/trust-anchors.toml   (copy of data/community/trust-anchors.toml)
//
// The bundle is signed with the dev Ed25519 keypair from
// shell/internal/cache/community/trust.go (DevPrivateKey). NOT for
// production use — production signing happens out-of-band.
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/cache/community"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "aish-community: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: aish-community build [flags]")
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
	seedPath := fs.String("seed", "data/community/seed.jsonl", "JSONL seed file (one row per line)")
	outDir := fs.String("out", "dist/community", "output directory for manifest.json + bundle.db")
	version := fs.Int("version", 1, "bundle_version (monotonic per signer)")
	trustAnchorsCopy := fs.String("trust-anchors", "data/community/trust-anchors.toml",
		"path to trust-anchors.toml to copy into the bundle dir (informational)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version <= 0 {
		return fmt.Errorf("-version must be > 0")
	}

	rows, err := readSeed(*seedPath)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("seed file %s contained no rows", *seedPath)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir out: %w", err)
	}
	bundlePath := filepath.Join(*outDir, community.BundleDBFileName)
	// Remove any pre-existing bundle.db so the build is reproducible.
	_ = os.Remove(bundlePath)
	if err := writeBundleDB(bundlePath, rows); err != nil {
		return err
	}

	priv := community.DevPrivateKey()
	sig, sha, err := community.SignBundleDB(priv, bundlePath)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	m := community.Manifest{
		FormatVersion: 1,
		BundleVersion: *version,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		IntentCount:   len(rows),
		SignerID:      "aish-dev",
		Signature:     sig,
		SHA256:        sha,
	}
	manifestPath := filepath.Join(*outDir, community.ManifestFileName)
	if err := writeJSON(manifestPath, m); err != nil {
		return err
	}
	if *trustAnchorsCopy != "" {
		dst := filepath.Join(*outDir, community.TrustAnchorsFileName)
		if err := copyFile(*trustAnchorsCopy, dst); err != nil {
			fmt.Fprintf(os.Stderr, "aish-community: warning: copy trust-anchors.toml: %v\n", err)
		}
	}
	fmt.Printf("aish-community: built v%d with %d intents (signer=aish-dev, sha256=%s...)\n",
		*version, len(rows), sha[:12])
	fmt.Printf("aish-community: artifacts in %s\n", *outDir)
	return nil
}

// seedRow is one line of the JSONL seed. Curators control the
// format; the build tool is the single authority on what gets into
// bundle.db.
type seedRow struct {
	Intent     string `json:"intent"`
	OS         string `json:"os"`
	Invocation string `json:"invocation"`
}

func readSeed(path string) ([]seedRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open seed %s: %w", path, err)
	}
	defer f.Close()
	out := []seedRow{}
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		var r seedRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("seed %s:%d: %w", path, lineNo, err)
		}
		if r.Intent == "" || r.OS == "" || r.Invocation == "" {
			return nil, fmt.Errorf("seed %s:%d: missing required field (intent|os|invocation)",
				path, lineNo)
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan seed: %w", err)
	}
	return out, nil
}

func writeBundleDB(path string, rows []seedRow) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open bundle.db: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(community.BundleSchema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO intents (intent_hash, os, intent, invocation, confidence)
		 VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		hash := community.HashIntentForBuild(r.Intent)
		if _, err := stmt.Exec(hash, r.OS, r.Intent, r.Invocation, 1.0); err != nil {
			return fmt.Errorf("insert (%s/%s): %w", r.Intent, r.OS, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func writeJSON(path string, v interface{}) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
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
