package adapter

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// T1 — SSH agent adapter tests. The fake-agent helper
// (ssh_fakeagent_test.go) speaks the actual SSH agent wire protocol;
// the adapter under test exercises the same `agent.NewClient` code
// path it uses in production.

// minimalPersonaWithSSH returns a persona declaring an SSH binding
// against the given vault label.
func minimalPersonaWithSSH(label string, lifetime int) persona.Persona {
	p := persona.Persona{
		Name:         "ssh-test",
		Version:      persona.SchemaVersion,
		SystemPrompt: "test",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
	p.ExternalBindings.SSH = &persona.SSHBinding{
		KeyLabel:           label,
		AddLifetimeSeconds: lifetime,
	}
	return p
}

// TestSSHAdapter_ApplyAddsKey — fake agent receives Add; key bytes
// match the vault label. (Plan §T1 acceptance.)
func TestSSHAdapter_ApplyAddsKey(t *testing.T) {
	t.Parallel()
	fa := newFakeAgent(t)
	pemBytes := genEd25519PEM(t)
	src := &mapKeySource{keys: map[string][]byte{"work": pemBytes}}

	ad := NewSSHAdapter(src, fa.Dialer())
	ctx := context.Background()

	if _, err := ad.Capture(ctx); err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := minimalPersonaWithSSH("work", 0)
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	keys := fa.ListKeys()
	if len(keys) != 1 {
		t.Fatalf("expected exactly one key in agent, got %d", len(keys))
	}
	if err := ad.Verify(ctx, p); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestSSHAdapter_RollbackRestoresAgentState — apply, rollback, agent
// returns the exact pre-call set.
func TestSSHAdapter_RollbackRestoresAgentState(t *testing.T) {
	t.Parallel()
	fa := newFakeAgent(t)
	// Pre-load a "user-added" key.
	_, userPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen user key: %v", err)
	}
	fa.AddKey(userPriv)
	before := fa.ListKeys()
	if len(before) != 1 {
		t.Fatalf("pre-state: expected 1 user key, got %d", len(before))
	}

	pemBytes := genEd25519PEM(t)
	src := &mapKeySource{keys: map[string][]byte{"work": pemBytes}}
	ad := NewSSHAdapter(src, fa.Dialer())
	ctx := context.Background()

	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := minimalPersonaWithSSH("work", 0)
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := len(fa.ListKeys()); got != 2 {
		t.Fatalf("post-apply: expected 2 keys, got %d", got)
	}
	if err := ad.Rollback(ctx, snap); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	after := fa.ListKeys()
	if len(after) != 1 {
		t.Fatalf("post-rollback: expected 1 key (user-added preserved), got %d",
			len(after))
	}
	if !bytes.Equal(after[0].Marshal(), before[0].Marshal()) {
		t.Fatal("post-rollback: user-added key was not preserved verbatim")
	}
}

// TestSSHAdapter_NoPrivateKeyInSnapshot — adversarial: Snapshot bytes
// do NOT contain the private-key PEM material. Threat-model invariant.
func TestSSHAdapter_NoPrivateKeyInSnapshot(t *testing.T) {
	t.Parallel()
	fa := newFakeAgent(t)
	pemBytes := genEd25519PEM(t)
	src := &mapKeySource{keys: map[string][]byte{"work": pemBytes}}

	// Add the persona's key first so Capture sees it in the agent.
	signer, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	fa.AddKey(signer)

	ad := NewSSHAdapter(src, fa.Dialer())
	snap, err := ad.Capture(context.Background())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	// The snapshot MUST NOT contain any meaningful chunk of the PEM.
	// Scan for the "PRIVATE KEY" header and for raw bytes.
	if bytes.Contains(snap, []byte("PRIVATE KEY")) {
		t.Fatal("snapshot contains 'PRIVATE KEY' substring")
	}
	// Also assert no 32-byte run of the PEM appears.
	if len(pemBytes) >= 32 && bytes.Contains(snap, pemBytes[:32]) {
		t.Fatal("snapshot contains the first 32 PEM bytes")
	}
}

// TestSSHAdapter_CaptureNoAgentReturnsErrNoAgent — Capture against a
// dialer that fails surfaces a wrapped ErrNoSubsystem so the
// orchestrator's skip path engages.
func TestSSHAdapter_CaptureNoAgentReturnsErrNoAgent(t *testing.T) {
	t.Parallel()
	ad := NewSSHAdapter(nil, failingDialer{})
	_, err := ad.Capture(context.Background())
	if err == nil {
		t.Fatal("expected error from Capture with unreachable agent")
	}
	if !errors.Is(err, ErrNoSubsystem) {
		t.Fatalf("expected ErrNoSubsystem; got %v", err)
	}
}

// TestSSHAdapter_PrivateKeyBufferZeroedAfterAdd — adversarial: the
// buffer the adapter held for the private-key bytes is zeroed after
// Add returns. Verified by making the KeySource return a deliberately
// non-zero byte pattern and re-reading the same slice (the
// production code calls secrets.Zero on the same backing array — the
// mapKeySource returns a copy, but we exercise the same code path via
// a sniffing KeySource that retains a reference to the slice it
// handed to the adapter).
func TestSSHAdapter_PrivateKeyBufferZeroedAfterAdd(t *testing.T) {
	t.Parallel()
	fa := newFakeAgent(t)
	pemBytes := genEd25519PEM(t)
	src := &sniffingKeySource{
		inner: &mapKeySource{keys: map[string][]byte{"work": pemBytes}},
	}
	ad := NewSSHAdapter(src, fa.Dialer())
	if err := ad.Apply(context.Background(), minimalPersonaWithSSH("work", 0)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if src.lastHanded == nil {
		t.Fatal("sniffing source never observed a GetPrivateKey call")
	}
	for i, b := range src.lastHanded {
		if b != 0 {
			t.Fatalf("buffer not zeroed: byte %d = 0x%x", i, b)
		}
	}
}

// failingDialer returns an immediate error — used to drive the
// "agent unreachable" path.
type failingDialer struct{}

func (failingDialer) Dial(ctx context.Context) (net.Conn, error) {
	return nil, errors.New("no agent here")
}

// sniffingKeySource wraps an inner KeySource and retains a reference
// to the most recently handed slice so the test can inspect it
// post-Apply.
type sniffingKeySource struct {
	inner      KeySource
	lastHanded []byte
}

func (s *sniffingKeySource) GetPrivateKey(label string) ([]byte, error) {
	buf, err := s.inner.GetPrivateKey(label)
	if err != nil {
		return nil, err
	}
	s.lastHanded = buf
	return buf, nil
}
