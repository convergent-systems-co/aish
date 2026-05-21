//go:build !windows

package exec

import (
	"errors"
	"testing"
)

// TestWin32Stubs verifies the non-Windows compile shim returns
// ErrUnsupported from every entry point. The error identity is
// load-bearing — built-ins use errors.Is(err, exec.ErrUnsupported)
// to decide "polite message" vs "real failure".
func TestWin32Stubs(t *testing.T) {
	if _, err := ListProcesses(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ListProcesses err = %v, want ErrUnsupported", err)
	}
	if err := KillProcess(1); !errors.Is(err, ErrUnsupported) {
		t.Errorf("KillProcess err = %v, want ErrUnsupported", err)
	}
	if _, err := ListServices(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ListServices err = %v, want ErrUnsupported", err)
	}
	if _, err := ServiceStatus("Spooler"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ServiceStatus err = %v, want ErrUnsupported", err)
	}
	if err := StartService("Spooler"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("StartService err = %v, want ErrUnsupported", err)
	}
	if err := StopService("Spooler"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("StopService err = %v, want ErrUnsupported", err)
	}
	if _, err := ListNetworkInterfaces(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ListNetworkInterfaces err = %v, want ErrUnsupported", err)
	}
}
