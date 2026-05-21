package secrets

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// ReadPassphrase reads a passphrase from stdin. If stdin is a TTY,
// the read is no-echo via golang.org/x/term.ReadPassword. Otherwise
// (pipe, file, here-doc), the read is a single line stripped of the
// trailing newline.
//
// An empty passphrase is rejected with a clear error message. The
// returned slice is the secret material; the caller MUST call Zero
// on it when done.
//
// The prompt is written to stderr (not stdout) so passphrase prompts
// don't pollute pipelines that capture stdout.
func ReadPassphrase(prompt string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		if prompt != "" {
			fmt.Fprint(os.Stderr, prompt)
		}
		pw, err := term.ReadPassword(fd)
		// term.ReadPassword does NOT print a newline after the user
		// hits enter — print one explicitly so the next prompt lands
		// on a fresh line.
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, fmt.Errorf("secrets: read passphrase: %w", err)
		}
		if len(pw) == 0 {
			return nil, errors.New("secrets: empty passphrase")
		}
		return pw, nil
	}
	return ReadPassphraseFrom(os.Stdin)
}

// ReadPassphraseFrom is the non-TTY path, factored out so tests can
// drive it with a bytes.Buffer. Reads a single line, strips \r?\n,
// rejects empty input.
func ReadPassphraseFrom(r io.Reader) ([]byte, error) {
	line, err := readLineStripped(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("secrets: empty passphrase")
	}
	return line, nil
}

// ReadValueFrom reads a single line from r as a secret value. Strips
// \r?\n. An empty value is rejected — `secret set NAME` with nothing
// to set is a mistake (the caller meant `rm`).
func ReadValueFrom(r io.Reader) ([]byte, error) {
	line, err := readLineStripped(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("secrets: empty value")
	}
	return line, nil
}

// readLineStripped reads through the first \n in r and returns the
// preceding bytes with \r stripped. EOF before any data is treated as
// an empty line (not an error) so callers can distinguish "user typed
// nothing" from "stream broke."
func readLineStripped(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("secrets: read: %w", err)
	}
	// Trim trailing \n then \r if present.
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, nil
}
