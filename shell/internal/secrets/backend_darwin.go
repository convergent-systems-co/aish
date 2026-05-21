//go:build darwin

// backend_darwin.go implements the macOS-native secrets backend
// behind the cross-platform Backend interface. The login keychain is
// the storage surface; access is via the `/usr/bin/security` CLI
// rather than CGO bindings to `Security.framework`. Trade-off chosen
// in `.artifacts/plans/v0.3-fu-keychain.md`:
//
//   - `security` is part of the OS — present on every macOS install
//     since 10.x. No third-party dependency, no CGO, no Xcode CLT.
//   - Sub-process spawn adds ~10 ms per call. Acceptable for an
//     interactive shell built-in; secret operations are not on any
//     hot path.
//   - The CLI surface is the same one an administrator would use
//     manually, which makes the behavior easy to audit and explain.
//
// The login keychain is the user's default keychain (visible in
// Keychain Access.app under "login"). Items are stored as "generic
// passwords" with a hardcoded service name (default `"aish"`) and the
// caller-supplied secret name as the account field. macOS encrypts
// the keychain at rest with a key derived from the login password;
// when the keychain is locked, `security` either blocks for a UI
// unlock prompt or fails — see the `Get` documentation.
//
// macOS has no "list every account under a service" command in
// `security` that doesn't either dump the entire keychain or prompt
// per item. To support `List` cheaply we maintain a small index file
// at `~/.aish/keychain-index.json` (mode 0600) containing only the
// non-secret names this backend has written. `List` validates each
// name against the live keychain via `Has` and prunes stale entries
// silently.
package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
)

// securityBin is the absolute path to the macOS keychain CLI. Using
// the absolute path (rather than relying on $PATH) defends against a
// shim binary inserted earlier in PATH. SIP protects /usr/bin/security
// from non-root writes.
const securityBin = "/usr/bin/security"

// errSecItemNotFound is the exit code `security` returns when an
// item is not present. From the SecBase.h header (
// `errSecItemNotFound = -25300`); the CLI maps it to exit code 44.
const errSecItemNotFound = 44

// darwinBackend implements Backend against the macOS login keychain
// via /usr/bin/security. The service field is the keychain "service"
// label (a.k.a. -s flag); account is an optional prefix applied to
// every secret name (a.k.a. -a flag) so multiple aish identities can
// coexist in one keychain.
type darwinBackend struct {
	service   string // -s value; default "aish"
	account   string // -a prefix; default ""
	indexPath string
	mu        sync.Mutex
	closed    bool
}

// keychainIndex is the on-disk index of names this backend has
// written. The file holds names only, never values; values live in
// the keychain. The index is advisory — List cross-checks every
// entry against the live keychain via Has and prunes stale names.
type keychainIndex struct {
	Service string   `json:"service"`
	Names   []string `json:"names"`
}

// OpenDarwinBackend returns a Backend that stores secrets in the
// macOS login keychain via /usr/bin/security.
//
//   - service: the -s value attached to every generic-password entry.
//     If empty, "aish" is used. Multiple tools sharing a user's
//     keychain pick different service names to avoid collisions.
//
//   - account: an optional prefix applied to the -a value (the secret
//     name) for every operation. Empty by default. Useful when an
//     identity wants its own namespace inside a shared service label.
//
// The index file path is fixed at $HOME/.aish/keychain-index.json
// with mode 0600 on the file and 0700 on the parent. If $HOME is
// unset, OpenDarwinBackend returns an error.
func OpenDarwinBackend(service string, account string) (Backend, error) {
	if service == "" {
		service = "aish"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("secrets: no home directory for keychain index")
	}
	dir := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dir, vaultDirPerm); err != nil {
		return nil, fmt.Errorf("secrets: mkdir aish home: %w", err)
	}
	// Sanity-check that /usr/bin/security is present and executable.
	// macOS ships it as part of the OS; missing it means we're on a
	// non-macOS host (impossible — build tag) or a deeply corrupted
	// install (the user has bigger problems than aish).
	if _, statErr := os.Stat(securityBin); statErr != nil {
		return nil, fmt.Errorf("secrets: %s not available: %w", securityBin, statErr)
	}
	return &darwinBackend{
		service:   service,
		account:   account,
		indexPath: filepath.Join(dir, "keychain-index.json"),
	}, nil
}

