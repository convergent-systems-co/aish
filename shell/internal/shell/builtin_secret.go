package shell

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// stripPkgPrefix turns "secrets: foo" into "secret: foo" so error
// messages read consistently with the rest of the built-in's output
// (we use the verb "secret", the package uses "secrets"). Leaves
// other prefixes alone.
func stripPkgPrefix(msg string) string {
	const pkg = "secrets: "
	if strings.HasPrefix(msg, pkg) {
		return "secret: " + msg[len(pkg):]
	}
	return "secret: " + msg
}

// readLineRaw reads through the first \n on r byte-by-byte and
// returns the preceding bytes with a trailing \r stripped. EOF before
// any data returns ("", nil) so callers can distinguish "user typed
// nothing" from "stream broke."
//
// IMPORTANT: this MUST NOT use bufio. The shell's top-level REPL
// drives stdin byte-by-byte explicitly (see shell.go's readLine
// comment about issue #167) — buffered prefetch here would swallow
// the next REPL line (e.g. the follow-up `secret get` after a
// `secret set`).
func readLineRaw(r io.Reader) ([]byte, error) {
	var line []byte
	var buf [1]byte
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			if buf[0] == '\n' {
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				return line, nil
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if err == io.EOF {
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				return line, nil
			}
			return line, fmt.Errorf("secrets: read: %w", err)
		}
	}
}

// secretBuiltin implements the `aish secret …` built-in. Subcommands:
//
//	set NAME   read a value from stdin and store it (TTY: no-echo
//	           passphrase prompt on first use of the session).
//	get NAME   decrypt and write to the OS clipboard. NEVER prints
//	           the value. Confirms with "Value copied to clipboard."
//	list       print sorted names, one per line. No values.
//	rm NAME    delete the named entry.
//	help       print usage.
//
// Exit codes: 0 success, 1 user error, 2 vault/IO error.
//
// The session-scoped key cache (s.secretKey) is populated on the
// first successful unlock and reused by subsequent subcommands so the
// user is only prompted for their passphrase once per shell session.
func (s *Shell) secretBuiltin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, secretUsage())
		return 0
	}
	switch args[0] {
	case "set":
		return s.secretSet(args[1:], stdin, stdout, stderr)
	case "get":
		return s.secretGet(args[1:], stdin, stdout, stderr)
	case "list", "ls":
		return s.secretList(args[1:], stdin, stdout, stderr)
	case "rm", "remove", "delete":
		return s.secretRm(args[1:], stdin, stdout, stderr)
	case "lock":
		// Optional: zero the cached key before process exit.
		s.secretLock()
		fmt.Fprintln(stdout, "secret: vault locked")
		return 0
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, secretUsage())
		return 0
	default:
		fmt.Fprintf(stderr, "secret: unknown sub-command %q\n", args[0])
		fmt.Fprintln(stderr, secretUsage())
		return 1
	}
}

func secretUsage() string {
	return strings.TrimSpace(`
Usage: secret <subcommand>

  set NAME       read a value from stdin (TTY: no-echo) and store it
  get NAME       decrypt and write to the OS clipboard
  list           list stored secret names (sorted; values never shown)
  rm NAME        delete the named entry
  lock           clear the session passphrase cache

Values are stored encrypted under ~/.aish/vault/vault.json with
Argon2id (KDF) + AES-256-GCM. The first command of a session prompts
for your passphrase. Choose a strong one — there is no recovery path.
`)
}

// openVault unlocks the user's vault, caching the derived key on the
// Shell so subsequent calls in this session don't re-prompt. On a
// fresh vault the KDF cost parameters are printed once to stderr.
func (s *Shell) openVault(prompt string, stdin io.Reader, stdout, stderr io.Writer) (*secrets.Vault, error) {
	home := homeDir(s.env)
	if home == "" {
		return nil, errors.New("secret: HOME not set; cannot locate vault")
	}
	params := s.secretKDFParams()

	// If we have a cached passphrase, reuse it. Per the threat model
	// we cache the *passphrase* (not the derived key) so each Open
	// re-derives — this is the simplest design that still avoids the
	// per-command re-prompt. The cached buffer is zeroed on lock.
	if len(s.secretPass) > 0 {
		v, err := secrets.OpenVault(home, s.secretPass, params)
		if err != nil {
			// Passphrase was stale (vault rotated under us?) — clear
			// the cache so the next call re-prompts.
			s.secretLock()
			return nil, err
		}
		return v, nil
	}

	// First call this session — prompt.
	pass, err := readPassphrase(prompt, stdin, stderr)
	if err != nil {
		return nil, err
	}
	v, err := secrets.OpenVault(home, pass, params)
	if err != nil {
		// Wipe the passphrase before returning the error — we never
		// want the caller's `panic recovered` middleware to see it.
		secrets.Zero(pass)
		return nil, err
	}
	// Cache for the rest of the session. We deliberately keep the
	// passphrase rather than the derived key because keeping a freshly
	// derived key around without re-deriving creates a longer-lived
	// secret in memory; passphrase + on-demand-derive matches the
	// Open() lifetime exactly.
	s.secretPass = pass
	return v, nil
}

// secretSet handles `secret set NAME`. Reads the value from stdin
// (after the passphrase if not cached), then encrypts + persists.
func (s *Shell) secretSet(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: secret set NAME")
		return 1
	}
	name := args[0]

	v, err := s.openVault("Vault passphrase: ", stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 2
	}
	defer v.Close()

	// If this is a brand-new vault, surface the KDF cost ONCE.
	if !s.secretCostShown {
		fmt.Fprintf(stderr, "secret: vault initialized — KDF: %s\n", s.secretKDFParams().DescribeCost())
		s.secretCostShown = true
	}

	value, err := readValueLine(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 1
	}
	defer secrets.Zero(value)

	if err := v.Set(name, value); err != nil {
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 2
	}
	fmt.Fprintf(stdout, "secret: stored %s\n", name)
	return 0
}

