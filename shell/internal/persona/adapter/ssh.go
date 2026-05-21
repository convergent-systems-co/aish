// SSH agent adapter — T1 of the v0.3-3 atomic-persona-switch plan.
//
// Strategy (Thomas-approved): golang.org/x/crypto/ssh/agent native.
// The adapter talks the SSH agent wire protocol over the unix socket
// at $SSH_AUTH_SOCK; no shell-out to `ssh-add` is involved.
//
// Snapshot contents — public-key bytes only. Per the package's
// threat model (doc.go) and plan §T1, the private key is NEVER stored
// in a Snapshot. The pre-call agent state is recorded as the set of
// SHA-256 fingerprints of currently-loaded keys plus their wire
// marshaling (so the adapter can re-Add user-added keys if needed
// — out of scope for v0.3-3 because rollback's job is "undo what we
// did," not "preserve unrelated additions"). The private-key buffer
// the adapter holds during Apply is zeroed via secrets.Zero
// immediately after the agent.Client.Add call returns.
//
// Capture-then-restore semantics for rollback:
//
//   - Capture records the fingerprint set of the currently-loaded
//     keys.
//   - Apply adds the persona's key.
//   - Rollback enumerates the agent's current keys; any key whose
//     fingerprint is NOT in the captured set is removed. Keys the
//     user added between Apply and Rollback are preserved (they were
//     in the captured set? no — they were not. But they are also not
//     the key we Added, so we leave them alone. The rule is "remove
//     only keys we added"; we identify those by tracking the
//     fingerprint we Added in Apply, not by set subtraction.)
//
// Actually — refining: the rule is "remove the key we Added in this
// transaction." Rollback removes exactly the fingerprint Apply
// installed, leaving everything else (captured-set members AND
// user-additions made during the transaction window) alone. This is
// stronger than "set subtraction" and avoids any race where a
// user-added key appears between Capture and Rollback.

package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// KeySource is the read-side contract the SSH adapter uses to fetch a
// PEM-encoded private key by its vault label. Production wires this
// to *secrets.Vault; integration tests supply an in-memory stub so
// the adapter can be exercised without standing up a full vault.
//
// The returned slice is treated as secret: the adapter zeroes it via
// secrets.Zero immediately after the agent.Add call returns.
type KeySource interface {
	GetPrivateKey(label string) ([]byte, error)
}

// AgentDialer abstracts the unix-socket dial against $SSH_AUTH_SOCK so
// tests can inject a fake-agent socket path. Production uses
// defaultDialer which net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")).
type AgentDialer interface {
	Dial(ctx context.Context) (net.Conn, error)
}

type envSockDialer struct{}

func (envSockDialer) Dial(ctx context.Context) (net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("%w: $SSH_AUTH_SOCK unset", ErrNoAgent)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %v", ErrNoAgent, sock, err)
	}
	return conn, nil
}

// SSHAdapter implements PersonaAdapter for the SSH agent.
type SSHAdapter struct {
	keys   KeySource
	dialer AgentDialer

	// addedFingerprint is the SHA-256 fingerprint of the key Apply
	// installed in the agent. Rollback uses this to identify exactly
	// the key to remove (see package doc). Empty if Apply did not run
	// or failed before reaching the agent.Add call.
	addedFingerprint string
}

// NewSSHAdapter constructs an SSH adapter wired to the given
// KeySource. Pass dialer = nil to use the default ($SSH_AUTH_SOCK).
func NewSSHAdapter(keys KeySource, dialer AgentDialer) *SSHAdapter {
	if dialer == nil {
		dialer = envSockDialer{}
	}
	return &SSHAdapter{keys: keys, dialer: dialer}
}

// Name implements PersonaAdapter.
func (a *SSHAdapter) Name() string { return "ssh" }

// sshSnapshot is the JSON-like body persisted as Snapshot bytes.
// Public-key fingerprints only. The bytes are designed to fail the
// adversarial !bytes.Contains(snap, privateKey) check by never
// holding private-key material in the first place.
type sshSnapshot struct {
	// Fingerprints is the set of SHA-256 fingerprints (hex-encoded)
	// of keys the agent had at Capture time. Used by Rollback to
	// distinguish "key we Added" from "everything else."
	Fingerprints []string
}

