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
// # Non-goals (this PR)
//
//   - Parser-level taint propagation (#96, #98, #99).
//   - OS keychain backends (#100, #101) — the Backend interface is
//     reserved; LocalVault is the only implementation here.
//   - Persona-bound secrets (#105); the labels field is reserved but
//     unused.
//   - Passphrase change / rekey.
//   - Vault export/import.
package secrets
