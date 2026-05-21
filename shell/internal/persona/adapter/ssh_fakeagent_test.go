package adapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// fakeAgent runs an in-memory keyring listening on a unix socket. It
// speaks the SSH agent wire protocol via agent.ServeAgent so the
// adapter under test exercises the SAME code path it uses in
// production — list, add, remove, sign — without forking ssh-agent.
//
// The fake is intentionally minimal: agent.NewKeyring provides the
// in-memory store; agent.ServeAgent connects the wire protocol to it.
type fakeAgent struct {
	t        *testing.T
	socket   string
	keyring  agent.Agent
	listener net.Listener
	wg       sync.WaitGroup
	stopMu   sync.Mutex
	stopped  bool
}

// newFakeAgent starts a unix-socket SSH agent in a shortened temp dir.
// Unix-socket paths on darwin are capped at 104 characters; Go's
// default t.TempDir() under macOS easily blows that with the test
// name + suffix. We use os.MkdirTemp("/tmp", ...) which keeps the
// path short and register cleanup manually.
func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aishsock")
	if err != nil {
		t.Fatalf("fakeAgent: mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("fakeAgent: listen %s: %v", sock, err)
	}
	fa := &fakeAgent{
		t:        t,
		socket:   sock,
		keyring:  agent.NewKeyring(),
		listener: l,
	}
	fa.wg.Add(1)
	go fa.serve()
	t.Cleanup(fa.Stop)
	return fa
}

func (f *fakeAgent) serve() {
	defer f.wg.Done()
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			// Listener closed — stop.
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			// agent.ServeAgent blocks until the connection is closed.
			// We deliberately ignore the error: connection-EOF is
			// normal at test teardown.
			_ = agent.ServeAgent(f.keyring, c)
		}(conn)
	}
}

// Stop closes the listener. Safe to call multiple times.
func (f *fakeAgent) Stop() {
	f.stopMu.Lock()
	defer f.stopMu.Unlock()
	if f.stopped {
		return
	}
	f.stopped = true
	_ = f.listener.Close()
	f.wg.Wait()
}

// Dialer returns an AgentDialer wired to this fake's socket.
func (f *fakeAgent) Dialer() AgentDialer {
	return socketDialer{sock: f.socket}
}

type socketDialer struct{ sock string }

func (s socketDialer) Dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", s.sock)
}

// ListKeys returns the keys currently loaded in the fake agent's
// keyring. Used by tests to assert post-Apply / post-Rollback state.
func (f *fakeAgent) ListKeys() []*agent.Key {
	keys, err := f.keyring.List()
	if err != nil {
		f.t.Fatalf("fakeAgent: list: %v", err)
	}
	return keys
}

// AddKey adds the given parsed private key to the fake agent. Used to
// seed pre-Capture state.
func (f *fakeAgent) AddKey(priv interface{}) {
	if err := f.keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		f.t.Fatalf("fakeAgent: add: %v", err)
	}
}

// mapKeySource is a KeySource backed by a map literal — sufficient
// for tests.
type mapKeySource struct {
	keys map[string][]byte
	errs map[string]error
}

func (m *mapKeySource) GetPrivateKey(label string) ([]byte, error) {
	if err, ok := m.errs[label]; ok {
		return nil, err
	}
	v, ok := m.keys[label]
	if !ok {
		return nil, errIO(label)
	}
	// Return a COPY so the adapter can zero it without corrupting the
	// stored bytes (production vaults already return a copy out of
	// Open()).
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func errIO(label string) error {
	return ioErr{label: label}
}

type ioErr struct{ label string }

func (e ioErr) Error() string { return "no such key: " + e.label }

// genEd25519PEM returns a fresh ed25519 private key encoded in OPENSSH
// PEM. The exact bytes are returned alongside so adversarial tests can
// scan snapshots for them.
func genEd25519PEM(t *testing.T) (pemBytes []byte) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ed25519: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

// readAll is a tiny helper used by adversarial tests that want to slurp
// reader content.
func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	return b
}
