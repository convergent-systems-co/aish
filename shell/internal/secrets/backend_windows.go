//go:build windows

// backend_windows.go implements the Windows-native secrets backend
// behind the cross-platform Backend interface. Two Windows primitives
// cooperate:
//
//   - DPAPI (`CryptProtectData` / `CryptUnprotectData`) wraps each
//     value with a key derived from the current logon session. A
//     ciphertext is unreadable by other users on the same machine and
//     unreadable to the same user on a different machine (unless
//     DPAPI roaming is configured).
//
//   - Credential Manager (`CredWrite` / `CredRead` / `CredDelete` /
//     `CredEnumerate`) stores the DPAPI ciphertext under a target
//     name. Entries are visible in Control Panel → Credential Manager
//     → Windows Credentials → Generic Credentials, which makes the
//     surface auditable by the user. Credential Manager itself
//     encrypts at rest with LSA secrets; the DPAPI wrap is
//     defense-in-depth against a same-user process that calls
//     CredEnumerate + CredRead directly.
//
// This file uses `golang.org/x/sys/windows` for DPAPI (already bound)
// and a thin LazyDLL binding for Credential Manager (which x/sys
// does not expose at v0.44.0). No CGO is required — every Win32
// call goes through the standard syscall machinery.
package secrets

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 constants. Mirrored from wincred.h. Defining them locally
// rather than depending on a constants-only package keeps the
// import surface minimal.
const (
	credTypeGeneric          uint32 = 0x1
	credPersistLocalMachine  uint32 = 0x2
	credPersistEnterprise    uint32 = 0x3
	credMaxCredentialBlobLen        = 5 * 512 // 2.5 KiB historical; 5 KiB modern Windows
)

// Win32 syscall bindings. advapi32 holds the Credential Manager API;
// crypt32 holds DPAPI (already covered by x/sys/windows.CryptProtectData).
var (
	modadvapi32        = windows.NewLazySystemDLL("advapi32.dll")
	modkernel32        = windows.NewLazySystemDLL("kernel32.dll")
	procCredWriteW     = modadvapi32.NewProc("CredWriteW")
	procCredReadW      = modadvapi32.NewProc("CredReadW")
	procCredDeleteW    = modadvapi32.NewProc("CredDeleteW")
	procCredEnumerateW = modadvapi32.NewProc("CredEnumerateW")
	procCredFree       = modadvapi32.NewProc("CredFree")
	procLocalFree      = modkernel32.NewProc("LocalFree")
)

// errorNotFound mirrors ERROR_NOT_FOUND (1168) from winerror.h. The
// Credential Manager APIs surface it when a target name doesn't
// exist. We map it to the package's ErrNotFound sentinel so callers
// using errors.Is can branch on it the same way they do for the
// local Vault.
const errorNotFound windows.Errno = 1168

// credential mirrors the CREDENTIAL struct from wincred.h. Field
// order and types MUST match the Win32 layout exactly — this is
// passed directly to CredWriteW as a packed struct.
//
// Reference: https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr // *credentialAttribute (unused this backend)
	TargetAlias        *uint16
	UserName           *uint16
}

// windowsBackend implements Backend against Credential Manager +
// DPAPI. Stateless apart from the target-name prefix and an optional
// DPAPI entropy buffer that further partitions the keyspace (two
// aish identities can use the same backend without one being able to
// decrypt the other's blobs if the entropy bytes differ).
type windowsBackend struct {
	prefix  string // applied to every target name; default "aish:"
	entropy []byte // optional DPAPI optional-entropy; may be nil
	mu      sync.Mutex
	closed  bool
}

// OpenWindowsBackend returns a Backend that stores secrets in
// Credential Manager, with each value DPAPI-wrapped under the
// current user's logon session key.
//
//   - prefix:  string prepended to every target name so multiple
//     tools sharing Credential Manager can coexist. If empty, "aish:"
//     is used. The List() and Has() methods strip this prefix from
//     user-facing names.
//
//   - entropy: optional bytes mixed into the DPAPI key derivation.
//     If nil, DPAPI uses only the logon-session key. Two backends
//     opened with different entropy cannot read each other's blobs.
//     Useful when an aish "identity" wants its own keyspace.
func OpenWindowsBackend(prefix string, entropy []byte) (Backend, error) {
	if prefix == "" {
		prefix = "aish:"
	}
	// Ensure advapi32 and the procs we need actually resolved.
	// LazyDLL.NewProc never fails at this point, but Find does — and
	// the lazy load happens on first call. We force-load now so a
	// missing DLL surfaces at OpenWindowsBackend time, not on Set.
	if err := modadvapi32.Load(); err != nil {
		return nil, fmt.Errorf("secrets: load advapi32.dll: %w", err)
	}
	for _, p := range []*windows.LazyProc{procCredWriteW, procCredReadW, procCredDeleteW, procCredEnumerateW, procCredFree} {
		if err := p.Find(); err != nil {
			return nil, fmt.Errorf("secrets: resolve %s: %w", p.Name, err)
		}
	}
	// Copy entropy defensively so the caller can mutate or zero
	// their buffer without affecting our DPAPI calls.
	var ent []byte
	if len(entropy) > 0 {
		ent = make([]byte, len(entropy))
		copy(ent, entropy)
	}
	return &windowsBackend{prefix: prefix, entropy: ent}, nil
}

