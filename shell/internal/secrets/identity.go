package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// identityNameRe is the allowed character set for identity profile
// names. Stricter than POSIX filenames to avoid path-traversal and
// shell-quoting traps.
var identityNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// Identity describes an aish identity profile: a label, a gateway
// endpoint URL, and a SHA-256 fingerprint of the signer's public key.
// Nothing here is secret on its own — the active identity name is
// only a privacy signal — but we still chmod the files 0600 for
// consistency with the vault.
type Identity struct {
	Name               string
	GatewayURL         string
	SignerPubkeySHA256 string
}

const (
	identityFilePerm = 0o600
	identityDirPerm  = 0o700
)

// activeIdentityPath returns the path to ~/.aish/identity.toml.
func activeIdentityPath(home string) string {
	return filepath.Join(home, ".aish", "identity.toml")
}

// profilePath returns the path to a per-identity profile file.
func profilePath(home, name string) string {
	return filepath.Join(home, ".aish", "identities", name+".toml")
}

// LoadActive reads the active identity pointer. Returns a zero-value
// Identity (and no error) when the file does not exist — the caller
// can render "no active identity" without distinguishing missing
// from empty.
func LoadActive(home string) (Identity, error) {
	if home == "" {
		return Identity{}, errors.New("secrets: no home directory")
	}
	raw, err := os.ReadFile(activeIdentityPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, nil
	}
	if err != nil {
		return Identity{}, fmt.Errorf("secrets: read identity.toml: %w", err)
	}
	name := parseTOMLString(raw, "name")
	if name == "" {
		return Identity{}, nil
	}
	prof, err := readProfile(home, name)
	if err != nil {
		// Active pointer references a missing profile — surface that
		// state so the user can act on it (run `aish identity create
		// <name>` or `aish identity use <other>`).
		return Identity{}, fmt.Errorf("secrets: active identity %q has no profile: %w", name, err)
	}
	return prof, nil
}

// SetActive writes the active-identity pointer at ~/.aish/identity.toml.
// The named profile MUST exist at ~/.aish/identities/<name>.toml; a
// missing profile is an error rather than a silent forward-declaration.
func SetActive(home, name string) error {
	if home == "" {
		return errors.New("secrets: no home directory")
	}
	if !identityNameRe.MatchString(name) {
		return fmt.Errorf("secrets: invalid identity name %q", name)
	}
	if _, err := os.Stat(profilePath(home, name)); err != nil {
		return fmt.Errorf("secrets: profile %q not found: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".aish"), identityDirPerm); err != nil {
		return fmt.Errorf("secrets: mkdir .aish: %w", err)
	}
	body := fmt.Sprintf("# aish active identity — written by `aish identity use`\nname = %q\n", name)
	return atomicWriteFile(activeIdentityPath(home), []byte(body), identityFilePerm)
}

// CreateProfile writes a per-identity profile file. The name is
// validated; arbitrary characters cannot escape the identities/
// directory.
func CreateProfile(home string, id Identity) error {
	if home == "" {
		return errors.New("secrets: no home directory")
	}
	if !identityNameRe.MatchString(id.Name) {
		return fmt.Errorf("secrets: invalid identity name %q", id.Name)
	}
	dir := filepath.Join(home, ".aish", "identities")
	if err := os.MkdirAll(dir, identityDirPerm); err != nil {
		return fmt.Errorf("secrets: mkdir identities: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("# aish identity profile — written by `aish identity create`\n")
	sb.WriteString(fmt.Sprintf("name = %q\n", id.Name))
	if id.GatewayURL != "" {
		sb.WriteString(fmt.Sprintf("gateway_url = %q\n", id.GatewayURL))
	} else {
		sb.WriteString("gateway_url = \"\"\n")
	}
	if id.SignerPubkeySHA256 != "" {
		sb.WriteString(fmt.Sprintf("signer_pubkey_sha256 = %q\n", id.SignerPubkeySHA256))
	} else {
		sb.WriteString("signer_pubkey_sha256 = \"\"\n")
	}
	return atomicWriteFile(profilePath(home, id.Name), []byte(sb.String()), identityFilePerm)
}

// ListProfiles returns the names of all profiles, sorted.
func ListProfiles(home string) ([]string, error) {
	if home == "" {
		return nil, errors.New("secrets: no home directory")
	}
	dir := filepath.Join(home, ".aish", "identities")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: read identities dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := strings.TrimSuffix(e.Name(), ".toml")
		if n == e.Name() {
			// not a .toml file; skip
			continue
		}
		if !identityNameRe.MatchString(n) {
			// untrusted filename — skip
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// readProfile loads ~/.aish/identities/<name>.toml.
func readProfile(home, name string) (Identity, error) {
	raw, err := os.ReadFile(profilePath(home, name))
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Name:               parseTOMLString(raw, "name"),
		GatewayURL:         parseTOMLString(raw, "gateway_url"),
		SignerPubkeySHA256: parseTOMLString(raw, "signer_pubkey_sha256"),
	}, nil
}

// parseTOMLString is a deliberately tiny line-oriented TOML parser
// that handles only `key = "value"` lines. The shell's other TOML
// files (identity.toml, identities/*.toml) are flat enough that a
// real parser would be overkill for the secrets package. We avoid
// the dependency to keep the security-critical package's import
// surface minimal.
func parseTOMLString(raw []byte, key string) string {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			return v[1 : len(v)-1]
		}
	}
	return ""
}

// atomicWriteFile writes body to path via a tempfile + rename so a
// crash mid-write can never leave a half-written file. Permission
// bits are applied to the tempfile before rename and re-applied to
// the destination as defense in depth.
func atomicWriteFile(path string, body []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), identityDirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	_ = os.Chmod(path, perm)
	return nil
}
