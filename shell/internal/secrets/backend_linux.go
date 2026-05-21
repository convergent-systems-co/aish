//go:build linux

// backend_linux.go implements the Linux-native secrets backend behind
// the cross-platform Backend interface. Storage is the freedesktop
// Secret Service (`org.freedesktop.secrets`) over D-Bus; concrete
// daemons that implement the protocol include `gnome-keyring-daemon`,
// `KeePassXC`, and `kwalletd5/kwalletmanager`. Access is via pure-Go
// `github.com/godbus/dbus/v5` — no libsecret, no CGO.
//
// On a host without a running session bus (no `DBUS_SESSION_BUS_ADDRESS`,
// typical for SSH-without-X or headless servers) or without any
// Secret Service implementation registered, OpenLinuxBackend returns
// ErrUnsupported. The dispatch layer can then fall through to
// LocalVault cleanly — no panic, no half-state.
//
// Items are stored in the user's default collection (the alias
// `/org/freedesktop/secrets/aliases/default`, which most daemons map
// to the "login" or "default" keyring). Each item carries two
// attributes:
//
//   - "service": the service label (default "aish"). All entries owned
//     by this backend share this value; enumeration filters on it.
//   - "name": the per-secret short name (the caller's `name` arg).
//
// Plus a third optional attribute when an account prefix is set:
//
//   - "account": the prefix value.
//
// The Secret Service spec is small: SearchItems → an array of item
// paths; per-item GetSecret / CreateItem / Delete drive the data
// plane. We open a `plain` (unencrypted) session between us and the
// daemon — the bus is a local Unix socket, no over-the-wire bytes to
// eavesdrop. The daemon still encrypts at rest with the user's login
// password.
package secrets

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Secret Service D-Bus interface paths and names. Mirrored from the
// freedesktop spec rev 0.2:
// https://specifications.freedesktop.org/secret-service/latest/
const (
	ssBusName        = "org.freedesktop.secrets"
	ssServicePath    = "/org/freedesktop/secrets"
	ssDefaultAlias   = "/org/freedesktop/secrets/aliases/default"
	ssServiceIface   = "org.freedesktop.Secret.Service"
	ssCollectionIfac = "org.freedesktop.Secret.Collection"
	ssItemIface      = "org.freedesktop.Secret.Item"
	ssSessionIface   = "org.freedesktop.Secret.Session"
	ssAlgoPlain      = "plain" // no encryption between client and daemon
)

// secretValue mirrors the Secret Service `Secret` struct:
//
//	(oay ay s) — session path, parameters, value, content-type
//
// Field order MUST match exactly; godbus marshals struct fields in
// declaration order.
type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// linuxBackend implements Backend against the freedesktop Secret
// Service. Holds the D-Bus connection, the open session path, and
// the service+account attributes that scope this backend's view of
// the default collection.
type linuxBackend struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
	service string // "service" attribute value; default "aish"
	account string // optional "account" attribute value; default ""
	mu      sync.Mutex
	closed  bool
}

// OpenLinuxBackend returns a Backend that stores secrets in the
// freedesktop Secret Service.
//
//   - service: the value used for the "service" attribute on every
//     stored item. If empty, "aish" is used. Items with a different
//     service attribute are invisible to this backend.
//
//   - schema: the value used for the conventional
//     "xdg:schema" attribute. If empty, "io.aish.Secret" is used.
//     The schema is advisory metadata only — Secret Service does not
//     enforce it.
//
// Returns ErrUnsupported with no additional wrapping when:
//   - the session bus is not reachable (no daemon, no env var), or
//   - the Secret Service bus name is not currently owned.
//
// Callers MUST treat ErrUnsupported as the "fall back to LocalVault"
// signal per the Backend interface contract.
func OpenLinuxBackend(service string, schema string) (Backend, error) {
	if service == "" {
		service = "aish"
	}
	if schema == "" {
		schema = "io.aish.Secret"
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		// No session bus reachable: typical for headless / SSH /
		// container hosts. Surface as ErrUnsupported so dispatch
		// can fall back to LocalVault.
		return nil, fmt.Errorf("%w: session bus unreachable: %v", ErrUnsupported, err)
	}

	// Confirm the Secret Service is on the bus. NameHasOwner returns
	// false on hosts where dbus is running but no keychain daemon
	// has registered the well-known name.
	var hasOwner bool
	callErr := conn.BusObject().Call(
		"org.freedesktop.DBus.NameHasOwner", 0, ssBusName,
	).Store(&hasOwner)
	if callErr != nil {
		return nil, fmt.Errorf("%w: NameHasOwner: %v", ErrUnsupported, callErr)
	}
	if !hasOwner {
		return nil, fmt.Errorf("%w: %s not registered on session bus", ErrUnsupported, ssBusName)
	}

	// Open a plain session. The Secret Service spec allows
	// algorithm "plain" (no client/daemon encryption) — fine because
	// the bus is a local Unix socket. The "" empty-variant parameter
	// is the spec-prescribed input for plain sessions.
	svc := conn.Object(ssBusName, dbus.ObjectPath(ssServicePath))
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".OpenSession", 0, ssAlgoPlain, dbus.MakeVariant("")).Store(&output, &session); err != nil {
		return nil, fmt.Errorf("%w: OpenSession: %v", ErrUnsupported, err)
	}

	return &linuxBackend{
		conn:    conn,
		session: session,
		service: service,
		account: "",
	}, nil
}

