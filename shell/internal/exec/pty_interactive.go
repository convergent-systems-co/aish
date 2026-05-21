// Package exec — interactive-binary allowlist for PTY dispatch.
//
// v0.2-2 ships an explicit allowlist of binary names that should run
// under a PTY when the parent stdin is a TTY. Future epics may replace
// this with a smarter probe (e.g. cache-plugin classification or an
// isatty(0)-of-child probe via a thin C-free shim) — but a static
// allowlist is the right floor: explicit, auditable, no probe cost
// on the hot path.
//
// The list is keyed on the *basename* of the first pipeline token
// (so `/usr/local/bin/vim` and `vim` both match) with case-insensitive
// comparison so `Vim.exe` works the day Windows PTYs land.
package exec

import (
	"path/filepath"
	"strings"
)

// interactiveBinaries is the curated v0.2-2 list. Entries MUST be
// lowercase basenames. Adding a name requires an entry plus a manual
// smoke test on at least one platform (see #57).
//
// Sourced from issue #57 (test matrix) and GOALS.md §"Epic v0.2-2"
// (vim, ssh, htop, less, top, az login). `nvim`/`vi`/`nano`/`man`/
// `more` added because they share the same TTY-or-die behavior; `gh`
// and `lazygit` deferred — they degrade cleanly without a PTY today.
var interactiveBinaries = map[string]struct{}{
	"vim":  {},
	"vi":   {},
	"nvim": {},
	"nano": {},
	"less": {},
	"more": {},
	"man":  {},
	"top":  {},
	"htop": {},
	"ssh":  {},
	"az":   {},
}

// IsInteractive reports whether the given command name (or absolute
// path) should be run under a PTY by default. Case-insensitive on the
// basename so a future Windows port can match `Vim.exe` the same way.
//
// An empty name returns false — the caller should treat this as "no
// PTY" rather than crashing.
func IsInteractive(name string) bool {
	if name == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(name))
	// Trim a Windows-style `.exe` so `vim.exe` matches `vim`.
	base = strings.TrimSuffix(base, ".exe")
	_, ok := interactiveBinaries[base]
	return ok
}
