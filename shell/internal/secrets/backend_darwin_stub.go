//go:build !darwin

package secrets

// OpenDarwinBackend on non-darwin platforms is a compile-time stub
// that returns ErrUnsupported. The real implementation lives in
// backend_darwin.go (build tag `darwin`); this sentinel keeps the
// symbol resolvable on every other GOOS so dispatch code can
// reference it unconditionally.
//
// The service and account parameters mirror the darwin signature so
// the call site is identical regardless of GOOS.
func OpenDarwinBackend(service string, account string) (Backend, error) {
	return nil, ErrUnsupported
}
