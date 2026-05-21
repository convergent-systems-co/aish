// Package secrets implements the v0.3-3 local encrypted vault and the
// identity pointer that selects which vault is active.
//
// # Threat model (summary; see .artifacts/plans/v0.3-3.md for the full table)
//
//   - Stolen vault file alone: Argon2id + AES-256-GCM. Adversary needs the
//     passphrase. Brute-force is bounded by the KDF cost.
//   - In-memory dump while unlocked: cleartext values are zeroed after each
//     Get; the session-scoped key cache holds the derived key only.
//     mlock / madvise(DONTDUMP) are NOT used in this PR — documented
//     limitation. On macOS and most Linux distros, swap is encrypted.
//   - Plaintext via stdout / stderr / history: Get writes to the OS
//     clipboard only; no value is ever printed. History events for
//     secret.get record the name, never the value.
//   - Wrong passphrase: AES-GCM auth-tag verification is constant-time.
//     No early return based on partial decryption. Error message names no
//     key material.
//   - Empty passphrase: rejected at both set-init and get-time.
//
// # File layout
//
//	~/.aish/vault/vault.json         (mode 0600, dir mode 0700)
//	~/.aish/identity.toml            (active identity pointer)
//	~/.aish/identities/<name>.toml   (per-identity profiles)
//
// # Backends
//
// As of v1.0-4, the package exposes a Backend interface (backend.go)
// that captures the operations the `aish secret` built-in needs:
// Set, Get, Rm, List, Has, Close. Two implementations exist:
//
//   - LocalVault (this package; default cross-platform) — the
//     Argon2id + AES-256-GCM file vault documented above.
//
//   - WindowsBackend (backend_windows.go) — Credential Manager
//     storage with DPAPI-wrapped values. Returned by
//     OpenWindowsBackend; the `!windows` build of that constructor
//     returns ErrUnsupported.
//
// Dispatch (the choice of which backend to use at runtime) is the
// responsibility of the caller, not this package.
//
// # Non-goals (current scope)
//
//   - Parser-level taint propagation (#96, #98, #99).
//   - macOS Keychain / freedesktop Secret Service backends
//     (post-v1.0).
//   - Persona-bound secrets (#105); the labels field is reserved but
//     unused.
//   - Passphrase change / rekey.
//   - Vault export/import.
//   - Built-in dispatch between LocalVault and WindowsBackend; the
//     `secret` built-in still calls OpenVault unconditionally
//     (deferred to v1.0-5).
package secrets
