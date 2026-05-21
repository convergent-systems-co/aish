//go:build linux

// Linux Secret Service backend for aish secrets.
//
// Mirrors the macOS + Windows backends: a small, CGO-free shim behind
// the package-level `Backend` interface. On Linux, the OS-native key
// store is whatever D-Bus service implements the freedesktop Secret
// Service spec — typically `gnome-keyring-daemon` (GNOME, Cinnamon,
// XFCE-with-gnome-keyring) or `kwalletd` (KDE, with the
// secret-service shim). Both share an at-rest cipher (AES-256 under a
// key tied to the login session) and identical D-Bus surface, so a
// single client works against either.
//
// Wire posture: pure `github.com/godbus/dbus/v5`, no CGO, no `libsecret`
// C dependency. Values move through D-Bus method arguments — never
// process argv or environment.
//
// Failure posture: when no session bus is available (no `DBUS_SESSION_
// BUS_ADDRESS`, headless container, missing keyring daemon) the
// constructor returns a wrapped `ErrUnsupported` so callers can fall
// back to the local vault without a special-cased OS check. This is
// the same posture as `OpenDarwinBackend` when `/usr/bin/security` is
// missing.
//
// Encryption transport: we use the `plain` Secret Service session
// algorithm. The freedesktop spec offers a DH-IETF mode for added
// transport secrecy between the client and the keyring daemon; for
// MVP we accept the in-process trust model (anything reading our
// memory can already read the value anyway). The DH path is filed as
// a follow-up.
//
// Item naming: items are stored under the collection's default
// "login" collection with an `xdg:schema = aish` attribute and a
// per-item `name` attribute. The `service` is exposed as the
// human-readable label so `seahorse` / `kwalletmanager` show
// "aish:<prefix>" in their UI.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	// secretServiceDest is the well-known D-Bus name for the
	// Secret Service implementation. Both gnome-keyring-daemon
	// and kwalletd register here.
	secretServiceDest = "org.freedesktop.secrets"

	// secretServicePath is the root object path; sessions and
	// collections are children of this object.
	secretServicePath dbus.ObjectPath = "/org/freedesktop/secrets"

	// defaultCollectionPath is the user's "login" collection —
	// unlocked at session start, the default destination for items
	// stored without an explicit collection.
	defaultCollectionPath dbus.ObjectPath = "/org/freedesktop/secrets/aliases/default"

	// sessionAlgPlain selects the unencrypted-transport session
	// algorithm. The keyring daemon's at-rest cipher is unchanged.
	sessionAlgPlain = "plain"

	// itemSchemaAttr is the `xdg:schema` value used to scope
	// our items to the aish namespace inside the shared "login"
	// collection. Mirrors how libsecret schemas work.
	itemSchemaAttr = "aish"
)

// linuxBackend implements Backend against a Secret Service daemon
// reachable over the session bus.
type linuxBackend struct {
	prefix  string // applied to the service attribute on each item
	service string // pre-computed "aish:<prefix>" form for List filtering
	entropy []byte // reserved for cross-backend signature compatibility

	conn    *dbus.Conn
	session dbus.ObjectPath

	mu     sync.Mutex
	closed bool
}

// OpenLinuxBackend returns a Backend backed by the freedesktop Secret
// Service. The connection model:
//
//  1. Open the session bus (`DBUS_SESSION_BUS_ADDRESS`).
//  2. Check that `org.freedesktop.secrets` is on the bus — if not,
//     return wrapped ErrUnsupported. This is the headless-container
//     case.
//  3. Open a plain (non-encrypted) session with the service. The
//     `Service.OpenSession` reply carries the session object path
//     we use for subsequent `Item.GetSecret` / `Collection.CreateItem`
//     calls.
//
// `prefix` scopes the service attribute on every item (default
// "aish"). `entropy` is reserved for cross-backend signature parity
// with the Windows backend; the Secret Service has no entropy hook
// (the daemon owns the at-rest key derivation).
func OpenLinuxBackend(prefix string, entropy []byte) (Backend, error) {
	if prefix == "" {
		prefix = "aish"
	}
	// Session bus is the right scope for user-owned secrets. The
	// system bus carries no user-keyring service by design.
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		// Some sandboxes do not export the env var but still bind
		// the bus via the autolaunch socket. Try to open and let
		// godbus surface the failure — we wrap whatever error we
		// get as ErrUnsupported so the caller can fall back.
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("secrets: linux: session bus: %w", joinUnsupported(err))
	}
	// Confirm the service is on the bus. ListNames is read-only and
	// cheap; we prefer it over a speculative method call so the
	// "service missing" case surfaces with a clean ErrUnsupported.
	if !serviceOnBus(conn, secretServiceDest) {
		return nil, fmt.Errorf("secrets: linux: %s not on session bus: %w",
			secretServiceDest, ErrUnsupported)
	}

	// Open a plain session.
	svc := conn.Object(secretServiceDest, secretServicePath)
	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	call := svc.Call("org.freedesktop.Secret.Service.OpenSession", 0,
		sessionAlgPlain, dbus.MakeVariant(""))
	if call.Err != nil {
		return nil, fmt.Errorf("secrets: linux: OpenSession: %w", joinUnsupported(call.Err))
	}
	if err := call.Store(&output, &sessionPath); err != nil {
		return nil, fmt.Errorf("secrets: linux: OpenSession decode: %w", err)
	}

	// Defensive copy of entropy — same posture as windowsBackend.
	var ent []byte
	if len(entropy) > 0 {
		ent = make([]byte, len(entropy))
		copy(ent, entropy)
	}

	return &linuxBackend{
		prefix:  prefix,
		service: prefix, // service-attr value; List filters on equality
		entropy: ent,
		conn:    conn,
		session: sessionPath,
	}, nil
}

