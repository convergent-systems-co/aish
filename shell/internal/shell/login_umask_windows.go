//go:build windows

package shell

// applyUmask is the Windows no-op. Windows has no concept of a
// process-wide creation-mask; file permissions are governed by
// ACLs set per-file. The RC's [shell] umask field is silently
// ignored on this platform.
func applyUmask(mask int) {}
