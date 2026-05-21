// Package registry implements the install/remove/verify side of the
// v0.3-2 plugin registry. The read-only walk + select logic lives in
// libs/proto/registry (used by both this package and the shell-side
// loader); this package owns only the mutation surface used by the
// `aish-plugin` admin CLI.
//
// Layout produced by Install:
//
//	<root>/
//	  <name>/
//	    manifest.json
//	  .lock           — install-time file lock
//
// Install does NOT copy the binary; the manifest references an
// already-deployed binary by absolute path. Remove deletes the
// manifest directory only.
package registry

import (
	"errors"
	"time"
)

// LockFileName is the install-lock filename inside the registry root.
const LockFileName = ".lock"

// ErrNotFound is returned by Remove when no plugin with the given
// name is installed.
var ErrNotFound = errors.New("registry: plugin not found")

// ErrAlreadyInstalled is returned by Install when a manifest with the
// same name is already on disk and the caller did not pass
// InstallOpts.Force.
var ErrAlreadyInstalled = errors.New("registry: plugin already installed")

// ErrLockBusy is returned by Install when the install lock is held
// by another process and the caller declined to wait.
var ErrLockBusy = errors.New("registry: install lock busy")

// LockTimeout bounds how long Install waits to acquire the registry
// lock. Long enough to absorb a concurrent install of a few plugins;
// short enough that a stale lock from a crashed install surfaces in
// reasonable time.
const LockTimeout = 30 * time.Second
