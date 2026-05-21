//go:build darwin

// macOS Keychain backend for aish secrets.
//
// This backend NEVER touches the user's login keychain. It creates
// and operates exclusively on a dedicated keychain file at
// $HOME/Library/Keychains/aish.keychain-db. The bootstrap passphrase
// is generated once at first open and stored at $HOME/.aish/
// keychain.bootstrap (mode 0600). The user can later change the
// passphrase via Keychain.app and update the bootstrap file; aish
// only reads the file on Open.
//
// All operations dispatch through the SIP-protected /usr/bin/security
// CLI — no CGO, no MachO frameworks. The known §4 limitation: the
// `security add-generic-password -w <value>` form puts the value on
// the subprocess argv, visible to `ps` for the call's duration.
// Mitigation: only the aish-owned process tree spawns these calls;
// on a single-user machine the exposure window is sub-millisecond.
// A future PR can switch to a CGO shim if argv visibility becomes a
// concern (out of scope for this MVP).
package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// securityBin is the absolute path to the macOS keychain CLI.
	// Using the absolute path means a PATH-injected shim cannot
	// silently substitute for it. SIP guarantees this binary's
	// integrity on a stock macOS install.
	securityBin = "/usr/bin/security"

	// aishKeychainName is the keychain identifier passed to
	// `security`. On disk this materialises as
	// ~/Library/Keychains/aish.keychain-db.
	aishKeychainName = "aish.keychain"

	// aishService is the keychain "service" field used for every
	// entry written by aish. Multiple aish identities scope the
	// service further as "aish:<prefix>".
	aishService = "aish"
)

// errExitNotFound is the exit code `security` returns when an item
// doesn't exist. Used to map to the package-level ErrNotFound.
const errExitNotFound = 44

// darwinBackend implements Backend against the aish-dedicated keychain
// on macOS.
type darwinBackend struct {
	service string
	kc      string

	mu     sync.Mutex
	closed bool
}

// OpenDarwinBackend returns a Backend whose storage is the
// aish.keychain-db file. The keychain is created on first open with a
// random 32-byte hex passphrase persisted at ~/.aish/keychain.bootstrap
// (mode 0600). On subsequent opens the existing keychain is unlocked
// using the stored passphrase.
//
// prefix scopes the service name; pass "" for the default. Multiple
// identities (per v0.3-3 identity engine) may pass distinct prefixes
// so their secrets coexist in one keychain.
//
// entropy is reserved (cross-backend signature compatibility with the
// Windows backend's DPAPI seed); ignored on macOS — the keychain's
// at-rest crypto is managed by the OS.
func OpenDarwinBackend(prefix string, entropy []byte) (Backend, error) {
	if _, err := os.Stat(securityBin); err != nil {
		return nil, fmt.Errorf("secrets: darwin: %s missing (%w)", securityBin, ErrUnsupported)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("secrets: darwin: home dir: %w", err)
	}

	keychainPath := filepath.Join(home, "Library", "Keychains", "aish.keychain-db")
	bootstrapPath := filepath.Join(home, ".aish", "keychain.bootstrap")

	passphrase, err := readOrCreateBootstrap(bootstrapPath)
	if err != nil {
		return nil, err
	}

	exists, err := keychainExists(keychainPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := createAishKeychain(passphrase); err != nil {
			return nil, err
		}
	}

	if err := unlockAishKeychain(passphrase); err != nil {
		return nil, fmt.Errorf("secrets: darwin: unlock keychain (passphrase mismatch? "+
			"check %s vs Keychain.app): %w", bootstrapPath, err)
	}

	return &darwinBackend{
		service: serviceFor(prefix),
		kc:      aishKeychainName,
	}, nil
}

// serviceFor builds the keychain service field for a given prefix.
// Empty prefix → "aish"; non-empty → "aish:<prefix>". Lower-case is
// not enforced — the prefix flows from the identity engine, which
// already validates its names.
func serviceFor(prefix string) string {
	if prefix == "" {
		return aishService
	}
	return aishService + ":" + prefix
}

// keychainExists returns true when the aish keychain file is present
// on disk. Stat is sufficient — the file's existence implies the
// keychain has been created at least once.
func keychainExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("secrets: darwin: stat keychain: %w", err)
}

// readOrCreateBootstrap reads the bootstrap passphrase from path, or
// generates and persists a new random 32-byte hex passphrase when the
// file does not exist. The file is created with mode 0600 in a
// directory created mode 0700.
func readOrCreateBootstrap(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimRight(string(b), "\n\r"), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("secrets: read bootstrap: %w", err)
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("secrets: rand: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("secrets: mkdir bootstrap: %w", err)
	}
	passphrase := hex.EncodeToString(buf[:])
	if err := os.WriteFile(path, []byte(passphrase+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("secrets: write bootstrap: %w", err)
	}
	return passphrase, nil
}

// createAishKeychain creates a fresh aish.keychain-db with the given
// passphrase. The `-p` form puts the passphrase on argv, which is the
// one-time exposure during creation. The created keychain inherits
// macOS's default lock settings (5-minute idle lock).
func createAishKeychain(passphrase string) error {
	cmd := exec.Command(securityBin, "create-keychain", "-p", passphrase, aishKeychainName)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secrets: create-keychain: %w", err)
	}
	// Disable auto-lock so the keychain stays unlocked for the
	// duration of the aish session. Locking is managed by the
	// shell's Close, not by an idle timer that would interrupt
	// inference plugins mid-call.
	if err := exec.Command(securityBin, "set-keychain-settings", aishKeychainName).Run(); err != nil {
		// Non-fatal; default settings are still functional.
		fmt.Fprintf(os.Stderr, "secrets: set-keychain-settings (non-fatal): %v\n", err)
	}
	return nil
}

