package shell

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentKeyrings maps the socket path → its dedicated keyring. Each
// test that calls startFakeAgent gets a fresh keyring for its socket
// so cross-test residue (keys left by a prior Apply) doesn't
// contaminate a subsequent test's Capture.
var (
	agentKeyringsMu sync.Mutex
	agentKeyrings   = map[string]agent.Agent{}
)

// registerAgentKeyring associates a fresh keyring with the given
// socket path. Called by startFakeAgent before its accept goroutine
// starts.
func registerAgentKeyring(socket string) agent.Agent {
	agentKeyringsMu.Lock()
	defer agentKeyringsMu.Unlock()
	kr := agent.NewKeyring()
	agentKeyrings[socket] = kr
	return kr
}

// keyringForConn returns the keyring registered for the given
// connection's local address. Each unix-socket Accept's LocalAddr
// holds the listener's socket path.
func keyringForConn(c net.Conn) agent.Agent {
	socket := c.LocalAddr().String()
	agentKeyringsMu.Lock()
	defer agentKeyringsMu.Unlock()
	if kr, ok := agentKeyrings[socket]; ok {
		return kr
	}
	// Fallback — shouldn't happen, but a fresh keyring is the safe
	// default rather than panicking inside a goroutine.
	return agent.NewKeyring()
}

// serveSSHAgent wires a single accepted unix-socket connection to
// agent.ServeAgent over the keyring associated with that socket.
func serveSSHAgent(c net.Conn) {
	defer c.Close()
	_ = agent.ServeAgent(keyringForConn(c), c)
}

// generateEd25519PEM returns a fresh ed25519 OpenSSH PEM-encoded
// private key. Each test gets a fresh key so the adversarial
// no-secret-in-event assertion can scan for these exact bytes.
func generateEd25519PEM(t *testing.T) []byte {
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
