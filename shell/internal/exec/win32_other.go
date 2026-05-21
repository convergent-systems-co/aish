//go:build !windows

// Package exec — non-Windows stubs for the Win32 wrapper layer
// defined in win32.go. Every exported entry point here returns
// ErrUnsupported so the built-ins that use them compile and run on
// macOS / Linux without a Windows toolchain.
//
// The contract: each function signature here matches its
// build-tagged Windows counterpart exactly. Tests on a non-Windows
// host exercise the "not supported" branch; runtime behavior on
// Windows is validated post-merge from a Windows VM smoke run.
package exec

import (
	"errors"
	"runtime"
)

// ErrUnsupported is returned by every Win32 wrapper when running on
// a non-Windows host. Built-ins surface this verbatim ("not
// supported on <GOOS>") rather than panicking.
var ErrUnsupported = errors.New("win32: not supported on " + runtime.GOOS)

// ProcessEntry captures the columns `aish process list` cares
// about. PID + ParentPID + Name is the MVP set; CPU/memory is a
// v1.1 follow-up.
type ProcessEntry struct {
	PID       uint32
	ParentPID uint32
	Name      string
}

// ServiceEntry captures the columns `aish service list` returns.
// StartType + DisplayName widen the view beyond plain Status so the
// user can disambiguate look-alike services.
type ServiceEntry struct {
	Name        string
	DisplayName string
	Status      string // "running" | "stopped" | "start-pending" | "stop-pending" | "unknown"
	StartType   string // "auto" | "manual" | "disabled" | "unknown"
}

// NetworkInterface is the MVP shape of `aish network interfaces`.
// First IPv4 only — multi-address adapters fall under v1.1.
type NetworkInterface struct {
	Name      string
	MAC       string
	IPv4      string
	Operational bool
}

// ListProcesses returns the running-process snapshot. Non-Windows: empty + ErrUnsupported.
func ListProcesses() ([]ProcessEntry, error) { return nil, ErrUnsupported }

// KillProcess terminates pid. Non-Windows: ErrUnsupported.
func KillProcess(pid uint32) error { return ErrUnsupported }

// ListServices enumerates SCM services. Non-Windows: empty + ErrUnsupported.
func ListServices() ([]ServiceEntry, error) { return nil, ErrUnsupported }

// ServiceStatus queries one service by name. Non-Windows: zero + ErrUnsupported.
func ServiceStatus(name string) (ServiceEntry, error) { return ServiceEntry{}, ErrUnsupported }

// StartService starts a service by name. Non-Windows: ErrUnsupported.
func StartService(name string) error { return ErrUnsupported }

// StopService stops a service by name. Non-Windows: ErrUnsupported.
func StopService(name string) error { return ErrUnsupported }

// ListNetworkInterfaces returns adapter info. Non-Windows: empty + ErrUnsupported.
func ListNetworkInterfaces() ([]NetworkInterface, error) { return nil, ErrUnsupported }
