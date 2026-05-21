//go:build !windows

package secrets

// OpenWindowsBackend on non-Windows platforms is a compile-time stub
// that returns ErrUnsupported. This lets dispatch code reference the
// symbol unconditionally — the build tag selects between the real
// implementation (backend_windows.go) and this sentinel.
//
// The prefix and entropy parameters mirror the Windows signature so
// the call site is identical regardless of GOOS.
func OpenWindowsBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}
