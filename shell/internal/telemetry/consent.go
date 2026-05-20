package telemetry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ConsentFilename is the basename of the consent file inside the
// user's `~/.aish` directory. Public so the `aish stats` built-in can
// reference it in error messages.
const ConsentFilename = "telemetry.toml"

// DefaultConsentFile is the text written to telemetry.toml on first
// use when no file exists. The header explains each key so a user
// auditing their consent state can read the file directly without
// hunting through documentation.
//
// Defaults: local opt-in, aggregate opt-out. The opt-out posture for
// aggregate is the "privacy first" stance from GOALS.md §"Epic
// v0.1-5" — no telemetry leaves the user's machine until they
// explicitly say so.
const DefaultConsentFile = `# aish telemetry consent — written by v0.1-5 on first run.
# This file is the single source of truth for what telemetry aish
# collects and where it goes. Edit it directly to change your choices.
#
# opt_in_local controls whether aish records per-session counters to
# ~/.aish/sessions/<id>.json. These files never leave your machine;
# they back the ` + "`aish stats`" + ` local dashboard. Default: true.
#
# opt_in_aggregate controls whether aish queues a privacy-preserving
# session payload to ~/.aish/sessions/pending/<id>.json for later
# upload to the team aggregate dashboard. Counters only; never command
# lines, paths, or environment data. Default: false (opt out).
#
# In v0.1-5 the aggregate transport is not yet wired — the pending
# directory is the queue; v0.2 will drain it on a successful POST.

[telemetry]
opt_in_local = true
opt_in_aggregate = false
`

// Consent is the typed projection of `~/.aish/telemetry.toml`. Two
// independent booleans: own-machine data, and outbound data.
type Consent struct {
	// OptInLocal authorizes recording session rows to
	// ~/.aish/sessions/<id>.json. Default true. When false, the
	// Recorder still ticks in-memory counters (so `aish stats` shows
	// the current session) but does NOT persist anything on Close.
	OptInLocal bool
	// OptInAggregate authorizes writing session payloads to the
	// pending queue at ~/.aish/sessions/pending/<id>.json for later
	// upload. Default FALSE. When false, no pending file is ever
	// written — regardless of OptInLocal.
	OptInAggregate bool
}

// rawConsent mirrors the on-disk TOML shape. Kept separate from
// Consent so the public type can grow without breaking the wire shape.
type rawConsent struct {
	Telemetry struct {
		OptInLocal     *bool `toml:"opt_in_local"`
		OptInAggregate *bool `toml:"opt_in_aggregate"`
	} `toml:"telemetry"`
}

// DefaultConsent returns the v0.1-5 defaults: local on, aggregate off.
// Exposed so callers (and tests) can build the default state without
// touching the filesystem.
func DefaultConsent() Consent {
	return Consent{OptInLocal: true, OptInAggregate: false}
}

// LoadConsent reads ~/.aish/telemetry.toml from `dotAishDir`. On any
// failure it returns the default opt-out-for-aggregate posture — a
// missing or malformed consent file MUST NOT enable aggregate
// telemetry. This rule is the privacy guarantee in #39.
//
// If the file does not exist, LoadConsent writes the default file
// with the documented header so the user can audit their state. The
// write uses O_CREATE|O_EXCL so two parallel shells don't fight over
// the file; the loser silently falls back to reading the winner's
// content.
//
// Partial files (e.g. only opt_in_local present) honor the present
// keys and default the missing ones — equivalent to the user
// explicitly setting only what they care about.
func LoadConsent(dotAishDir string) Consent {
	defaults := DefaultConsent()
	if dotAishDir == "" {
		return defaults
	}
	path := filepath.Join(dotAishDir, ConsentFilename)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// First run for this user — write the documented default file
		// so they can audit it. Failure to write is silent: we still
		// return the default Consent in-memory.
		_ = writeDefaultConsentFile(dotAishDir, path)
		return defaults
	}
	if err != nil {
		// Unreadable file (permission denied, etc.) — privacy stance
		// is to keep aggregate off.
		return defaults
	}
	var raw rawConsent
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return defaults
	}
	c := defaults
	if raw.Telemetry.OptInLocal != nil {
		c.OptInLocal = *raw.Telemetry.OptInLocal
	}
	if raw.Telemetry.OptInAggregate != nil {
		c.OptInAggregate = *raw.Telemetry.OptInAggregate
	}
	return c
}

// writeDefaultConsentFile creates dotAishDir if needed and writes the
// default telemetry.toml at path. Uses O_CREATE|O_EXCL so a
// concurrent first-call from another shell doesn't clobber.
//
// Returns nil on success or on EEXIST (the file appeared underneath
// us, which is fine — we'll read it next call). All other errors are
// returned for the caller's tests to assert against.
func writeDefaultConsentFile(dotAishDir, path string) error {
	if err := os.MkdirAll(dotAishDir, 0o755); err != nil {
		return fmt.Errorf("telemetry: consent: mkdir %q: %w", dotAishDir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("telemetry: consent: open %q: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(DefaultConsentFile); err != nil {
		return fmt.Errorf("telemetry: consent: write %q: %w", path, err)
	}
	return nil
}