// fullAccount returns the -a value for the given user-supplied name.
// The optional account prefix lets multiple identities coexist in
// one keychain entry namespace.
func (b *darwinBackend) fullAccount(name string) string {
	if b.account == "" {
		return name
	}
	return b.account + ":" + name
}

// Set writes value under name. Uses `security add-generic-password
// -U`; the -U flag is "update if exists," which makes Set
// idempotent-with-overwrite per the Backend contract.
//
// The password is passed via -w on the command line. macOS does NOT
// expose the process argv to other users on the same machine — only
// to the same user via `ps`. The same-user threat is out of our
// model (a same-user attacker can also dump our process memory).
// We avoid embedding the value in a shell string; exec.Command's
// argv array bypasses shell interpolation.
func (b *darwinBackend) Set(name string, value []byte) error {
	if err := b.guard(name); err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("secrets: empty value")
	}
	cmd := exec.Command(securityBin,
		"add-generic-password",
		"-U", // update if exists
		"-s", b.service,
		"-a", b.fullAccount(name),
		"-w", string(value),
	)
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secrets: security add-generic-password: %w (stderr: %s)", err, stderr.String())
	}
	return b.indexAdd(name)
}

// Get reads the named entry. Returns ErrNotFound for missing entries.
// If the login keychain is locked, `security` may either prompt the
// user via the GUI or return an error — we surface either outcome to
// the caller; we do NOT swallow the failure.
//
// The returned slice is a fresh allocation; the caller MUST Zero it
// when done.
func (b *darwinBackend) Get(name string) ([]byte, error) {
	if err := b.guard(name); err != nil {
		return nil, err
	}
	cmd := exec.Command(securityBin,
		"find-generic-password",
		"-w", // print just the password to stdout
		"-s", b.service,
		"-a", b.fullAccount(name),
	)
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == errSecItemNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("secrets: security find-generic-password: %w (stderr: %s)", err, stderr.String())
	}
	out := stdout.Bytes()
	// `security -w` appends exactly one trailing newline. Strip
	// exactly one — do NOT use bytes.TrimSpace, because a legitimate
	// secret may end in space or tab.
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	// Copy into a slice we own; the bytes.Buffer's internal slice
	// is part of the buffer's backing array.
	result := make([]byte, len(out))
	copy(result, out)
	// Best-effort zero of the stdout buffer's bytes. Once stdout is
	// gone the Go runtime may reuse the memory; the caller's
	// returned slice is the copy above.
	Zero(stdout.Bytes())
	return result, nil
}

// Rm deletes the named entry. Returns ErrNotFound for missing names.
func (b *darwinBackend) Rm(name string) error {
	if err := b.guard(name); err != nil {
		return err
	}
	cmd := exec.Command(securityBin,
		"delete-generic-password",
		"-s", b.service,
		"-a", b.fullAccount(name),
	)
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == errSecItemNotFound {
			// Still prune the index — a stale index entry would
			// otherwise persist forever.
			_ = b.indexRm(name)
			return ErrNotFound
		}
		return fmt.Errorf("secrets: security delete-generic-password: %w (stderr: %s)", err, stderr.String())
	}
	return b.indexRm(name)
}

// List returns the names of every entry the index knows about, after
// cross-checking each against the live keychain via Has. Stale index
// entries (deleted out-of-band via Keychain Access.app or `security
// delete-generic-password` directly) are pruned silently.
//
// Names are returned sorted lexicographically.
func (b *darwinBackend) List() ([]string, error) {
	if err := b.checkClosed(); err != nil {
		return nil, err
	}
	idx, err := b.indexRead()
	if err != nil {
		return nil, err
	}
	live := make([]string, 0, len(idx.Names))
	stale := make([]string, 0)
	for _, n := range idx.Names {
		ok, err := b.hasLocked(n)
		if err != nil {
			return nil, err
		}
		if ok {
			live = append(live, n)
		} else {
			stale = append(stale, n)
		}
	}
	if len(stale) > 0 {
		// Prune the index. Best-effort — if we fail to write we
		// still return the accurate live names; the next List() will
		// retry the prune.
		idx.Names = live
		_ = b.indexWrite(idx)
	}
	sort.Strings(live)
	return live, nil
}

