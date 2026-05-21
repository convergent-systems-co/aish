package shell

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/cache/community"
)

// stageBundle writes a freshly-signed dev bundle (manifest.json +
// bundle.db) into a tempdir using the community package's
// SignBundleDB. Returns the bundle directory path.
//
// The bundle carries two rows: one darwin, one linux. Tests pick
// the row matching runtime.GOOS so the install-then-resolve path
// produces a hit on the test host.
func stageBundle(t *testing.T, version int) string {
	t.Helper()
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, community.BundleDBFileName)

	// Use the community subpackage's exported sign helper.
	mkBundleDBWithRows(t, bundlePath, []bundleRow{
		{intent: "list files", os: "darwin", invocation: "ls -la"},
		{intent: "list files", os: "linux", invocation: "ls -la --color=auto"},
		{intent: "list files", os: "windows", invocation: "Get-ChildItem"},
	})

	sig, sha, err := community.SignBundleDB(community.DevPrivateKey(), bundlePath)
	if err != nil {
		t.Fatalf("SignBundleDB: %v", err)
	}
	m := community.Manifest{
		FormatVersion: 1,
		BundleVersion: version,
		CreatedAt:     "2026-05-20T00:00:00Z",
		IntentCount:   3,
		SignerID:      "aish-dev",
		Signature:     sig,
		SHA256:        sha,
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, community.ManifestFileName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

type bundleRow struct {
	intent     string
	os         string
	invocation string
}

// mkBundleDBWithRows is a tiny duplicate of community/verify_test's
// makeBundleDB. Pulled here because that helper is unexported. The
// SQL it issues matches community.BundleSchema verbatim — if either
// drifts, TestCommunityInstallE2E will catch it.
func mkBundleDBWithRows(t *testing.T, path string, rows []bundleRow) {
	t.Helper()
	import_modernc_sqlite() // ensure driver registered
	db, err := sqlOpen(path)
	if err != nil {
		t.Fatalf("open bundle.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(community.BundleSchema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	stmt, err := db.Prepare(
		`INSERT INTO intents (intent_hash, os, intent, invocation, confidence) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(communityHashIntent(r.intent), r.os, r.intent, r.invocation, 1.0); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
}

func TestCommunityBuiltin_Usage(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	var out, errb bytes.Buffer
	code := s.communityBuiltin(nil, &out, &errb)
	if code != 2 {
		t.Errorf("exit code = %d; want 2", code)
	}
	if !strings.Contains(errb.String(), "Usage") {
		t.Errorf("stderr = %q; want Usage line", errb.String())
	}
}

func TestCommunityBuiltin_InfoNoBundle(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	var out, errb bytes.Buffer
	code := s.communityBuiltin([]string{"info"}, &out, &errb)
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out.String(), "not loaded") {
		t.Errorf("stdout = %q; want \"not loaded\"", out.String())
	}
}

func TestCommunityBuiltin_StatusAbsent(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	var out, errb bytes.Buffer
	code := s.communityBuiltin([]string{"status"}, &out, &errb)
	if code != 0 {
		t.Errorf("exit code = %d", code)
	}
	if !strings.Contains(out.String(), "absent") {
		t.Errorf("stdout = %q; want \"absent\"", out.String())
	}
}

func TestCommunityBuiltin_InstallAndInfo(t *testing.T) {
	home := isolatedHome(t)
	s := New()
	defer s.Close()
	src := stageBundle(t, 1)
	var out, errb bytes.Buffer
	code := s.communityBuiltin([]string{"install", src}, &out, &errb)
	if code != 0 {
		t.Fatalf("install exit code = %d; stderr=%q", code, errb.String())
	}
	// Installed file should exist under ~/.aish/.
	if _, err := os.Stat(filepath.Join(home, ".aish", community.InstalledBundleFileName)); err != nil {
		t.Errorf("installed bundle missing: %v", err)
	}
	// Info now reports a loaded bundle.
	out.Reset()
	errb.Reset()
	if code := s.communityBuiltin([]string{"info"}, &out, &errb); code != 0 {
		t.Errorf("info exit code = %d", code)
	}
	if !strings.Contains(out.String(), "signer_id:      aish-dev") {
		t.Errorf("info stdout = %q; want signer line", out.String())
	}
	if !strings.Contains(out.String(), "bundle_version: 1") {
		t.Errorf("info stdout = %q; want version line", out.String())
	}
}

func TestCommunityBuiltin_InstallDowngradeRefused(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	srcV2 := stageBundle(t, 2)
	srcV1 := stageBundle(t, 1)

	if code := s.communityBuiltin([]string{"install", srcV2}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("install v2 exit = %d", code)
	}
	// v1 install must fail without --force.
	var errb bytes.Buffer
	if code := s.communityBuiltin([]string{"install", srcV1}, new(bytes.Buffer), &errb); code == 0 {
		t.Errorf("install v1 exit = 0; want nonzero (downgrade)")
	}
	if !strings.Contains(errb.String(), "downgrade") {
		t.Errorf("stderr = %q; want downgrade message", errb.String())
	}
	// `community refresh` is `install --force` and must succeed.
	if code := s.communityBuiltin([]string{"refresh", srcV1}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Errorf("refresh v1 exit = %d; want 0", code)
	}
}

func TestCommunityBuiltin_ContributeRefusesUnresolvedIntent(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	var out, errb bytes.Buffer
	code := s.communityBuiltin([]string{"contribute", "never resolved before"}, &out, &errb)
	if code == 0 {
		t.Errorf("exit code = 0; want nonzero (no L1 row)")
	}
	if !strings.Contains(errb.String(), "has not been resolved") {
		t.Errorf("stderr = %q; want \"has not been resolved\"", errb.String())
	}
}

func TestCommunityBuiltin_ContributeWritesJSONL(t *testing.T) {
	home := isolatedHome(t)
	s := New()
	defer s.Close()
	// Pre-populate L1.
	if err := s.cacheStore.Write("say hi", runtime.GOOS, "echo hi", 1.0, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var out, errb bytes.Buffer
	code := s.communityBuiltin([]string{"contribute", "say", "hi"}, &out, &errb)
	if code != 0 {
		t.Errorf("exit code = %d (stderr=%q)", code, errb.String())
	}
	path := filepath.Join(home, ".aish", "community-contribute.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if !strings.Contains(string(raw), "say hi") {
		t.Errorf("jsonl content = %q; want \"say hi\"", string(raw))
	}
	if !strings.Contains(string(raw), "echo hi") {
		t.Errorf("jsonl content = %q; want \"echo hi\"", string(raw))
	}
	// Privacy contract: no cwd, no userid, no env.
	if strings.Contains(string(raw), home) {
		t.Errorf("jsonl content leaks HOME path: %q", string(raw))
	}
}

func TestCommunityBuiltin_UnknownSubcommand(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	var errb bytes.Buffer
	code := s.communityBuiltin([]string{"frobnicate"}, new(bytes.Buffer), &errb)
	if code != 2 {
		t.Errorf("exit code = %d; want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Errorf("stderr = %q; want \"unknown subcommand\"", errb.String())
	}
}

func TestResolveTier_CommunityIsBuiltin(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	if got := s.ResolveTier("community"); got != tierBuiltinExpected() {
		t.Errorf("ResolveTier(community) = %v; want builtin", got)
	}
}
