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
	// v0.3-4: mv joins the destructive set so a rename of an existing
	// file gets snapshotted before exec. The interceptor distinguishes
	// mv from rm via parser command-name and calls SnapshotMove rather
	// than SnapshotMany.
	"mv": {},
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
// it returns [./dist]; for `dd of=/tmp/x` it returns [/tmp/x]; for `mv
// SRC DST` it returns [SRC] (the snapshot path) — DST is handled by
// RenameTargets, not TargetPaths, because mv is a rename, not a
// delete; for a non-destructive pipeline it returns nil.
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
		case "mv":
			// mv is handled through RenameTargets; emit the SOURCE
			// paths only so the snapshotter takes their bytes. The
			// DST path is snapshotted in the modify-pair branch
			// inside the interceptor.
			args := extractFileArgs(c.Args, nil)
			if len(args) >= 2 {
				// `mv SRC1 SRC2 ... DSTDIR` — all but the last are
				// sources. `mv SRC DST` reduces to the same shape:
				// one source, one destination.
				out = append(out, args[:len(args)-1]...)
			}
		default:
			out = append(out, extractFileArgs(c.Args, flagsTakingValue[c.Name])...)
		}
	}
	return out
}

// RenameTargets returns the (src, dst) pairs for every `mv` invocation
// in the pipeline. For `mv SRC DST` it returns [(SRC, DST)]; for `mv
// SRC1 SRC2 DSTDIR` (multi-source mv) it returns one pair per source
// with the destination derived as filepath.Join(DSTDIR, base(SRC)).
// Non-mv stages produce no pairs.
//
// The caller canonicalizes the paths against the shell cwd — like
// TargetPaths, this returns argv-as-typed.
func RenameTargets(pl parser.Pipeline) [][2]string {
	var out [][2]string
	for _, c := range pl.Commands {
		if c.Name != "mv" {
			continue
		}
		args := extractFileArgs(c.Args, nil)
		if len(args) < 2 {
			continue
		}
		srcs := args[:len(args)-1]
		dst := args[len(args)-1]
		// `mv A B` — single source, single destination. The
		// destination can be either a file path (rename) or a
		// directory (move into); the interceptor checks at snapshot
		// time and adjusts.
		for _, s := range srcs {
			out = append(out, [2]string{s, dst})
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
