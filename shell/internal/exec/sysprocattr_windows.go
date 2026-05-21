//go:build windows

// Package exec — Windows process-group stub.
//
// POSIX process-groups have no direct Windows equivalent. Job control
// on Windows uses JobObjects + console-control-event routing, which is
// its own v1.0 follow-up. For now `applyPgroup` is a no-op so the
// shell compiles on `GOOS=windows`; the job-control built-ins surface
// a clear "not supported" error to the user.
package exec

import "os/exec"

func applyPgroup(cmd *exec.Cmd, firstPgid int) {}
