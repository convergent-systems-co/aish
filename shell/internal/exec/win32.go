//go:build windows

// Package exec — Windows-only wrappers around the subset of Win32
// APIs the v1.0-2 built-ins need. We deliberately layer on top of
// `golang.org/x/sys/windows` (pure-Go syscall trampoline, no CGO)
// to preserve aish's no-CGO build promise.
//
// Everything here is a thin shape-converter: the syscall returns raw
// Win32 structs; this layer projects them into the small typed
// records the built-ins consume. We do NOT hide errors — every call
// surfaces the underlying Win32 errno via the returned error.
package exec

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrUnsupported keeps the same identity as the !windows stub so a
// caller switching on `errors.Is(err, exec.ErrUnsupported)` works on
// every host. On Windows we use it for explicit "not implemented in
// this MVP" branches inside otherwise-Windows paths.
var ErrUnsupported = errors.New("win32: not supported on " + runtime.GOOS)

// ProcessEntry — see win32_other.go for the contract.
type ProcessEntry struct {
	PID       uint32
	ParentPID uint32
	Name      string
}

// ServiceEntry — see win32_other.go for the contract.
type ServiceEntry struct {
	Name        string
	DisplayName string
	Status      string
	StartType   string
}

// NetworkInterface — see win32_other.go for the contract.
type NetworkInterface struct {
	Name        string
	MAC         string
	IPv4        string
	Operational bool
}

// ListProcesses walks the running-process snapshot via toolhelp.
func ListProcesses() ([]ProcessEntry, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, fmt.Errorf("Process32First: %w", err)
	}
	out := []ProcessEntry{}
	for {
		out = append(out, ProcessEntry{
			PID:       entry.ProcessID,
			ParentPID: entry.ParentProcessID,
			Name:      utf16ZToString(entry.ExeFile[:]),
		})
		if err := windows.Process32Next(snap, &entry); err != nil {
			// ERROR_NO_MORE_FILES marks the end of the snapshot —
			// not an error condition.
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return out, nil
			}
			return out, fmt.Errorf("Process32Next: %w", err)
		}
	}
}

// KillProcess terminates pid via OpenProcess + TerminateProcess.
// Exit code 1 — the conventional "killed externally" marker.
func KillProcess(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("TerminateProcess(%d): %w", pid, err)
	}
	return nil
}

// ListServices enumerates every service registered with the SCM,
// regardless of current state, projecting each into ServiceEntry.
func ListServices() ([]ServiceEntry, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE|windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, fmt.Errorf("OpenSCManager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)
	// First call: discover required buffer size.
	var bytesNeeded, servicesReturned, resumeHandle uint32
	err = windows.EnumServicesStatusEx(
		scm,
		windows.SC_ENUM_PROCESS_INFO,
		windows.SERVICE_WIN32,
		windows.SERVICE_STATE_ALL,
		nil, 0,
		&bytesNeeded, &servicesReturned, &resumeHandle, nil,
	)
	if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
		return nil, fmt.Errorf("EnumServicesStatusEx(size): %w", err)
	}
	if bytesNeeded == 0 {
		return nil, nil
	}
	buf := make([]byte, bytesNeeded)
	err = windows.EnumServicesStatusEx(
		scm,
		windows.SC_ENUM_PROCESS_INFO,
		windows.SERVICE_WIN32,
		windows.SERVICE_STATE_ALL,
		&buf[0], bytesNeeded,
		&bytesNeeded, &servicesReturned, &resumeHandle, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("EnumServicesStatusEx: %w", err)
	}
	stride := unsafe.Sizeof(windows.ENUM_SERVICE_STATUS_PROCESS{})
	out := make([]ServiceEntry, 0, servicesReturned)
	for i := uint32(0); i < servicesReturned; i++ {
		raw := (*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[uintptr(i)*stride]))
		out = append(out, ServiceEntry{
			Name:        windows.UTF16PtrToString(raw.ServiceName),
			DisplayName: windows.UTF16PtrToString(raw.DisplayName),
			Status:      statusName(raw.ServiceStatusProcess.CurrentState),
			// StartType requires a per-service QueryServiceConfig
			// call; leave empty in `list` to avoid N round-trips.
			// ServiceStatus() returns the full record for the
			// detail view.
			StartType: "",
		})
	}
	return out, nil
}

// ServiceStatus queries one service by name, returning the full
// detail record (status + start type + display name).
func ServiceStatus(name string) (ServiceEntry, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return ServiceEntry{}, fmt.Errorf("OpenSCManager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)
	svcName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return ServiceEntry{}, fmt.Errorf("UTF16PtrFromString(%q): %w", name, err)
	}
	svc, err := windows.OpenService(scm, svcName, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return ServiceEntry{}, fmt.Errorf("OpenService(%s): %w", name, err)
	}
	defer windows.CloseServiceHandle(svc)
	var status windows.SERVICE_STATUS_PROCESS
	var bytesNeeded uint32
	err = windows.QueryServiceStatusEx(
		svc,
		windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&status)),
		uint32(unsafe.Sizeof(status)),
		&bytesNeeded,
	)
	if err != nil {
		return ServiceEntry{}, fmt.Errorf("QueryServiceStatusEx(%s): %w", name, err)
	}
	cfg, startType, err := queryServiceConfig(svc)
	if err != nil {
		// Config failure is non-fatal — fall back to "unknown".
		return ServiceEntry{
			Name:        name,
			DisplayName: name,
			Status:      statusName(status.CurrentState),
			StartType:   "unknown",
		}, nil
	}
	return ServiceEntry{
		Name:        name,
		DisplayName: cfg,
		Status:      statusName(status.CurrentState),
		StartType:   startType,
	}, nil
}

