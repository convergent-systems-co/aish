package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// T3 — Kube context adapter tests. Implementation uses direct YAML
// edit via gopkg.in/yaml.v3 (the plan §T3 fallback path — chosen at
// the outset to avoid client-go's binary-size swell on darwin).

func personaWithKube(ctx string) persona.Persona {
	p := persona.Persona{
		Name:         "kube-test",
		Version:      persona.SchemaVersion,
		SystemPrompt: "test",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
	p.ExternalBindings.Kube = &persona.KubeBinding{Context: ctx}
	return p
}

// stageKubeconfigFixture copies the testdata fixture into a tempdir
// and returns the destination path.
func stageKubeconfigFixture(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "kube", "kubeconfig.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}
	return dst
}

func readCurrentContext(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	doc, err := parseKubeconfig(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc.CurrentContext
}

// TestKubeAdapter_ContextSwap — fixture kubeconfig with two contexts;
// Apply; Rollback; both verified via re-reading current-context.
func TestKubeAdapter_ContextSwap(t *testing.T) {
	t.Parallel()
	path := stageKubeconfigFixture(t)
	if got := readCurrentContext(t, path); got != "personal-cluster" {
		t.Fatalf("pre-state current-context = %q; want personal-cluster", got)
	}
	ad := NewKubeAdapterWithDeps(fixedHome{Path: t.TempDir()}, path)
	ctx := context.Background()

	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithKube("work-cluster")
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readCurrentContext(t, path); got != "work-cluster" {
		t.Fatalf("post-apply current-context = %q; want work-cluster", got)
	}
	if err := ad.Verify(ctx, p); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := ad.Rollback(ctx, snap); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := readCurrentContext(t, path); got != "personal-cluster" {
		t.Fatalf("post-rollback current-context = %q; want personal-cluster", got)
	}
}

// TestKubeAdapter_UnknownContextErrors — persona binds a context that
// doesn't exist; Apply returns ErrSchema-wrapped error.
func TestKubeAdapter_UnknownContextErrors(t *testing.T) {
	t.Parallel()
	path := stageKubeconfigFixture(t)
	ad := NewKubeAdapterWithDeps(fixedHome{Path: t.TempDir()}, path)
	ctx := context.Background()
	if _, err := ad.Capture(ctx); err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithKube("does-not-exist")
	err := ad.Apply(ctx, p)
	if err == nil {
		t.Fatal("expected ErrSchema for unknown context")
	}
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("expected wrapped ErrSchema; got %v", err)
	}
	// Apply must not have mutated the file when the context is
	// unknown.
	if got := readCurrentContext(t, path); got != "personal-cluster" {
		t.Fatalf("file was mutated despite schema error: current-context = %q", got)
	}
}

// TestKubeAdapter_ConcurrentMutationWarns — between Capture and
// Rollback, mutate the kubeconfig externally; assert Rollback
// surfaces a KubeDigestWarning AND still restores the prior context.
func TestKubeAdapter_ConcurrentMutationWarns(t *testing.T) {
	t.Parallel()
	path := stageKubeconfigFixture(t)
	ad := NewKubeAdapterWithDeps(fixedHome{Path: t.TempDir()}, path)
	ctx := context.Background()

	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithKube("work-cluster")
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// External mutator: append a meaningless comment so the digest
	// changes between Apply and Rollback. (The current-context line
	// is unaffected.)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read for mutation: %v", err)
	}
	mutated := append(raw, []byte("\n# external mutation\n")...)
	if err := os.WriteFile(path, mutated, 0o644); err != nil {
		t.Fatalf("write mutated: %v", err)
	}

	err = ad.Rollback(ctx, snap)
	if err == nil {
		t.Fatal("expected KubeDigestWarning on Rollback after external mutation")
	}
	if !IsKubeDigestWarning(err) {
		t.Fatalf("expected KubeDigestWarning; got %T: %v", err, err)
	}
	// Even with the digest warning, the prior context must be
	// restored.
	if got := readCurrentContext(t, path); got != "personal-cluster" {
		t.Fatalf("post-rollback current-context = %q; want personal-cluster (warning notwithstanding)", got)
	}
	// And the warning must mention this is non-fatal in its message.
	if !strings.Contains(err.Error(), "prior context restored") {
		t.Fatalf("warning message lost the non-fatal hint: %v", err)
	}
}

// TestKubeAdapter_NoKubeconfigReturnsErrNoSubsystem — when no
// kubeconfig exists at the resolved path, Capture returns a wrapped
// ErrNoSubsystem.
func TestKubeAdapter_NoKubeconfigReturnsErrNoSubsystem(t *testing.T) {
	// Cannot t.Parallel() — t.Setenv would panic.
	home := t.TempDir() // empty — no ~/.kube/config inside
	ad := NewKubeAdapterWithDeps(fixedHome{Path: home}, "")
	// KUBECONFIG must not be set on the test process for this path.
	t.Setenv("KUBECONFIG", "")
	_, err := ad.Capture(context.Background())
	if err == nil {
		t.Fatal("expected ErrNoSubsystem when no kubeconfig present")
	}
	if !errors.Is(err, ErrNoSubsystem) {
		t.Fatalf("expected wrapped ErrNoSubsystem; got %v", err)
	}
}