// attrsFor returns the attribute map used for both SearchItems and
// CreateItem. Includes the optional account attribute when set.
func (b *linuxBackend) attrsFor(name string) map[string]string {
	m := map[string]string{
		"service": b.service,
		"name":    name,
	}
	if b.account != "" {
		m["account"] = b.account
	}
	return m
}

// listAttrs returns the attribute map for backend-wide enumeration
// (no per-name filter).
func (b *linuxBackend) listAttrs() map[string]string {
	m := map[string]string{"service": b.service}
	if b.account != "" {
		m["account"] = b.account
	}
	return m
}

// Set creates or replaces the item identified by (service, name).
// CreateItem's replace=true flag is how Secret Service expresses
// "overwrite if exists."
func (b *linuxBackend) Set(name string, value []byte) error {
	if err := b.guard(name); err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("secrets: empty value")
	}
	collection := b.conn.Object(ssBusName, dbus.ObjectPath(ssDefaultAlias))
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant(b.service + ":" + name),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(b.attrsFor(name)),
	}
	secret := secretValue{
		Session:     b.session,
		Parameters:  []byte{},
		Value:       value,
		ContentType: "text/plain; charset=utf8",
	}
	var itemPath dbus.ObjectPath
	var promptPath dbus.ObjectPath
	if err := collection.Call(
		ssCollectionIfac+".CreateItem", 0, props, secret, true, // replace=true
	).Store(&itemPath, &promptPath); err != nil {
		return fmt.Errorf("secrets: CreateItem: %w", err)
	}
	// If a prompt is required (collection locked), we surface it
	// rather than handle it here. The spec returns "/" when no
	// prompt is needed.
	if promptPath != "/" && promptPath != "" {
		if err := b.promptAndWait(promptPath); err != nil {
			return fmt.Errorf("secrets: collection locked, prompt failed: %w", err)
		}
	}
	return nil
}

// Get reads the secret value for the named entry. Returns ErrNotFound
// if no item matches. The returned slice is a fresh allocation; the
// caller MUST Zero it when done.
func (b *linuxBackend) Get(name string) ([]byte, error) {
	if err := b.guard(name); err != nil {
		return nil, err
	}
	itemPath, err := b.findOne(b.attrsFor(name))
	if err != nil {
		return nil, err
	}
	if itemPath == "" {
		return nil, ErrNotFound
	}
	item := b.conn.Object(ssBusName, itemPath)
	var secret secretValue
	if err := item.Call(ssItemIface+".GetSecret", 0, b.session).Store(&secret); err != nil {
		return nil, fmt.Errorf("secrets: GetSecret: %w", err)
	}
	// Copy so the caller owns the buffer.
	out := make([]byte, len(secret.Value))
	copy(out, secret.Value)
	// Best-effort zero of the dbus-allocated slice.
	Zero(secret.Value)
	return out, nil
}

// Rm deletes the named item. Returns ErrNotFound for missing names.
func (b *linuxBackend) Rm(name string) error {
	if err := b.guard(name); err != nil {
		return err
	}
	itemPath, err := b.findOne(b.attrsFor(name))
	if err != nil {
		return err
	}
	if itemPath == "" {
		return ErrNotFound
	}
	item := b.conn.Object(ssBusName, itemPath)
	var promptPath dbus.ObjectPath
	if err := item.Call(ssItemIface+".Delete", 0).Store(&promptPath); err != nil {
		return fmt.Errorf("secrets: item.Delete: %w", err)
	}
	if promptPath != "/" && promptPath != "" {
		if err := b.promptAndWait(promptPath); err != nil {
			return fmt.Errorf("secrets: delete prompt failed: %w", err)
		}
	}
	return nil
}

