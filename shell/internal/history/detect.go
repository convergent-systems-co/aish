package history

import (
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// destructiveNames is the curated set of POSIX commands whose typical
// effect is to remove or overwrite file bytes. False positives are
// fine — they just trigger an unnecessary snapshot. False negatives
// lose data, so this list is conservative.
//
// Out of scope for v0.1 (per .artifacts/plans/v0.1-4.md):
//   - mv (a move out of cwd looks the same as a rename for v0.1; the
//     "overwrite" case is v0.2 modification-snapshot territory).
//   - > / >> redirect overwrite — the parser does not surface
//     redirects yet (parser is v0.1-1 scope).
var destructiveNames = map[string]struct{}{
	"rm":       {},
	"rmdir":    {},
	"unlink":   {},
	"shred":    {},
	"srm":      {},
	"truncate": {},
	"dd":       {},
}

// flagsTakingValue records which short flags consume the next argv as
// their value rather than as a positional path. Without this table,
// `truncate -s 0 /tmp/x` would emit `[0, /tmp/x]` because `0` is not
// prefixed with `-`.
//
// Per-command keyed; the empty key applies to every destructive
// command (catches the universal `--` end-of-options separately —
// that lives in extractFileArgs).
var flagsTakingValue = map[string]map[string]bool{
	"truncate": {"-s": true, "--size": true, "-r": true, "--reference": true},
	"shred":    {"-n": true, "--iterations": true, "-s": true, "--size": true},
}

// IsDestructive returns true if any command in the pipeline is in the
// destructive set. v0.1 snapshots every destructive stage; in practice
// only the first stage typically writes (a pipe's downstream `rm` is
// rare), but checking the whole pipeline is more conservative.
func IsDestructive(pl parser.Pipeline) bool {
	for _, c := range pl.Commands {
		if _, ok := destructiveNames[c.Name]; ok {
			return true
		}
	}
	return false
}

// TargetPaths returns the list of paths the destructive commands
// target. For `rm /a /b /c` it returns [/a /b /c]; for `rm -rf ./dist`
// it returns [./dist]; for `dd of=/tmp/x` it returns [/tmp/x]; for a
// non-destructive pipeline it returns nil.
//
// The caller is expected to canonicalize each path against the shell
// cwd before passing it to the Snapshotter — TargetPaths returns
// argv-as-typed and is intentionally lossless about that.
func TargetPaths(pl parser.Pipeline) []string {
	var out []string
	for _, c := range pl.Commands {
		_, isDestructive := destructiveNames[c.Name]
		if !isDestructive {
			continue
		}
		switch c.Name {
		case "dd":
			// dd uses of=<path>. Other forms (of=/dev/null, no of=)
			// produce no target.
			for _, a := range c.Args {
				if rest, ok := stripPrefix(a, "of="); ok && rest != "" {
					out = append(out, rest)
				}
			}
		default:
			out = append(out, extractFileArgs(c.Args, flagsTakingValue[c.Name])...)
		}
	}
	return out
}

// extractFileArgs returns the positional arguments of an rm-style
// command — anything that is not a flag (`-x`, `--flag`, `--key=val`).
// The POSIX `--` end-of-options marker is honored: arguments after it
// are positional regardless of leading `-`.
//
// valueFlags names short / long flags whose immediate next argv is
// the flag's value (e.g. truncate's `-s SIZE`). The next argv after
// such a flag is consumed, never treated as a path. Pass nil when no
// such flags apply.
func extractFileArgs(args []string, valueFlags map[string]bool) []string {
	var out []string
	endOfOpts := false
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if endOfOpts {
			out = append(out, a)
			continue
		}
		if a == "--" {
			endOfOpts = true
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			// `--key=value` carries its value inline; do not eat the
			// next argv.
			if !strings.Contains(a, "=") && valueFlags[a] {
				skipNext = true
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// stripPrefix is the standard "trim leading prefix and report" helper.
// Used by the dd-specific of= extractor.
func stripPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
