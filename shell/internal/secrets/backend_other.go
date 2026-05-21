//go:build !windows && !darwin && !linux

package secrets

// Compile-time stubs for platforms where no OS-keychain backend is
// wired. The Windows + macOS + Linux backends live in their own
// build-tagged files; this stub lets dispatch code reference every
// constructor symbol on the BSDs / Plan 9 / Illumos / anything else
// without per-OS conditionals at the call site.

// OpenWindowsBackend on this OS returns ErrUnsupported.
func OpenWindowsBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}

// OpenDarwinBackend on this OS returns ErrUnsupported.
func OpenDarwinBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}

// OpenLinuxBackend on this OS returns ErrUnsupported.
func OpenLinuxBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}
