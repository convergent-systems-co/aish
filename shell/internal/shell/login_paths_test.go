package shell

import "testing"

// TestUserRCPathForGoos exercises the pure-function path-resolver
// that picks the per-user RC file location per GOOS. v1.0-5
// extends v0.3-1 so the same shell binary can find its RC on
// Windows (`%APPDATA%\aish\aishrc.toml`) and POSIX
// (`$HOME/.aish/aishrc.toml`) without a build-tag fork in callers.
//
// The function is intentionally a pure helper that takes `goos`,
// `home`, and `appdata` as parameters so the resolution is
// testable from any host without `runtime.GOOS` indirection.
func TestUserRCPathForGoos(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		home    string
		appdata string
		want    string
	}{
		{
			name:    "linux uses home + .aish",
			goos:    "linux",
			home:    "/home/alice",
			appdata: "",
			want:    "/home/alice/.aish/aishrc.toml",
		},
		{
			name:    "darwin uses home + .aish",
			goos:    "darwin",
			home:    "/Users/alice",
			appdata: "",
			want:    "/Users/alice/.aish/aishrc.toml",
		},
		{
			name:    "windows uses appdata when set",
			goos:    "windows",
			home:    `C:\Users\Alice`,
			appdata: `C:\Users\Alice\AppData\Roaming`,
			want:    `C:\Users\Alice\AppData\Roaming\aish\aishrc.toml`,
		},
		{
			name:    "windows falls back to home + AppData/Roaming when appdata is unset",
			goos:    "windows",
			home:    `C:\Users\Alice`,
			appdata: "",
			want:    `C:\Users\Alice\AppData\Roaming\aish\aishrc.toml`,
		},
		{
			name:    "windows with both empty returns empty (caller handles)",
			goos:    "windows",
			home:    "",
			appdata: "",
			want:    "",
		},
		{
			name:    "posix with empty home returns empty (caller handles)",
			goos:    "linux",
			home:    "",
			appdata: "",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := userRCPathForGoos(tc.goos, tc.home, tc.appdata)
			if got != tc.want {
				t.Errorf("userRCPathForGoos(%q, %q, %q) = %q, want %q",
					tc.goos, tc.home, tc.appdata, got, tc.want)
			}
		})
	}
}

// TestSystemRCPathForGoos exercises the system-wide RC resolver.
// On POSIX this is the fixed `/etc/aish/aishrc`. On Windows it
// resolves from `%PROGRAMDATA%` at runtime (defaulting to the
// conventional `C:\ProgramData` when the env var is unset, which
// matches Windows' own default).
func TestSystemRCPathForGoos(t *testing.T) {
	cases := []struct {
		name        string
		goos        string
		programData string
		want        string
	}{
		{
			name:        "linux",
			goos:        "linux",
			programData: "",
			want:        "/etc/aish/aishrc",
		},
		{
			name:        "darwin",
			goos:        "darwin",
			programData: "",
			want:        "/etc/aish/aishrc",
		},
		{
			name:        "windows with PROGRAMDATA",
			goos:        "windows",
			programData: `C:\ProgramData`,
			want:        `C:\ProgramData\aish\aishrc.toml`,
		},
		{
			name:        "windows without PROGRAMDATA falls back to default",
			goos:        "windows",
			programData: "",
			want:        `C:\ProgramData\aish\aishrc.toml`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := systemRCPathForGoos(tc.goos, tc.programData)
			if got != tc.want {
				t.Errorf("systemRCPathForGoos(%q, %q) = %q, want %q",
					tc.goos, tc.programData, got, tc.want)
			}
		})
	}
}