// unlockAishKeychain unlocks the aish keychain using the bootstrap
// passphrase. Argv exposure as documented at top of file.
func unlockAishKeychain(passphrase string) error {
	cmd := exec.Command(securityBin, "unlock-keychain", "-p", passphrase, aishKeychainName)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secrets: unlock-keychain: %w", err)
	}
	return nil
}

// Set stores value under name. If an entry already exists it is
// replaced (security has no upsert; we delete-then-add).
func (b *darwinBackend) Set(name string, value []byte) error {
	if err := b.guard(); err != nil {
		return err
	}
	// Best-effort delete; ignore "not found".
	if err := b.deleteEntry(name); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	cmd := exec.Command(securityBin, "add-generic-password",
		"-s", b.service, "-a", name, "-w", string(value), b.kc)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secrets: add-generic-password: %w", err)
	}
	return nil
}

// Get returns the plaintext value for name.
func (b *darwinBackend) Get(name string) ([]byte, error) {
	if err := b.guard(); err != nil {
		return nil, err
	}
	cmd := exec.Command(securityBin, "find-generic-password",
		"-s", b.service, "-a", name, "-w", b.kc)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("secrets: find-generic-password: %w", err)
	}
	// `security -w` writes the password + newline; strip trailing
	// newline only.
	v := bytes.TrimRight(out.Bytes(), "\n")
	return v, nil
}

// Rm deletes the named entry. Returns ErrNotFound if no such entry.
func (b *darwinBackend) Rm(name string) error {
	if err := b.guard(); err != nil {
		return err
	}
	return b.deleteEntry(name)
}

// deleteEntry is the inner delete used by Rm and by Set's overwrite path.
func (b *darwinBackend) deleteEntry(name string) error {
	cmd := exec.Command(securityBin, "delete-generic-password",
		"-s", b.service, "-a", name, b.kc)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if isNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("secrets: delete-generic-password: %w", err)
	}
	return nil
}

// Has reports whether the named entry exists.
func (b *darwinBackend) Has(name string) (bool, error) {
	if err := b.guard(); err != nil {
		return false, err
	}
	cmd := exec.Command(securityBin, "find-generic-password",
		"-s", b.service, "-a", name, b.kc)
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("secrets: find (Has): %w", err)
	}
	return true, nil
}

// List enumerates entry names in the aish keychain that match this
// backend's service. The `security dump-keychain` form is the only
// way to enumerate generic passwords; we parse its plaintext output.
//
// Important: dump-keychain WITHOUT `-d` lists item metadata only —
// names and attributes, never plaintext values. We must NOT pass `-d`.
func (b *darwinBackend) List() ([]string, error) {
	if err := b.guard(); err != nil {
		return nil, err
	}
	cmd := exec.Command(securityBin, "dump-keychain", b.kc)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("secrets: dump-keychain: %w", err)
	}
	return parseDumpKeychain(out.Bytes(), b.service), nil
}

// parseDumpKeychain extracts account names for items whose service
// matches wantService. The output format is multi-line per-item;
// each item has lines like:
//
//	"svce"<blob>="aish:work"
//	"acct"<blob>="MY_API_KEY"
//
// Items are separated by `keychain:` lines.
func parseDumpKeychain(raw []byte, wantService string) []string {
	type item struct {
		service string
		account string
	}
	var items []item
	var cur item
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "keychain:") {
			if cur.service != "" && cur.account != "" {
				items = append(items, cur)
			}
			cur = item{}
			continue
		}
		if v, ok := extractBlob(ln, "svce"); ok {
			cur.service = v
		}
		if v, ok := extractBlob(ln, "acct"); ok {
			cur.account = v
		}
	}
	if cur.service != "" && cur.account != "" {
		items = append(items, cur)
	}

	names := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		if it.service != wantService {
			continue
		}
		if seen[it.account] {
			continue
		}
		seen[it.account] = true
		names = append(names, it.account)
	}
	// Sort lexicographically per the Backend contract.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

// extractBlob parses `"<key>"<blob>="<value>"` line forms emitted by
// dump-keychain. Returns (value, true) on a key match; ("", false)
// otherwise.
func extractBlob(line, key string) (string, bool) {
	want := `"` + key + `"<blob>=`
	idx := strings.Index(line, want)
	if idx < 0 {
		return "", false
	}
	rest := line[idx+len(want):]
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	end := strings.LastIndex(rest, `"`)
	if end <= 0 {
		return "", false
	}
	return rest[1:end], true
}

// Close locks the aish keychain and marks the backend as closed.
// Idempotent — subsequent calls are no-ops.
func (b *darwinBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	// Best-effort lock; not fatal.
	_ = exec.Command(securityBin, "lock-keychain", aishKeychainName).Run()
	return nil
}

// guard centralises the closed-check; every public method runs it first.
func (b *darwinBackend) guard() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("secrets: backend closed")
	}
	return nil
}

// isNotFound maps `security`'s exit code 44 to the package-level
// ErrNotFound sentinel.
func isNotFound(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() == errExitNotFound
	}
	return false
}

// OpenWindowsBackend on darwin returns ErrUnsupported. The Windows
// backend's real impl is in backend_windows.go (build:windows); this
// stub lets dispatch code reference the symbol on darwin builds.
func OpenWindowsBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}