func encodeSnapshot(s sshSnapshot) Snapshot {
	// Format: "fp1\nfp2\nfp3\n" — newline-delimited hex digests.
	// Trivially-parseable, no JSON dep needed, and easy to inspect in
	// the adversarial test (which scans for raw private-key bytes).
	var b bytes.Buffer
	for _, fp := range s.Fingerprints {
		b.WriteString(fp)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// (decodeSnapshot intentionally omitted — Rollback targets the
// specific fingerprint recorded in Apply.addedFingerprint, not the
// captured set. The Snapshot's fingerprint list is forensic-only and
// not deserialized within the adapter; the adversarial
// !bytes.Contains(snap, privateKey) test exercises the bytes
// directly without decoding.)

// fingerprintOf returns the SHA-256 hex of the SSH wire-format public
// key bytes.
func fingerprintOf(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return hex.EncodeToString(sum[:])
}

// Capture lists currently-loaded agent keys and records their public
// fingerprints. ErrNoAgent wraps any dial failure ($SSH_AUTH_SOCK
// unset or socket unreachable).
func (a *SSHAdapter) Capture(ctx context.Context) (Snapshot, error) {
	conn, err := a.dialer.Dial(ctx)
	if err != nil {
		// Wrap up to ErrNoSubsystem so the orchestrator treats a
		// missing agent as "skip me, not fail."
		return nil, fmt.Errorf("%w: %v", ErrNoSubsystem, err)
	}
	defer conn.Close()
	client := agent.NewClient(conn)
	keys, err := client.List()
	if err != nil {
		return nil, fmt.Errorf("ssh agent list: %w", err)
	}
	snap := sshSnapshot{Fingerprints: make([]string, 0, len(keys))}
	for _, k := range keys {
		// k.Marshal() returns the wire bytes of the public key.
		sum := sha256.Sum256(k.Marshal())
		snap.Fingerprints = append(snap.Fingerprints, hex.EncodeToString(sum[:]))
	}
	return encodeSnapshot(snap), nil
}

// Apply loads the persona's SSH key into the agent.
func (a *SSHAdapter) Apply(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.SSH == nil {
		return fmt.Errorf("%w: persona %q has no [external.ssh]", ErrNoBinding, p.Name)
	}
	binding := p.ExternalBindings.SSH
	if binding.KeyLabel == "" {
		return fmt.Errorf("%w: [external.ssh].key_label is empty", ErrSchema)
	}
	if a.keys == nil {
		return fmt.Errorf("%w: no KeySource configured", ErrSchema)
	}

	pemBytes, err := a.keys.GetPrivateKey(binding.KeyLabel)
	if err != nil {
		return fmt.Errorf("fetch key %q: %w", binding.KeyLabel, err)
	}
	// pemBytes is secret. Guarantee it's zeroed regardless of code
	// path below — defer fires before return whether or not Add
	// succeeds.
	defer secrets.Zero(pemBytes)

	signer, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		return fmt.Errorf("parse private key %q: %w", binding.KeyLabel, err)
	}

	conn, err := a.dialer.Dial(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoAgent, err)
	}
	defer conn.Close()
	client := agent.NewClient(conn)

	addedKey := agent.AddedKey{
		PrivateKey: signer,
	}
	if binding.AddLifetimeSeconds > 0 {
		addedKey.LifetimeSecs = uint32(binding.AddLifetimeSeconds)
	}
	if err := client.Add(addedKey); err != nil {
		return fmt.Errorf("agent add %q: %w", binding.KeyLabel, err)
	}

	// Record the fingerprint we installed so Rollback can target
	// exactly this key. Read the public key off the signer to compute
	// the fingerprint; agent.AddedKey doesn't expose it directly.
	pub, err := signerPublicKey(signer)
	if err != nil {
		return fmt.Errorf("derive public key for fingerprint: %w", err)
	}
	a.addedFingerprint = fingerprintOf(pub)
	return nil
}

// Verify reads the agent's loaded keys and confirms the persona's key
// fingerprint is among them.
func (a *SSHAdapter) Verify(ctx context.Context, p persona.Persona) error {
	if a.addedFingerprint == "" {
		return errors.New("verify: Apply did not record a fingerprint")
	}
	conn, err := a.dialer.Dial(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoAgent, err)
	}
	defer conn.Close()
	keys, err := agent.NewClient(conn).List()
	if err != nil {
		return fmt.Errorf("agent list (verify): %w", err)
	}
	for _, k := range keys {
		if fingerprintOf(k) == a.addedFingerprint {
			return nil
		}
	}
	return fmt.Errorf("verify: fingerprint %s not present in agent",
		a.addedFingerprint)
}

// Rollback removes the key Apply installed. If addedFingerprint is
// empty (Apply never reached the Add call), Rollback is a no-op —
// per orchestrator contract Rollback is invoked only for adapters
// whose Apply completed.
func (a *SSHAdapter) Rollback(ctx context.Context, snap Snapshot) error {
	_ = snap // captured set is informational; we target our installed fp
	if a.addedFingerprint == "" {
		return nil
	}
	conn, err := a.dialer.Dial(ctx)
	if err != nil {
		return fmt.Errorf("rollback dial: %w", err)
	}
	defer conn.Close()
	client := agent.NewClient(conn)
	keys, err := client.List()
	if err != nil {
		return fmt.Errorf("rollback list: %w", err)
	}
	for _, k := range keys {
		if fingerprintOf(k) == a.addedFingerprint {
			// Construct a ssh.PublicKey from the agent.Key.
			pk, perr := ssh.ParsePublicKey(k.Marshal())
			if perr != nil {
				return fmt.Errorf("rollback parse: %w", perr)
			}
			if err := client.Remove(pk); err != nil {
				return fmt.Errorf("rollback remove: %w", err)
			}
			a.addedFingerprint = ""
			return nil
		}
	}
	// Already gone — call Rollback idempotent.
	a.addedFingerprint = ""
	return nil
}

// signerPublicKey extracts the ssh.PublicKey from a parsed signer.
// ssh.ParseRawPrivateKey returns one of several typed keys (RSA, ed25519,
// ecdsa). The simplest path to a wire-format pub-key is via
// ssh.NewSignerFromKey(signer).PublicKey().
func signerPublicKey(rawKey interface{}) (ssh.PublicKey, error) {
	signer, err := ssh.NewSignerFromKey(rawKey)
	if err != nil {
		return nil, err
	}
	return signer.PublicKey(), nil
}

// Compile-time assertion.
var _ PersonaAdapter = (*SSHAdapter)(nil)
