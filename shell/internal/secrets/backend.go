package secrets

import "errors"

// Backend is the cross-platform contract for a secrets store. The
// local-file vault is one implementation (see Vault); OS-native
// keychain backends — Windows Credential Manager, macOS Keychain,
// freedesktop Secret Service — are others, gated by build tags so
// each binary only links what its target OS can actually call.
//
// The interface is deliberately the smallest surface that the
// `aish secret` built-in needs:
//
//	Set, Get, Rm, List, Has, Close.
//
// Implementations MUST treat values as opaque byte slices and MUST
// NOT log, echo, or otherwise side-channel them. The same "no
// plaintext escape" rule that governs the local vault (see doc.go)
// applies to every Backend.
type Backend interface {
	// Set stores value under name, overwriting any existing entry.
	// Implementations MUST NOT retain a reference to value after
	// Set returns — the caller owns the buffer and may zero it.
	Set(name string, value []byte) error

	// Get returns the plaintext stored under name. Returns
	// ErrNotFound if no such entry exists. The caller MUST treat
	// the returned slice as secret and Zero it before it falls out
	// of scope.
	Get(name string) ([]byte, error)

	// Rm deletes the named entry. Returns ErrNotFound if no such
	// entry. Idempotent only at the caller's discretion — backends
	// MUST surface "not found" rather than silently succeed.
	Rm(name string) error

	// List returns the names of all entries owned by this backend,
	// sorted lexicographically. Names are not secret; values never
	// appear in the result. Returns an error only if enumeration
	// itself fails (a backend whose enumeration cannot fail SHOULD
	// return a nil error).
	List() ([]string, error)

	// Has reports whether the named entry exists. Convenience for
	// callers that want to probe without a full Get. Returns an
	// error only if the existence check itself fails.
	Has(name string) (bool, error)

	// Close releases any backend-side resources (DLL handles,
	// in-memory keys, file locks). Safe to call multiple times.
	// After Close, all other methods return an error.
	Close() error
}

// ErrUnsupported is returned by backend constructors on platforms
// where the requested backend cannot run. Callers SHOULD treat this
// as a clean "fall back to the local vault" signal, not a hard
// failure.
//
// Example: OpenWindowsBackend on macOS returns ErrUnsupported so the
// dispatch layer can fall through to the cross-platform LocalVault
// without a special-cased OS check at the call site.
var ErrUnsupported = errors.New("secrets: backend unsupported on this platform")
