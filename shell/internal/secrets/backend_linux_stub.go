//go:build !linux

package secrets

// OpenLinuxBackend on non-linux platforms is a compile-time stub
// that returns ErrUnsupported. The real implementation lives in
// backend_linux.go (build tag `linux`); this sentinel keeps the
// symbol resolvable on every other GOOS so dispatch code can
// reference it unconditionally.
//
// The service and schema parameters mirror the linux signature so
// the call site is identical regardless of GOOS.
func OpenLinuxBackend(service string, schema string) (Backend, error) {
	return nil, ErrUnsupported
}
