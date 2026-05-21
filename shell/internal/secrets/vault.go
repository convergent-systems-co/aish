package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"time"
)

// vaultVersion is the on-disk format version. New writes always use
// this version; new fields may be added in a backwards-compatible way
// (reserved labels, profile). A version bump is a breaking change.
const vaultVersion = 1

// saltLen is the per-vault salt length in bytes. 16 is the Argon2id
// RFC recommendation; we use it.
const saltLen = 16

// vaultDirPerm + vaultFilePerm pin the POSIX permission bits. These
// MUST be enforced on every write.
const (
	vaultDirPerm  = 0o700
	vaultFilePerm = 0o600
)

// nameRe is the allowed character set for secret names. Names appear
// in plaintext on disk and in `aish secret list` output; restricting
// them to a small character class makes downstream rendering trivial
// (no shell quoting, no Unicode normalization, no surprises).
var nameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ErrNotFound is returned by Get and Rm when the named entry does not
// exist. Distinct from ErrDecrypt so callers can give a useful error.
var ErrNotFound = errors.New("secrets: entry not found")

// Vault holds an unlocked-but-not-decrypted view of the local secrets
// store. The 32-byte derived key lives on the struct for the session;
// individual values are decrypted on demand and never cached as
// plaintext.
type Vault struct {
	path   string
	key    []byte // 32 bytes, derived from passphrase + salt; Zero on Close
	header vaultHeader
	data   map[string]vaultEntry // name → encrypted entry
}

type vaultHeader struct {
	Version int          `json:"version"`
	KDF     vaultKDFMeta `json:"kdf"`
	Salt    string       `json:"salt_b64"`
}

type vaultKDFMeta struct {
	Algo        string `json:"algo"`        // always "argon2id"
	Time        uint32 `json:"time"`
	Memory      uint32 `json:"memory_kib"`
	Parallelism uint8  `json:"parallelism"`
}

