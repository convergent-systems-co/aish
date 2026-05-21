//go:build !windows

package shell

import "syscall"

// applyUmask is the POSIX implementation: install the new mask via
// syscall.Umask. The return value (the previous mask) is discarded —
// aish has no concept of "save and restore umask" for v0.3-1.
//
// umask is a per-process attribute; setting it here affects every
// child process spawned by this shell. That's POSIX-correct
// behavior — the RC file is exactly where users expect to set it.
func applyUmask(mask int) {
	_ = syscall.Umask(mask)
}