// queryServiceConfig fetches the display name + start type for one
// SCM handle. Two-call pattern: first to size the buffer, second to
// read it.
func queryServiceConfig(svc windows.Handle) (string, string, error) {
	var bytesNeeded uint32
	err := windows.QueryServiceConfig(svc, nil, 0, &bytesNeeded)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", "", err
	}
	if bytesNeeded == 0 {
		return "", "", errors.New("QueryServiceConfig: zero-byte buffer requested")
	}
	buf := make([]byte, bytesNeeded)
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	if err := windows.QueryServiceConfig(svc, cfg, bytesNeeded, &bytesNeeded); err != nil {
		return "", "", err
	}
	return windows.UTF16PtrToString(cfg.DisplayName), startTypeName(cfg.StartType), nil
}

// StartService starts a service by name. Wraps OpenSCManager +
// OpenService + StartService into one call.
func StartService(name string) error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("OpenSCManager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)
	svcName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("UTF16PtrFromString(%q): %w", name, err)
	}
	svc, err := windows.OpenService(scm, svcName, windows.SERVICE_START)
	if err != nil {
		return fmt.Errorf("OpenService(%s): %w", name, err)
	}
	defer windows.CloseServiceHandle(svc)
	if err := windows.StartService(svc, 0, nil); err != nil {
		return fmt.Errorf("StartService(%s): %w", name, err)
	}
	return nil
}

// StopService stops a service by name via SERVICE_CONTROL_STOP.
func StopService(name string) error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("OpenSCManager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)
	svcName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("UTF16PtrFromString(%q): %w", name, err)
	}
	svc, err := windows.OpenService(scm, svcName, windows.SERVICE_STOP)
	if err != nil {
		return fmt.Errorf("OpenService(%s): %w", name, err)
	}
	defer windows.CloseServiceHandle(svc)
	var status windows.SERVICE_STATUS
	if err := windows.ControlService(svc, windows.SERVICE_CONTROL_STOP, &status); err != nil {
		return fmt.Errorf("ControlService(%s, STOP): %w", name, err)
	}
	return nil
}

// ListNetworkInterfaces returns adapter address info — name + MAC +
// first IPv4 + operational flag. Two-call sizing pattern matches
// EnumServicesStatusEx.
func ListNetworkInterfaces() ([]NetworkInterface, error) {
	const flags = windows.GAA_FLAG_INCLUDE_PREFIX
	var size uint32 = 15000 // documented starting size; will grow on demand
	var buf []byte
	for tries := 0; tries < 4; tries++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(syscall.AF_UNSPEC, flags, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
		// loop: `size` now holds the required size
	}
	out := []NetworkInterface{}
	for adapter := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); adapter != nil; adapter = adapter.Next {
		ni := NetworkInterface{
			Name:        windows.UTF16PtrToString(adapter.FriendlyName),
			MAC:         formatMAC(adapter.PhysicalAddress[:adapter.PhysicalAddressLength]),
			Operational: adapter.OperStatus == windows.IfOperStatusUp,
		}
		if u := adapter.FirstUnicastAddress; u != nil {
			if ip := u.Address.IP(); ip != nil {
				if v4 := ip.To4(); v4 != nil {
					ni.IPv4 = v4.String()
				}
			}
		}
		out = append(out, ni)
	}
	return out, nil
}

// utf16ZToString converts a null-terminated UTF-16 slice
// (ExeFile-style buffer) to a Go string.
func utf16ZToString(buf []uint16) string {
	for i, v := range buf {
		if v == 0 {
			return string(utf16ToRunes(buf[:i]))
		}
	}
	return string(utf16ToRunes(buf))
}

func utf16ToRunes(buf []uint16) []rune {
	// syscall.UTF16ToString does the same thing — wrap it to keep
	// the import surface minimal and to avoid surprises if the
	// stdlib helper changes shape.
	return []rune(syscall.UTF16ToString(buf))
}

// formatMAC converts a 6-byte (or shorter) MAC address byte slice
// into colon-separated hex.
func formatMAC(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return net.HardwareAddr(b).String()
}

// statusName maps the Win32 SERVICE_STATUS.CurrentState enum to a
// short, human-readable label suitable for one-line output.
func statusName(state uint32) string {
	switch state {
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "start-pending"
	case windows.SERVICE_STOP_PENDING:
		return "stop-pending"
	case windows.SERVICE_PAUSED:
		return "paused"
	case windows.SERVICE_PAUSE_PENDING:
		return "pause-pending"
	case windows.SERVICE_CONTINUE_PENDING:
		return "continue-pending"
	default:
		return "unknown"
	}
}

// startTypeName maps the QUERY_SERVICE_CONFIG.StartType enum to a
// short label. Used by `aish service status <name>`.
func startTypeName(t uint32) string {
	switch t {
	case windows.SERVICE_AUTO_START:
		return "auto"
	case windows.SERVICE_DEMAND_START:
		return "manual"
	case windows.SERVICE_DISABLED:
		return "disabled"
	case windows.SERVICE_BOOT_START:
		return "boot"
	case windows.SERVICE_SYSTEM_START:
		return "system"
	default:
		return "unknown"
	}
}
