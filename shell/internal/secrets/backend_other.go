//go:build !windows && !darwin

package secrets

// Compile-time stubs for platforms where the OS-keychain backends
// aren't (yet) wired. The Windows + macOS backends live in their own
// build-tagged files; this stub lets dispatch code reference both
// symbols on Linux + BSD + the rest without per-OS conditionals.
//
// Linux Secret Service (v0.3-3 task #101) is a separate PR — its
// D-Bus session-bus story needs its own design pass.

// OpenWindowsBackend on non-Windows returns ErrUnsupported.
func OpenWindowsBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}

// OpenDarwinBackend on non-Darwin returns ErrUnsupported.
func OpenDarwinBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}
