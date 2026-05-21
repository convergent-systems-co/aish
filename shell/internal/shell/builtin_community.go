package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/cache"
	"github.com/convergent-systems-co/aish/shell/internal/cache/community"
)

// communityBuiltin implements `aish community ...` per v0.2-3 tasks
// #58–#63. Returns the exit code the dispatch loop should record.
//
// Subcommands:
//
//	community info        — bundle path, version, signer, intent
//	                        count + per-source counts in L1.
//	community status      — one-line status (`installed | absent`).
//	community install     — re-run the install path from a bundle
//	                        directory on disk; refuses downgrade
//	                        unless --force is passed.
//	community refresh     — alias for `install --force`.
//	community contribute  — opt-in: append the intent+invocation+os
//	                        triple to ~/.aish/community-contribute.jsonl
//	                        for offline review. NO NETWORK CALL.
//
// Bare `community` prints a usage hint and exits 2.
func (s *Shell) communityBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: community info | status | install [--force] [<dir>] | refresh | contribute <intent>")
		return 2
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]
	switch sub {
	case "info":
		return s.communityInfo(stdout, stderr)
	case "status":
		return s.communityStatus(stdout, stderr)
	case "install":
		return s.communityInstall(rest, stdout, stderr)
	case "refresh":
		// Equivalent to `install --force` from the bundle dir
		// already on disk — same discovery path.
		return s.communityInstall(append([]string{"--force"}, rest...), stdout, stderr)
	case "contribute":
		return s.communityContribute(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "community: unknown subcommand %q (try `community info`)\n", sub)
		return 2
	}
}

// communityInfo prints a multi-line report. The shell threads
// s.community via openCommunity(); if no bundle is loaded we still
// print a useful summary including any community-sourced rows in L1
// (which can exist after an aish reinstall that drops the bundle but
// leaves the cache.db intact).
func (s *Shell) communityInfo(stdout, stderr io.Writer) int {
	if s.community == nil || !s.community.IsLoaded() {
		fmt.Fprintln(stdout, "Community bundle: not loaded")
		s.printL1SourceBreakdown(stdout)
		return 0
	}
	m := s.community.Manifest()
	fmt.Fprintln(stdout, "Community bundle:")
	fmt.Fprintf(stdout, "  path:           %s\n", s.community.Path())
	fmt.Fprintf(stdout, "  bundle_version: %d\n", m.BundleVersion)
	fmt.Fprintf(stdout, "  signer_id:      %s\n", m.SignerID)
	fmt.Fprintf(stdout, "  created_at:     %s\n", m.CreatedAt)
	fmt.Fprintf(stdout, "  intent_count:   %d (manifest)\n", m.IntentCount)
	if live, err := s.community.IntentCount(); err == nil {
		fmt.Fprintf(stdout, "  intent_count:   %d (live)\n", live)
	}
	s.printL1SourceBreakdown(stdout)
	return 0
}

func (s *Shell) printL1SourceBreakdown(stdout io.Writer) {
	if s.cacheStore == nil {
		return
	}
	bySrc, err := s.cacheStore.EntriesBySource()
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, "L1 cache by source:")
	if len(bySrc) == 0 {
		fmt.Fprintln(stdout, "  (empty)")
		return
	}
	// Stable ordering: SourcePlugin, SourceCommunity, then anything
	// else alphabetically. Keep this deterministic so tests can
	// assert on full stdout.
	known := []string{cache.SourcePlugin, cache.SourceCommunity}
	seen := map[string]bool{}
	for _, k := range known {
		if n, ok := bySrc[k]; ok {
			fmt.Fprintf(stdout, "  %s: %d\n", k, n)
			seen[k] = true
		}
	}
	// Unknown sources at the end.
	for k, n := range bySrc {
		if !seen[k] {
			fmt.Fprintf(stdout, "  %s: %d\n", k, n)
		}
	}
}

func (s *Shell) communityStatus(stdout, _ io.Writer) int {
	if s.community != nil && s.community.IsLoaded() {
		m := s.community.Manifest()
		fmt.Fprintf(stdout, "community: installed (v%d, signer=%s)\n", m.BundleVersion, m.SignerID)
		return 0
	}
	fmt.Fprintln(stdout, "community: absent")
	return 0
}

// communityInstall reruns the verify+install pipeline against a
// bundle directory on disk. Flags:
//
//	--force         skip downgrade protection
//	<dir>           bundle directory (defaults to $AISH_COMMUNITY_BUNDLE_DIR
//	                or the well-known paths)
//
// Returns 0 on success; non-zero with stderr on any failure.
func (s *Shell) communityInstall(args []string, stdout, stderr io.Writer) int {
	force := false
	dir := ""
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		default:
			dir = a
		}
	}
	if dir == "" {
		envDir, _ := s.env.Get("AISH_COMMUNITY_BUNDLE_DIR")
		candidates := community.DefaultCandidates(currentBinaryPath())
		found, err := community.DiscoverBundleDir(envDir, candidates)
		if err != nil {
			fmt.Fprintf(stderr, "community: %v\n", err)
			return 1
		}
		dir = found
	}
	dotAish := s.dotAishDir()
	if dotAish == "" {
		fmt.Fprintln(stderr, "community: $HOME not set; cannot install")
		return 1
	}
	m, err := community.Install(dir, dotAish, community.InstallOpts{Force: force, Logger: stdout})
	if err != nil {
		fmt.Fprintf(stderr, "community: install: %v\n", err)
		return 1
	}
	// Re-open the bundle so subsequent Resolves consult the new
	// content immediately.
	b, openErr := community.OpenInstalled(dotAish, stderr)
	if openErr != nil {
		fmt.Fprintf(stderr, "community: open after install: %v\n", openErr)
		return 1
	}
	if s.community != nil {
		_ = s.community.Close()
	}
	s.community = b
	if s.cache != nil {
		s.cache.WithCommunityBundle(b)
	}
	fmt.Fprintf(stdout, "community: installed v%d (signer=%s)\n", m.BundleVersion, m.SignerID)
	return 0
}