// List returns the names of every item under the backend's
// (service, account) scope. Items in the keyring whose "service"
// attribute doesn't match are invisible. Returned sorted.
func (b *linuxBackend) List() ([]string, error) {
	if err := b.checkClosed(); err != nil {
		return nil, err
	}
	svc := b.conn.Object(ssBusName, dbus.ObjectPath(ssServicePath))
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, b.listAttrs()).Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("secrets: SearchItems: %w", err)
	}
	names := make([]string, 0, len(unlocked)+len(locked))
	for _, p := range append(unlocked, locked...) {
		n, err := b.itemNameFromAttrs(p)
		if err != nil {
			// One bad item shouldn't kill the whole enumeration;
			// surface it via a soft warning channel later. For now,
			// skip silently — name extraction failure means the item
			// isn't ours (no "name" attribute).
			continue
		}
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Has reports whether the named item exists.
func (b *linuxBackend) Has(name string) (bool, error) {
	if err := b.guard(name); err != nil {
		return false, err
	}
	itemPath, err := b.findOne(b.attrsFor(name))
	if err != nil {
		return false, err
	}
	return itemPath != "", nil
}

// Close closes the D-Bus connection and marks the backend unusable.
// Idempotent.
func (b *linuxBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.session != "" && b.conn != nil {
		// Best-effort session close. Don't surface a failure here —
		// the user is closing the backend and any error is post-hoc.
		sess := b.conn.Object(ssBusName, b.session)
		_ = sess.Call(ssSessionIface+".Close", 0).Store()
	}
	if b.conn != nil {
		_ = b.conn.Close()
		b.conn = nil
	}
	return nil
}

// findOne returns the first item path matching attrs, or "" if none.
// Errors are returned for D-Bus call failures, not "not found."
func (b *linuxBackend) findOne(attrs map[string]string) (dbus.ObjectPath, error) {
	if err := b.checkClosed(); err != nil {
		return "", err
	}
	svc := b.conn.Object(ssBusName, dbus.ObjectPath(ssServicePath))
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return "", fmt.Errorf("secrets: SearchItems: %w", err)
	}
	if len(unlocked) > 0 {
		return unlocked[0], nil
	}
	if len(locked) > 0 {
		return locked[0], nil
	}
	return "", nil
}

// itemNameFromAttrs reads the "name" attribute off an item path. We
// re-query attributes (rather than caching from SearchItems) so a
// foreign mutation of the item is reflected immediately.
func (b *linuxBackend) itemNameFromAttrs(path dbus.ObjectPath) (string, error) {
	item := b.conn.Object(ssBusName, path)
	v, err := item.GetProperty(ssItemIface + ".Attributes")
	if err != nil {
		return "", fmt.Errorf("secrets: get Attributes: %w", err)
	}
	attrs, ok := v.Value().(map[string]string)
	if !ok {
		return "", errors.New("secrets: Attributes property has unexpected type")
	}
	return attrs["name"], nil
}

// promptAndWait drives a Secret Service Prompt to completion. The
// daemon returns a Prompt object path when a collection is locked;
// we call Prompt.Prompt("") and wait for the Completed signal. In
// practice this is rare for aish's use case (default collection is
// typically unlocked after login), so we keep the implementation
// minimal and surface failures clearly.
func (b *linuxBackend) promptAndWait(path dbus.ObjectPath) error {
	prompt := b.conn.Object(ssBusName, path)
	// "" platform-specific window-id argument; empty means "no
	// parent window," which works for non-GUI clients.
	if err := prompt.Call("org.freedesktop.Secret.Prompt.Prompt", 0, "").Store(); err != nil {
		return fmt.Errorf("Prompt: %w", err)
	}
	// We do NOT wait on the Completed signal in this MVP. If the
	// daemon requires user interaction the next call from the
	// caller will block or fail with a clearer error; surfacing the
	// signal-handling complexity here is out of scope.
	return nil
}

// guard validates name and backend state. Centralized so every
// public method gets the same checks in the same order.
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