// secretGet handles `secret get NAME`. Decrypts and writes to the OS
// clipboard. NEVER prints the value to stdout/stderr.
func (s *Shell) secretGet(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: secret get NAME")
		return 1
	}
	name := args[0]

	v, err := s.openVault("Vault passphrase: ", stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 2
	}
	defer v.Close()

	value, err := v.Get(name)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			fmt.Fprintf(stderr, "secret: %s not found\n", name)
			return 1
		}
		// Uniform error for any decrypt failure. Do NOT include err.Error()
		// directly — defensive against future variants that might leak.
		fmt.Fprintln(stderr, "secret: wrong passphrase or vault corrupt")
		return 2
	}
	defer secrets.Zero(value)

	if err := s.secretClipboard()(value); err != nil {
		fmt.Fprintf(stderr, "secret: clipboard: %s\n", err.Error())
		return 2
	}
	fmt.Fprintf(stdout, "secret: value copied to clipboard — %s\n", name)

	// Signed history event for #106. Best-effort: if history is not
	// available, the get still succeeds.
	s.recordSecretAccess(name)
	return 0
}

// secretList handles `secret list`. Prints sorted names, no values.
func (s *Shell) secretList(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "Usage: secret list")
		return 1
	}
	v, err := s.openVault("Vault passphrase: ", stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 2
	}
	defer v.Close()
	for _, n := range v.List() {
		fmt.Fprintln(stdout, n)
	}
	return 0
}

// secretRm handles `secret rm NAME`. Wipes the entry from the vault.
func (s *Shell) secretRm(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: secret rm NAME")
		return 1
	}
	name := args[0]
	v, err := s.openVault("Vault passphrase: ", stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 2
	}
	defer v.Close()
	if err := v.Rm(name); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			fmt.Fprintf(stderr, "secret: %s not found\n", name)
			return 1
		}
		fmt.Fprintf(stderr, "%s\n", stripPkgPrefix(err.Error()))
		return 2
	}
	fmt.Fprintf(stdout, "secret: removed %s\n", name)
	return 0
}

// readPassphrase reads a passphrase from stdin. The TTY path is
// covered by secrets.ReadPassphrase; the non-TTY path (tests, piped
// invocations) reads a single line from the supplied reader.
//
// Prompt goes to stderr to keep stdout clean for callers that pipe
// `secret list` etc.
func readPassphrase(prompt string, stdin io.Reader, stderr io.Writer) ([]byte, error) {
	if prompt != "" {
		// We don't use the TTY no-echo path in the built-in because the
		// dispatch layer drives stdin as an io.Reader, not a file
		// descriptor. The TTY no-echo path is exercised by the
		// integration-test binary that wires stdin to os.Stdin directly
		// (see secrets.ReadPassphrase).
		fmt.Fprint(stderr, prompt)
	}
	line, err := readLineRaw(stdin)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("secrets: empty passphrase")
	}
	return line, nil
}

// readValueLine reads a single line from stdin and returns it as the
// secret value to store. Empty values are rejected — `secret set NAME`
// with nothing to set is a mistake (the caller meant `rm`).
func readValueLine(stdin io.Reader) ([]byte, error) {
	line, err := readLineRaw(stdin)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("secrets: empty value")
	}
	return line, nil
}

// secretLock zeroes the cached passphrase. Safe to call multiple times.
func (s *Shell) secretLock() {
	if len(s.secretPass) > 0 {
		secrets.Zero(s.secretPass)
		s.secretPass = nil
	}
}

// recordSecretAccess writes a signed history event noting that NAME
// was read. Per #106 subset: name only, never the value. Best-effort —
// a missing or closed history is a no-op.
func (s *Shell) recordSecretAccess(name string) {
	if s.history == nil {
		return
	}
	store := s.history.Store()
	if store == nil {
		return
	}
	ev := history.Event{
		ID:        history.NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      history.Kind("secret.get"),
		Command:   "secret get " + name,
		Cwd:       s.cwd,
	}
	zero := 0
	ev.ExitCode = &zero
	_ = store.Append(&ev)
}

// secretKDFParams returns the active KDF cost. Production callers
// get DefaultKDFParams; tests inject something cheaper via
// SetSecretKDFParamsForTesting.
func (s *Shell) secretKDFParams() secrets.KDFParams {
	if s.secretKDFOverride != nil {
		return *s.secretKDFOverride
	}
	return secrets.DefaultKDFParams()
}

// secretClipboard returns the active clipboard writer. Production
// callers get secrets.CopyToClipboard; tests inject a capturing stub
// via SetClipboardFnForTesting.
func (s *Shell) secretClipboard() func([]byte) error {
	if s.secretClipFn != nil {
		return s.secretClipFn
	}
	return secrets.CopyToClipboard
}

// SetSecretKDFParamsForTesting clamps the KDF to a fast set of params
// for unit tests. Production callers MUST NOT use this.
func (s *Shell) SetSecretKDFParamsForTesting(p secrets.KDFParams) {
	s.secretKDFOverride = &p
}

// SetClipboardFnForTesting injects a clipboard stub for unit tests.
// The stub MUST be loud — if the real clipboard binary is missing on
// the test host, the test would otherwise be order-dependent.
func (s *Shell) SetClipboardFnForTesting(fn func([]byte) error) {
	s.secretClipFn = fn
}

// SecretLockForTesting wipes the cached passphrase so a follow-up
// command in the same Shell re-prompts. Used by tests that want to
// exercise the full unlock path across multiple subcommands.
func (s *Shell) SecretLockForTesting() {
	s.secretLock()
}