type vaultEntry struct {
	Nonce      string   `json:"nonce_b64"`
	Ciphertext string   `json:"ciphertext_b64"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	Labels     []string `json:"labels,omitempty"` // reserved for #105 persona binding
}

type vaultOnDisk struct {
	vaultHeader
	Entries map[string]vaultEntry `json:"entries"`
}

// VaultPath returns the absolute path to the vault file under home.
// Exposed so callers and tests can stat / verify permissions without
// reaching into package internals.
func VaultPath(home string) string {
	return filepath.Join(home, ".aish", "vault", "vault.json")
}

// OpenVault opens (or creates) the vault under home. If no vault
// exists, one is initialized with a fresh random salt and the given
// KDF params. If a vault exists, params from the header are used to
// derive the key (the call-site params are advisory only).
//
// Empty passphrases are rejected.
//
// Returned Vault holds the derived key; the caller MUST call Close
// to zero it.
func OpenVault(home string, passphrase []byte, params KDFParams) (*Vault, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("secrets: empty passphrase")
	}
	if home == "" {
		return nil, errors.New("secrets: no home directory")
	}
	dir := filepath.Join(home, ".aish", "vault")
	if err := os.MkdirAll(dir, vaultDirPerm); err != nil {
		return nil, fmt.Errorf("secrets: mkdir vault: %w", err)
	}
	path := VaultPath(home)

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		return openExisting(path, raw, passphrase)
	case errors.Is(err, os.ErrNotExist):
		return createNew(path, passphrase, params)
	default:
		return nil, fmt.Errorf("secrets: read vault: %w", err)
	}
}

func createNew(path string, passphrase []byte, params KDFParams) (*Vault, error) {
	if params.KeyLen == 0 {
		params.KeyLen = KeySize
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("secrets: rand salt: %w", err)
	}
	key, err := Derive(passphrase, salt, params)
	if err != nil {
		return nil, err
	}
	v := &Vault{
		path: path,
		key:  key,
		header: vaultHeader{
			Version: vaultVersion,
			KDF: vaultKDFMeta{
				Algo:        "argon2id",
				Time:        params.Time,
				Memory:      params.Memory,
				Parallelism: params.Parallelism,
			},
			Salt: base64.StdEncoding.EncodeToString(salt),
		},
		data: map[string]vaultEntry{},
	}
	if err := v.save(); err != nil {
		Zero(key)
		return nil, err
	}
	return v, nil
}

func openExisting(path string, raw, passphrase []byte) (*Vault, error) {
	// Permission check on POSIX. On Windows the mode bits are
	// approximate; skip the check there.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("secrets: stat vault: %w", err)
		}
		if perm := info.Mode().Perm(); perm != vaultFilePerm {
			return nil, fmt.Errorf("secrets: vault has insecure permissions %#o (want %#o); refusing to open", perm, vaultFilePerm)
		}
	}

	var od vaultOnDisk
	if err := json.Unmarshal(raw, &od); err != nil {
		return nil, fmt.Errorf("secrets: parse vault: %w", err)
	}
	if od.Version != vaultVersion {
		return nil, fmt.Errorf("secrets: unsupported vault version %d", od.Version)
	}
	if od.KDF.Algo != "argon2id" {
		return nil, fmt.Errorf("secrets: unsupported KDF %q", od.KDF.Algo)
	}
	salt, err := base64.StdEncoding.DecodeString(od.Salt)
	if err != nil {
		return nil, fmt.Errorf("secrets: bad salt encoding: %w", err)
	}
	p := KDFParams{
		Time:        od.KDF.Time,
		Memory:      od.KDF.Memory,
		Parallelism: od.KDF.Parallelism,
		KeyLen:      KeySize,
	}
	key, err := Derive(passphrase, salt, p)
	if err != nil {
		return nil, err
	}
	if od.Entries == nil {
		od.Entries = map[string]vaultEntry{}
	}
	return &Vault{
		path:   path,
		key:    key,
		header: od.vaultHeader,
		data:   od.Entries,
	}, nil
}

// Close zeroes the in-memory key. After Close, the Vault is unusable.
func (v *Vault) Close() {
	if v == nil {
		return
	}
	Zero(v.key)
	v.key = nil
}

// Set encrypts value under the vault key and stores it under name.
// Overwrites any existing entry. Persists to disk before returning.
// Caller is responsible for treating value as secret (the slice is
// NOT zeroed by this function — the caller owns the buffer).
func (v *Vault) Set(name string, value []byte) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("secrets: invalid name %q (want %s)", name, nameRe.String())
	}
	if len(value) == 0 {
		return errors.New("secrets: empty value")
	}
	if v.key == nil {
		return errors.New("secrets: vault closed")
	}
	nonce, ct, err := Seal(v.key, value)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	entry := vaultEntry{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		UpdatedAt:  now,
	}
	if existing, ok := v.data[name]; ok {
		entry.CreatedAt = existing.CreatedAt
	} else {
		entry.CreatedAt = now
	}
	v.data[name] = entry
	return v.save()
}

// Get decrypts the named entry's value. Returns ErrNotFound if the
// name doesn't exist, ErrDecrypt for any decryption failure. The
// caller MUST treat the returned slice as secret and call Zero on it
// before it goes out of scope.
func (v *Vault) Get(name string) ([]byte, error) {
	if v.key == nil {
		return nil, errors.New("secrets: vault closed")
	}
	entry, ok := v.data[name]
	if !ok {
		return nil, ErrNotFound
	}
	nonce, err := base64.StdEncoding.DecodeString(entry.Nonce)
	if err != nil {
		return nil, ErrDecrypt
	}
	ct, err := base64.StdEncoding.DecodeString(entry.Ciphertext)
	if err != nil {
		return nil, ErrDecrypt
	}
	return Open(v.key, nonce, ct)
}

// Rm deletes the named entry. Returns ErrNotFound if no such entry.
func (v *Vault) Rm(name string) error {
	if v.key == nil {
		return errors.New("secrets: vault closed")
	}
	if _, ok := v.data[name]; !ok {
		return ErrNotFound
	}
	delete(v.data, name)
	return v.save()
}

// List returns the names of all entries, sorted lexicographically.
// Names are not secret; values are. No value information is leaked.
func (v *Vault) List() []string {
	names := make([]string, 0, len(v.data))
	for n := range v.data {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Has reports whether the named entry exists. Convenience for callers
// that want to avoid a Get just to probe existence.
func (v *Vault) Has(name string) bool {
	_, ok := v.data[name]
	return ok
}

// save atomically rewrites the vault file. The on-disk path is
// updated via a tempfile-and-rename dance so a crash mid-write never
// leaves a half-written file.
func (v *Vault) save() error {
	od := vaultOnDisk{vaultHeader: v.header, Entries: v.data}
	body, err := json.MarshalIndent(od, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: marshal vault: %w", err)
	}
	dir := filepath.Dir(v.path)
	tmp, err := os.CreateTemp(dir, "vault-*.json.tmp")
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
	if err := os.Rename(tmpName, v.path); err != nil {
		cleanup()
		return fmt.Errorf("secrets: rename: %w", err)
	}
	// Defense in depth: chmod the destination too. On filesystems
	// where rename preserves source permissions this is a no-op; on
	// the rare case where it doesn't, we still land at 0600.
	_ = os.Chmod(v.path, vaultFilePerm)
	return nil
}
