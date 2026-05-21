package shell

import (
	"path/filepath"
	"strings"
)

// joinWindows builds a Windows-style path by joining segments with
// a backslash. Used by the pure-function path helpers so the
// returned string is correct regardless of the host's
// `filepath.Separator` (which is `/` when this code runs on
// macOS or Linux during cross-host testing). Strips trailing
// backslashes from the first segment to avoid `C:\\\\aish\\...`
// when the caller passes `C:\` literally.
func joinWindows(segments ...string) string {
	if len(segments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(segments))
	for i, s := range segments {
		if s == "" {
			continue
		}
		if i == 0 {
			s = strings.TrimRight(s, `\/`)
		} else {
			s = strings.Trim(s, `\/`)
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, `\`)
}

// userRCPathForGoos resolves the per-user RC file path for a given
// OS, $HOME, and %APPDATA% value. It is a pure function so the
// resolution rules are testable from any host — callers in
// production pass `runtime.GOOS`, `os.Getenv("HOME")`, and
// `os.Getenv("APPDATA")`; tests pass literals.
//
// POSIX (linux/darwin/freebsd/…): `<home>/.aish/aishrc.toml`.
// Windows: `<appdata>\aish\aishrc.toml`. When `appdata` is empty
// on Windows, we fall back to `<home>\AppData\Roaming\aish\aishrc.toml`
// — the conventional Windows default that the OS itself would
// resolve to. When both are empty (or `home` is empty on POSIX),
// the function returns "" and the caller skips RC sourcing — RC
// failure must not deny login (v0.3-1 convention).
//
// See `.artifacts/plans/v1.0-5.md` §1 for the alternatives
// table that picked `%APPDATA%` over `%LOCALAPPDATA%` /
// `%USERPROFILE%\.aish` / etc.
func userRCPathForGoos(goos, home, appdata string) string {
	if goos == "windows" {
		if appdata != "" {
			return joinWindows(appdata, "aish", "aishrc.toml")
		}
		if home != "" {
			return joinWindows(home, "AppData", "Roaming", "aish", "aishrc.toml")
		}
		return ""
	}
	// POSIX path.
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".aish", "aishrc.toml")
}

// systemRCPathForGoos resolves the system-wide RC file path. On
// POSIX it is the fixed `/etc/aish/aishrc` (matching v0.3-1). On
// Windows it is `<programdata>\aish\aishrc.toml`; when
// `programdata` is empty, we fall back to the conventional
// `C:\ProgramData` default. The Windows file ends in `.toml`
// (consistent with the per-user file); POSIX matches the legacy
// no-extension `/etc/aish/aishrc` v0.3-1 convention.
func systemRCPathForGoos(goos, programData string) string {
	if goos == "windows" {
		base := programData
		if base == "" {
			base = `C:\ProgramData`
		}
		return joinWindows(base, "aish", "aishrc.toml")
	}
	// POSIX path: keep `filepath.Join` for consistency, though
	// the result is a fixed string today.
	return filepath.Join("/etc", "aish", "aishrc")
}
