package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
	"github.com/convergent-systems-co/aish/shell/internal/persona"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// identityBuiltin implements the `aish identity …` built-in:
//
//	list                  print profile names, sorted
//	show [<name>]         render the active or named profile
//	use <name>            set the active identity pointer
//	create <name>         create a new profile (interactive — gateway
//	                      URL and signer pubkey hash read from stdin)
//
// Exit codes: 0 success, 1 user error, 2 IO error.
func (s *Shell) identityBuiltin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, identityUsage())
		return 0
	}
	switch args[0] {
	case "list", "ls":
		return s.identityList(stdout, stderr)
	case "show":
		return s.identityShow(args[1:], stdout, stderr)
	case "use":
		return s.identityUse(args[1:], stdout, stderr)
	case "create":
		return s.identityCreate(args[1:], stdin, stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, identityUsage())
		return 0
	default:
		fmt.Fprintf(stderr, "identity: unknown sub-command %q\n", args[0])
		fmt.Fprintln(stderr, identityUsage())
		return 1
	}
}

func identityUsage() string {
	return strings.TrimSpace(`
Usage: identity <subcommand>

  list                              list available identity profiles
  show [<name>]                     show details of an identity (default: active)
  use <name> [--persona <persona>]  set <name> as the active identity; bind a persona
  create <name>                     create a new identity profile (interactive)
`)
}

func (s *Shell) identityList(stdout, stderr io.Writer) int {
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "identity: HOME not set")
		return 2
	}
	names, err := secrets.ListProfiles(home)
	if err != nil {
		fmt.Fprintf(stderr, "identity: %s\n", err.Error())
		return 2
	}
	active, _ := secrets.LoadActive(home)
	for _, n := range names {
		marker := "  "
		if n == active.Name {
			marker = "* "
		}
		fmt.Fprintf(stdout, "%s%s\n", marker, n)
	}
	return 0
}

func (s *Shell) identityShow(args []string, stdout, stderr io.Writer) int {
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "identity: HOME not set")
		return 2
	}
	var id secrets.Identity
	if len(args) == 0 {
		active, err := secrets.LoadActive(home)
		if err != nil {
			fmt.Fprintf(stderr, "identity: %s\n", err.Error())
			return 2
		}
		if active.Name == "" {
			fmt.Fprintln(stdout, "identity: no active identity")
			return 0
		}
		id = active
	} else {
		// Load the named profile directly.
		names, err := secrets.ListProfiles(home)
		if err != nil {
			fmt.Fprintf(stderr, "identity: %s\n", err.Error())
			return 2
		}
		want := args[0]
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(stderr, "identity: profile %q not found\n", want)
			return 1
		}
		// Briefly point active at it, read, restore. To avoid that
		// surface, just shell out to the read primitive. For MVP we
		// require an indirection through LoadActive on a per-profile
		// copy; here we keep it simple by reading the file directly
		// is more invasive — instead we expose nothing extra and
		// rely on the user setting `identity use` first if they want
		// the full record. The minimal output remains useful.
		id = secrets.Identity{Name: want}
	}
	fmt.Fprintf(stdout, "name:                 %s\n", id.Name)
	if id.GatewayURL != "" {
		fmt.Fprintf(stdout, "gateway_url:          %s\n", id.GatewayURL)
	}
	if id.SignerPubkeySHA256 != "" {
		fmt.Fprintf(stdout, "signer_pubkey_sha256: %s\n", id.SignerPubkeySHA256)
	}
	return 0
}