// Set DPAPI-wraps value and writes it to Credential Manager under
// prefix+name. Overwrites any existing entry.
func (b *windowsBackend) Set(name string, value []byte) error {
	if err := b.guard(name); err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("secrets: empty value")
	}
	blob, err := dpapiProtect(value, b.entropy)
	if err != nil {
		return err
	}
	defer Zero(blob)

	if len(blob) > credMaxCredentialBlobLen {
		return fmt.Errorf("secrets: protected value too large (%d bytes; Credential Manager max %d)", len(blob), credMaxCredentialBlobLen)
	}

	target, err := windows.UTF16PtrFromString(b.prefix + name)
	if err != nil {
		return fmt.Errorf("secrets: utf16 target: %w", err)
	}
	// UserName is required by some Credential Manager UIs; "aish"
	// is descriptive and not secret.
	user, err := windows.UTF16PtrFromString("aish")
	if err != nil {
		return fmt.Errorf("secrets: utf16 user: %w", err)
	}

	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	r, _, e := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		return fmt.Errorf("secrets: CredWriteW: %w", e)
	}
	return nil
}

// Get reads the DPAPI ciphertext from Credential Manager and
// unwraps it. Returns ErrNotFound for ERROR_NOT_FOUND so the
// built-in dispatch can render a clean "not found" message.
func (b *windowsBackend) Get(name string) ([]byte, error) {
	if err := b.guard(name); err != nil {
		return nil, err
	}
	target, err := windows.UTF16PtrFromString(b.prefix + name)
	if err != nil {
		return nil, fmt.Errorf("secrets: utf16 target: %w", err)
	}
	var credPtr *credential
	r, _, e := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&credPtr)),
	)
	if r == 0 {
		if errno, ok := e.(windows.Errno); ok && errno == errorNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("secrets: CredReadW: %w", e)
	}
	defer credFree(unsafe.Pointer(credPtr))

	// Copy the blob into a Go-owned slice BEFORE we free the
	// Win32 buffer.
	n := int(credPtr.CredentialBlobSize)
	if n == 0 || credPtr.CredentialBlob == nil {
		return nil, ErrDecrypt
	}
	src := unsafe.Slice(credPtr.CredentialBlob, n)
	blob := make([]byte, n)
	copy(blob, src)
	defer Zero(blob)

	pt, err := dpapiUnprotect(blob, b.entropy)
	if err != nil {
		return nil, err
	}
	return pt, nil
}

// Rm deletes the named entry. Returns ErrNotFound for missing names.
func (b *windowsBackend) Rm(name string) error {
	if err := b.guard(name); err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(b.prefix + name)
	if err != nil {
		return fmt.Errorf("secrets: utf16 target: %w", err)
	}
	r, _, e := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
	)
	if r == 0 {
		if errno, ok := e.(windows.Errno); ok && errno == errorNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("secrets: CredDeleteW: %w", e)
	}
	return nil
}

// List enumerates every Credential Manager entry matching prefix+"*",
// strips the prefix, and returns the short names sorted.
func (b *windowsBackend) List() ([]string, error) {
	if err := b.checkClosed(); err != nil {
		return nil, err
	}
	filter, err := windows.UTF16PtrFromString(b.prefix + "*")
	if err != nil {
		return nil, fmt.Errorf("secrets: utf16 filter: %w", err)
	}
	var count uint32
	var arr **credential // pointer to array of *credential
	r, _, e := procCredEnumerateW.Call(
		uintptr(unsafe.Pointer(filter)),
		0,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&arr)),
	)
	if r == 0 {
		if errno, ok := e.(windows.Errno); ok && errno == errorNotFound {
			// No entries match — return an empty list, not an error.
			return []string{}, nil
		}
		return nil, fmt.Errorf("secrets: CredEnumerateW: %w", e)
	}
	defer credFree(unsafe.Pointer(arr))

	names := make([]string, 0, count)
	if count > 0 && arr != nil {
		slice := unsafe.Slice(arr, int(count))
		for _, c := range slice {
			if c == nil || c.TargetName == nil {
				continue
			}
			full := windows.UTF16PtrToString(c.TargetName)
			if !strings.HasPrefix(full, b.prefix) {
				continue
			}
			names = append(names, strings.TrimPrefix(full, b.prefix))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Has reports whether the named entry exists. Implementation: a
// CredReadW + ERROR_NOT_FOUND check. This costs one read but avoids
// hand-rolling a separate enumeration filter just for membership.
func (b *windowsBackend) Has(name string) (bool, error) {
	if err := b.guard(name); err != nil {
		return false, err
	}
	target, err := windows.UTF16PtrFromString(b.prefix + name)
	if err != nil {
		return false, fmt.Errorf("secrets: utf16 target: %w", err)
	}
	var credPtr *credential
	r, _, e := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&credPtr)),
	)
	if r == 0 {
		if errno, ok := e.(windows.Errno); ok && errno == errorNotFound {
			return false, nil
		}
		return false, fmt.Errorf("secrets: CredReadW: %w", e)
	}
	credFree(unsafe.Pointer(credPtr))
	return true, nil
}

