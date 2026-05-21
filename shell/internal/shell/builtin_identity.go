package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
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

  list                  list available identity profiles
  show [<name>]         show details of an identity (default: active)
  use <name>            set <name> as the active identity
  create <name>         create a new identity profile (interactive)
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
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: identity use NAME")
		return 1
	}
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "identity: HOME not set")
		return 2
	}
	if err := secrets.SetActive(home, args[0]); err != nil {
		// "profile not found" is a user error (1); the rest is IO (2).
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "identity: %s\n", err.Error())
			return 1
		}
		fmt.Fprintf(stderr, "identity: %s\n", err.Error())
		return 2
	}
	fmt.Fprintf(stdout, "identity: active = %s\n", args[0])
	s.recordIdentityUse(args[0])
	return 0
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