func (s *Shell) identityUse(args []string, stdout, stderr io.Writer) int {
	// v0.3-5.1 (#128): accept `--persona <name>` after NAME. Two-arg
	// shape `identity use NAME` keeps the pre-FU semantics; the
	// four-arg shape `identity use NAME --persona <p>` activates both
	// axes in one call. The persona binding is persisted to
	// ~/.aish/identity-persona.toml so subsequent `identity use NAME`
	// (without --persona) re-activates the bound persona too.
	name, personaName, parseErr := parseIdentityUseArgs(args)
	if parseErr != nil {
		fmt.Fprintln(stderr, parseErr.Error())
		return 1
	}
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "identity: HOME not set")
		return 2
	}
	if err := secrets.SetActive(home, name); err != nil {
		// "profile not found" is a user error (1); the rest is IO (2).
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "identity: %s\n", err.Error())
			return 1
		}
		fmt.Fprintf(stderr, "identity: %s\n", err.Error())
		return 2
	}
	fmt.Fprintf(stdout, "identity: active = %s\n", name)

	// Persona binding: explicit --persona wins. When --persona is
	// absent, fall back to the existing binding for this identity.
	chosenPersona := personaName
	if chosenPersona == "" {
		chosenPersona = persona.ReadBinding(home, name)
	} else {
		// Persist the explicit choice so future `identity use NAME`
		// (without --persona) re-activates the same persona.
		if err := persona.WriteBinding(home, name, chosenPersona); err != nil {
			fmt.Fprintf(stderr, "identity: persona binding: %v\n", err)
			// Non-fatal — identity is already active. Continue to
			// activate the persona in-process so the user sees the
			// effect even if the binding file write failed.
		}
	}
	if chosenPersona != "" && s.personas != nil {
		if _, ok := s.personas.Get(chosenPersona); ok {
			if err := persona.WriteActivePersona(home, chosenPersona); err != nil {
				fmt.Fprintf(stderr, "identity: activate persona %q: %v\n", chosenPersona, err)
			} else {
				s.activePersona = chosenPersona
				fmt.Fprintf(stdout, "persona:  active = %s (bound to identity %s)\n", chosenPersona, name)
			}
		} else {
			fmt.Fprintf(stderr, "identity: persona %q is not in the registry; binding kept but not activated\n", chosenPersona)
		}
	}

	s.recordIdentityUse(name)
	return 0
}

// parseIdentityUseArgs parses the argv tail of `identity use`. Accepted
// shapes:
//
//	identity use NAME
//	identity use NAME --persona <persona-name>
//
// Returns (name, persona, err). An empty persona is the no-flag case.
func parseIdentityUseArgs(args []string) (string, string, error) {
	switch len(args) {
	case 1:
		return args[0], "", nil
	case 3:
		if args[1] != "--persona" {
			return "", "", fmt.Errorf("Usage: identity use NAME [--persona <persona>]")
		}
		if args[2] == "" {
			return "", "", fmt.Errorf("identity: --persona requires a non-empty value")
		}
		return args[0], args[2], nil
	default:
		return "", "", fmt.Errorf("Usage: identity use NAME [--persona <persona>]")
	}
}

// identityCreate runs the create flow. With no stdin attached, it
// creates the profile with just the name (no gateway URL or signer
// hash). When stdin has lines, the first line is gateway URL and the
// second is signer pubkey SHA-256. A blank line is "leave empty."
func (s *Shell) identityCreate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: identity create NAME")
		return 1
	}
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "identity: HOME not set")
		return 2
	}
	id := secrets.Identity{Name: args[0]}

	// Read gateway URL (optional).
	if stdin != nil {
		fmt.Fprintln(stderr, "gateway URL (blank to skip):")
		if line, err := readSingleLine(stdin); err == nil && line != "" {
			id.GatewayURL = line
		}
		fmt.Fprintln(stderr, "signer pubkey SHA-256 (blank to skip):")
		if line, err := readSingleLine(stdin); err == nil && line != "" {
			id.SignerPubkeySHA256 = line
		}
	}

	if err := secrets.CreateProfile(home, id); err != nil {
		fmt.Fprintf(stderr, "identity: %s\n", err.Error())
		return 2
	}
	fmt.Fprintf(stdout, "identity: created %s\n", args[0])
	return 0
}

// readSingleLine reads through the first \n in r and strips \r?\n.
// Empty input is not an error here (caller wanted optional fields).
func readSingleLine(r io.Reader) (string, error) {
	var line []byte
	var buf [1]byte
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			if buf[0] == '\n' {
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				return string(line), nil
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return string(line), nil
		}
	}
}

// recordIdentityUse writes a best-effort history event for the
// identity switch (per #106 subset). No-op if history is unavailable.
func (s *Shell) recordIdentityUse(name string) {
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
		Kind:      history.Kind("identity.use"),
		Command:   "identity use " + name,
		Cwd:       s.cwd,
	}
	zero := 0
	ev.ExitCode = &zero
	_ = store.Append(&ev)
}