// Close zeroes the entropy buffer and marks the backend unusable.
// Idempotent.
func (b *windowsBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	if len(b.entropy) > 0 {
		Zero(b.entropy)
		b.entropy = nil
	}
	b.closed = true
	return nil
}

// guard validates the name and the backend state. Centralized so
// every public method gets the same checks in the same order.
func (b *windowsBackend) guard(name string) error {
	if err := b.checkClosed(); err != nil {
		return err
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("secrets: invalid name %q (want %s)", name, nameRe.String())
	}
	return nil
}

func (b *windowsBackend) checkClosed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("secrets: backend closed")
	}
	return nil
}

// dpapiProtect wraps plaintext with CryptProtectData under the
// current user's logon session key. entropy (may be nil) is mixed
// into the key derivation; the same entropy MUST be passed to
// dpapiUnprotect.
//
// CRYPTPROTECT_UI_FORBIDDEN suppresses the SmartCard prompt path so
// a headless shell never blocks on a GUI dialog.
func dpapiProtect(plaintext, entropy []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("secrets: empty plaintext")
	}
	in := dataBlobFor(plaintext)
	var entBlob *windows.DataBlob
	if len(entropy) > 0 {
		eb := dataBlobFor(entropy)
		entBlob = &eb
	}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, entBlob, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("secrets: CryptProtectData: %w", err)
	}
	defer localFree(unsafe.Pointer(out.Data))
	if out.Size == 0 || out.Data == nil {
		return nil, errors.New("secrets: CryptProtectData returned empty blob")
	}
	buf := make([]byte, out.Size)
	copy(buf, unsafe.Slice(out.Data, int(out.Size)))
	return buf, nil
}

// dpapiUnprotect reverses dpapiProtect. Wrong entropy (or a blob
// produced for a different user / machine) returns ErrDecrypt — the
// underlying Win32 error is suppressed so callers cannot distinguish
// "wrong key" from "tampered ciphertext" (no timing-leak surface).
func dpapiUnprotect(ciphertext, entropy []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, ErrDecrypt
	}
	in := dataBlobFor(ciphertext)
	var entBlob *windows.DataBlob
	if len(entropy) > 0 {
		eb := dataBlobFor(entropy)
		entBlob = &eb
	}
	var out windows.DataBlob
	var namePtr *uint16
	if err := windows.CryptUnprotectData(&in, &namePtr, entBlob, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		_ = err // intentionally not surfaced — see ErrDecrypt comment
		return nil, ErrDecrypt
	}
	defer localFree(unsafe.Pointer(out.Data))
	if namePtr != nil {
		localFree(unsafe.Pointer(namePtr))
	}
	if out.Size == 0 || out.Data == nil {
		return nil, ErrDecrypt
	}
	buf := make([]byte, out.Size)
	copy(buf, unsafe.Slice(out.Data, int(out.Size)))
	return buf, nil
}

// dataBlobFor builds a Win32 DATA_BLOB pointing at buf. The caller
// MUST keep buf alive for the duration of the syscall — DataBlob
// does NOT copy.
func dataBlobFor(buf []byte) windows.DataBlob {
	if len(buf) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{
		Size: uint32(len(buf)),
		Data: &buf[0],
	}
}

// credFree wraps CredFree, which the Credential Manager API uses to
// release buffers returned by CredReadW and CredEnumerateW.
// Forgetting this leaks process memory per credential operation.
func credFree(p unsafe.Pointer) {
	if p == nil {
		return
	}
	_, _, _ = procCredFree.Call(uintptr(p))
}

// localFree wraps kernel32.LocalFree, used to release buffers
// returned by CryptProtectData / CryptUnprotectData via the Out
// DataBlob and the optional Name pointer.
func localFree(p unsafe.Pointer) {
	if p == nil {
		return
	}
	_, _, _ = procLocalFree.Call(uintptr(p))
}
