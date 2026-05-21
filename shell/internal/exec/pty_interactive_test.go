package exec

import "testing"

// TestIsInteractive locks the curated v0.2-2 allowlist (per #57). The
// list is small and explicit on purpose; any addition or removal MUST
// show up here as a failing-then-passing test, not a silent map edit.
func TestIsInteractive(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"vim", true},
		{"vi", true},
		{"nvim", true},
		{"nano", true},
		{"less", true},
		{"more", true},
		{"man", true},
		{"top", true},
		{"htop", true},
		{"ssh", true},
		{"az", true},
		// case-insensitive basename match — future Windows port
		// will see `Vim.exe`, so prove the contract now.
		{"Vim", true},
		{"VIM.EXE", true},
		{"/usr/local/bin/vim", true},
		{"/opt/homebrew/bin/htop", true},
		// negatives — anything not on the list must NOT be PTY'd
		// (pipelines, scripts, CI invocations would break).
		{"echo", false},
		{"cat", false},
		{"ls", false},
		{"grep", false},
		{"", false},
		{"git", false},  // git pager triggers `less` internally; that's enough
		{"gh", false},   // currently degrades cleanly without a PTY
		{"node", false},
	}
	for _, c := range cases {
		got := IsInteractive(c.name)
		if got != c.want {
			t.Errorf("IsInteractive(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