// communityContribute appends one record to
// ~/.aish/community-contribute.jsonl. NO network call — the upload
// daemon is a follow-up. Records are intentionally minimal: intent,
// invocation, os. No cwd, no env, no args, no userid.
//
// The user must invoke this explicitly per-intent; there is no
// global "share everything" switch. That's the privacy contract.
func (s *Shell) communityContribute(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: community contribute <intent>")
		return 2
	}
	intent := strings.Join(args, " ")
	dotAish := s.dotAishDir()
	if dotAish == "" {
		fmt.Fprintln(stderr, "community: $HOME not set; cannot record contribution")
		return 1
	}
	// Look up the L1 row to find the invocation we'd contribute.
	// If the user contributes an intent we've never resolved, refuse
	// — there's nothing to share.
	if s.cacheStore == nil {
		fmt.Fprintln(stderr, "community: cache not available; nothing to contribute")
		return 1
	}
	invocation, hit, err := s.cacheStore.Lookup(intent, runtimeGOOS())
	if err != nil {
		fmt.Fprintf(stderr, "community: lookup: %v\n", err)
		return 1
	}
	if !hit {
		fmt.Fprintf(stderr,
			"community: %q has not been resolved on this machine; run it first\n", intent)
		return 1
	}
	rec := struct {
		Intent     string `json:"intent"`
		OS         string `json:"os"`
		Invocation string `json:"invocation"`
		RecordedAt string `json:"recorded_at"`
	}{
		Intent:     intent,
		OS:         runtimeGOOS(),
		Invocation: invocation,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		fmt.Fprintf(stderr, "community: marshal: %v\n", err)
		return 1
	}
	path := filepath.Join(dotAish, "community-contribute.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "community: open contribute log: %v\n", err)
		return 1
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		fmt.Fprintf(stderr, "community: write contribute log: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "community: recorded contribution for %q (local only; no network call)\n", intent)
	fmt.Fprintf(stdout, "community: review pending uploads at %s\n", path)
	return 0
}

// dotAishDir returns the $HOME/.aish directory or "" when HOME is
// unset. Shared by openCommunity, communityInstall, and
// communityContribute.
func (s *Shell) dotAishDir() string {
	home := homeDir(s.env)
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".aish")
}

// openCommunity is the boot-time hook. Called from Shell.New after
// openCache. Best-effort: any failure leaves s.community nil, the
// cache's L3 wire-up unset, and the user sees no community-cache
// behaviour. The shell still runs.
//
// Two passes:
//  1. Try ~/.aish/community-bundle.db (installed copy) first.
//  2. If absent, discover a fresh bundle on disk and install it.
//
// The two-pass shape matches the v0.1-2 cache: an already-installed
// store is the steady state, and discovery only fires on first run.
func (s *Shell) openCommunity(stderr io.Writer) {
	dotAish := s.dotAishDir()
	if dotAish == "" {
		return
	}
	if b, err := community.OpenInstalled(dotAish, stderr); err == nil {
		s.community = b
		if s.cache != nil {
			s.cache.WithCommunityBundle(b)
		}
		return
	} else if !errors.Is(err, community.ErrBundleNotFound) {
		// Open-installed errored for a non-"missing" reason. Surface
		// the failure on stderr but don't abort the shell.
		fmt.Fprintf(stderr, "aish: community: %v\n", err)
		return
	}
	// First-run install path: discover a fresh bundle on disk and
	// run Install. Silent on ErrBundleNotFound — the L3 tier is
	// optional.
	envDir, _ := s.env.Get("AISH_COMMUNITY_BUNDLE_DIR")
	cands := community.DefaultCandidates(currentBinaryPath())
	src, err := community.DiscoverBundleDir(envDir, cands)
	if err != nil {
		return
	}
	if _, err := community.Install(src, dotAish, community.InstallOpts{Logger: stderr}); err != nil {
		fmt.Fprintf(stderr, "aish: community: install: %v\n", err)
		return
	}
	if b, err := community.OpenInstalled(dotAish, stderr); err == nil {
		s.community = b
		if s.cache != nil {
			s.cache.WithCommunityBundle(b)
		}
	}
}

// currentBinaryPath returns the running aish binary path so the
// DefaultCandidates walk can include `<binary>/../share/aish/community`.
// Returns "" on failure; DefaultCandidates handles that gracefully.
func currentBinaryPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}

// runtimeGOOS returns runtime.GOOS as a string. Pulled out into a
// helper so tests can stub it via a package-var indirection; today
// it just defers to the stdlib. The community-cache contribution
// flow uses this to record the OS the intent was resolved on.
var runtimeGOOS = func() string { return runtime.GOOS }