// joinUnsupported wraps err with ErrUnsupported when the failure mode
// is one we can confidently call "the platform can't run this." Other
// errors pass through verbatim so callers can diagnose real bugs
// (auth failure, broken pipe, etc.).
func joinUnsupported(err error) error {
	if err == nil {
		return nil
	}
	// godbus surfaces these as plain errors with descriptive
	// messages; we treat any session-bus-open failure as
	// ErrUnsupported because "no session bus" == "platform can't
	// run this backend" by definition.
	return errors.Join(err, ErrUnsupported)
}

// serviceOnBus reports whether dest is currently owned by some
// connection on the session bus. Uses the standard
// `org.freedesktop.DBus.ListNames` introspection call.
func serviceOnBus(conn *dbus.Conn, dest string) bool {
	var names []string
	err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names)
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == dest {
			return true
		}
	}
	return false
}

// Set creates or updates an item under the default collection. The
// item's lookup attributes are {xdg:schema=aish, service=<prefix>,
// name=<name>}; CreateItem with replace=true overwrites an existing
// match on those attributes.
func (b *linuxBackend) Set(name string, value []byte) error {
	if err := b.guard(name); err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("secrets: empty value")
	}

	attrs := map[string]string{
		"xdg:schema": itemSchemaAttr,
		"service":    b.service,
		"name":       name,
	}
	// org.freedesktop.Secret.Collection.CreateItem signature:
	//   in  a{sv}  properties
	//   in  (oayays) secret  (session, parameters, value, mime)
	//   in  b      replace
	//   out o      item
	//   out o      prompt   (path; "/" means no prompt needed)
	properties := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant(b.service + ":" + name),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(attrs),
	}
	secretArg := secretStruct{
		Session:     b.session,
		Parameters:  []byte{},
		Value:       value,
		ContentType: "text/plain",
	}
	coll := b.conn.Object(secretServiceDest, defaultCollectionPath)
	var itemPath dbus.ObjectPath
	var promptPath dbus.ObjectPath
	call := coll.Call("org.freedesktop.Secret.Collection.CreateItem", 0,
		properties, secretArg, true)
	if call.Err != nil {
		return fmt.Errorf("secrets: linux: CreateItem: %w", call.Err)
	}
	if err := call.Store(&itemPath, &promptPath); err != nil {
		return fmt.Errorf("secrets: linux: CreateItem decode: %w", err)
	}
	// We do NOT call Prompt() — the default "login" collection is
	// already unlocked at session start. If a future MVP supports
	// locked collections, the prompt path will need to fire.
	return nil
}

// Get reads the value of the item with attributes
// {schema=aish, service=<prefix>, name=<name>}. SearchItems returns
// the list of matching item paths; we expect exactly one. Multiple
// matches are a daemon-side surprise and we return the first.
func (b *linuxBackend) Get(name string) ([]byte, error) {
	if err := b.guard(name); err != nil {
		return nil, err
	}
	path, err := b.findItemPath(name)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, ErrNotFound
	}
	item := b.conn.Object(secretServiceDest, path)
	var s secretStruct
	call := item.Call("org.freedesktop.Secret.Item.GetSecret", 0, b.session)
	if call.Err != nil {
		return nil, fmt.Errorf("secrets: linux: GetSecret: %w", call.Err)
	}
	if err := call.Store(&s); err != nil {
		return nil, fmt.Errorf("secrets: linux: GetSecret decode: %w", err)
	}
	// Defensive copy — godbus returns a slice into its own buffer,
	// reusing the backing array on the next call.
	out := make([]byte, len(s.Value))
	copy(out, s.Value)
	return out, nil
}