// Has reports whether the named entry exists. Implemented via
// `security find-generic-password` without -w (we don't want the
// password on stdout, only the exit code).
func (b *darwinBackend) Has(name string) (bool, error) {
	if err := b.guard(name); err != nil {
		return false, err
	}
	return b.hasLocked(name)
}

// hasLocked is the closed-state-already-checked variant. Called by
// Has (after guard) and by List (after checkClosed). Avoids
// re-acquiring the mutex inside List's per-entry loop.
func (b *darwinBackend) hasLocked(name string) (bool, error) {
	cmd := exec.Command(securityBin,
		"find-generic-password",
		"-s", b.service,
		"-a", b.fullAccount(name),
	)
	cmd.Stdin = nil
	cmd.Stdout = nil // discard the keychain item dump
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == errSecItemNotFound {
		return false, nil
	}
	return false, fmt.Errorf("secrets: security find-generic-password: %w (stderr: %s)", err, stderr.String())
}

// Close marks the backend unusable. Idempotent. No backend-side
// resources to release — the index file is closed after every read
// or write, and there's no persistent process state.
func (b *darwinBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

// guard validates the name and the backend state. Centralized so
// every public method gets the same checks in the same order.
func (b *darwinBackend) guard(name string) error {
	if err := b.checkClosed(); err != nil {
		return err
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("secrets: invalid name %q (want %s)", name, nameRe.String())
	}
	return nil
}

func (b *darwinBackend) checkClosed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("secrets: backend closed")
	}
	return nil
}

// indexRead loads the on-disk index. A missing file yields an empty
// index, not an error.
func (b *darwinBackend) indexRead() (keychainIndex, error) {
	raw, err := os.ReadFile(b.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return keychainIndex{Service: b.service, Names: nil}, nil
	}
	if err != nil {
		return keychainIndex{}, fmt.Errorf("secrets: read keychain index: %w", err)
	}
	var idx keychainIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return keychainIndex{}, fmt.Errorf("secrets: parse keychain index: %w", err)
	}
	if idx.Service == "" {
		idx.Service = b.service
	}
	return idx, nil
}

// indexWrite atomically rewrites the index file at 0600. Uses
// tempfile-and-rename to avoid a half-written file on crash.
func (b *darwinBackend) indexWrite(idx keychainIndex) error {
	if idx.Service == "" {
		idx.Service = b.service
	}
	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: marshal keychain index: %w", err)
	}
	dir := filepath.Dir(b.indexPath)
	tmp, err := os.CreateTemp(dir, "keychain-index-*.json.tmp")
	if err != nil {
		return fmt.Errorf("secrets: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("secrets: write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("secrets: sync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("secrets: close tempfile: %w", err)
	}
	if err := os.Chmod(tmpName, vaultFilePerm); err != nil {
		cleanup()
		return fmt.Errorf("secrets: chmod tempfile: %w", err)
	}
	if err := os.Rename(tmpName, b.indexPath); err != nil {
		cleanup()
		return fmt.Errorf("secrets: rename: %w", err)
	}
	_ = os.Chmod(b.indexPath, vaultFilePerm)
	return nil
}

// indexAdd inserts name into the index if absent. No-op on
// already-present names.
func (b *darwinBackend) indexAdd(name string) error {
	idx, err := b.indexRead()
	if err != nil {
		return err
	}
	for _, n := range idx.Names {
		if n == name {
			return nil
		}
	}
	idx.Names = append(idx.Names, name)
	sort.Strings(idx.Names)
	return b.indexWrite(idx)
}

// indexRm removes name from the index if present. No-op on absent
// names so callers can use it as a pruning step.
func (b *darwinBackend) indexRm(name string) error {
	idx, err := b.indexRead()
	if err != nil {
		return err
	}
	out := idx.Names[:0]
	for _, n := range idx.Names {
		if n != name {
			out = append(out, n)
		}
	}
	if len(out) == len(idx.Names) {
		return nil
	}
	idx.Names = out
	return b.indexWrite(idx)
}