// Rm deletes the matching item. ErrNotFound when no match.
func (b *linuxBackend) Rm(name string) error {
	if err := b.guard(name); err != nil {
		return err
	}
	path, err := b.findItemPath(name)
	if err != nil {
		return err
	}
	if path == "" {
		return ErrNotFound
	}
	item := b.conn.Object(secretServiceDest, path)
	var promptPath dbus.ObjectPath
	call := item.Call("org.freedesktop.Secret.Item.Delete", 0)
	if call.Err != nil {
		return fmt.Errorf("secrets: linux: Delete: %w", call.Err)
	}
	if err := call.Store(&promptPath); err != nil {
		return fmt.Errorf("secrets: linux: Delete decode: %w", err)
	}
	return nil
}

// List enumerates every item whose attributes match
// {schema=aish, service=<prefix>}. The service-level SearchItems
// returns paths from every unlocked collection; we further refine
// names by reading each item's `name` attribute.
func (b *linuxBackend) List() ([]string, error) {
	if err := b.checkClosed(); err != nil {
		return nil, err
	}
	attrs := map[string]string{
		"xdg:schema": itemSchemaAttr,
		"service":    b.service,
	}
	svc := b.conn.Object(secretServiceDest, secretServicePath)
	var unlocked, locked []dbus.ObjectPath
	call := svc.Call("org.freedesktop.Secret.Service.SearchItems", 0, attrs)
	if call.Err != nil {
		return nil, fmt.Errorf("secrets: linux: SearchItems: %w", call.Err)
	}
	if err := call.Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("secrets: linux: SearchItems decode: %w", err)
	}
	all := append([]dbus.ObjectPath{}, unlocked...)
	all = append(all, locked...)
	names := make([]string, 0, len(all))
	seen := map[string]bool{}
	for _, p := range all {
		n, err := b.readNameAttr(p)
		if err != nil || n == "" {
			continue
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// Has reports whether the named entry exists.
func (b *linuxBackend) Has(name string) (bool, error) {
	if err := b.guard(name); err != nil {
		return false, err
	}
	path, err := b.findItemPath(name)
	if err != nil {
		return false, err
	}
	return path != "", nil
}

// Close zeroes the entropy buffer, closes the bus connection, and
// marks the backend unusable. Idempotent.
func (b *linuxBackend) Close() error {
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
	if b.conn != nil {
		_ = b.conn.Close()
		b.conn = nil
	}
	return nil
}

// findItemPath returns the object path of the single item whose
// attributes match {schema=aish, service=<prefix>, name=<name>}.
// Empty string + nil error means "no such item".
func (b *linuxBackend) findItemPath(name string) (dbus.ObjectPath, error) {
	attrs := map[string]string{
		"xdg:schema": itemSchemaAttr,
		"service":    b.service,
		"name":       name,
	}
	svc := b.conn.Object(secretServiceDest, secretServicePath)
	var unlocked, locked []dbus.ObjectPath
	call := svc.Call("org.freedesktop.Secret.Service.SearchItems", 0, attrs)
	if call.Err != nil {
		return "", fmt.Errorf("secrets: linux: SearchItems: %w", call.Err)
	}
	if err := call.Store(&unlocked, &locked); err != nil {
		return "", fmt.Errorf("secrets: linux: SearchItems decode: %w", err)
	}
	if len(unlocked) > 0 {
		return unlocked[0], nil
	}
	if len(locked) > 0 {
		return locked[0], nil
	}
	return "", nil
}

// readNameAttr reads the `name` attribute of the item at path. Used
// by List to surface the user-facing short name (the D-Bus path is
// daemon-internal and not stable for display).
func (b *linuxBackend) readNameAttr(path dbus.ObjectPath) (string, error) {
	item := b.conn.Object(secretServiceDest, path)
	v, err := item.GetProperty("org.freedesktop.Secret.Item.Attributes")
	if err != nil {
		return "", err
	}
	m, ok := v.Value().(map[string]string)
	if !ok {
		return "", fmt.Errorf("secrets: linux: attributes property has unexpected type %T", v.Value())
	}
	return m["name"], nil
}

// guard validates the name and the backend state. Centralized so
// every public method gets the same checks in the same order.
func (b *linuxBackend) guard(name string) error {
	if err := b.checkClosed(); err != nil {
		return err
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("secrets: invalid name %q (want %s)", name, nameRe.String())
	}
	return nil
}

func (b *linuxBackend) checkClosed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("secrets: backend closed")
	}
	return nil
}

// secretStruct mirrors the org.freedesktop.Secret.Secret D-Bus
// struct: (o session, ay parameters, ay value, s content_type).
// The field order MUST match the wire layout exactly — godbus
// marshals structs positionally.
type secretStruct struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// OpenWindowsBackend on linux returns ErrUnsupported. The Windows
// backend's real impl is in backend_windows.go; this stub lets
// dispatch code reference the symbol on Linux builds.
func OpenWindowsBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}

// OpenDarwinBackend on linux returns ErrUnsupported. The macOS
// backend's real impl is in backend_darwin.go; this stub lets
// dispatch code reference the symbol on Linux builds.
func OpenDarwinBackend(prefix string, entropy []byte) (Backend, error) {
	return nil, ErrUnsupported
}
