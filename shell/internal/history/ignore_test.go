package history

import "testing"

func TestIgnoreMatcherDefaults(t *testing.T) {
	m := DefaultIgnoreMatcher()
	cases := []struct {
		path string
		want bool
	}{
		{"node_modules/foo.js", true},
		{"src/node_modules/index.js", true}, // nested also matches
		{".git/HEAD", true},
		{"vendor/x/y.go", true},
		{"dist/bundle.js", true},
		{"build/out.o", true},
		{"target/release/x", true},
		{"__pycache__/foo.pyc", true},
		{"app.log", true},
		{"pkg.tmp", true},
		{".cache/foo", true},
		// negative — these MUST snapshot
		{"src/main.go", false},
		{"README.md", false},
		{"cmd/aish/main.go", false},
		// leading slash should normalize
		{"/abs/path/to/node_modules/x", true},
		{"/abs/src/main.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := m.Match(tc.path)
			if got != tc.want {
				t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestIgnoreEmptyMatcherIsPermissive verifies that an empty matcher
// (no patterns) returns false for every path. Snapshotting MUST occur
// when no filter is configured.
func TestIgnoreEmptyMatcherIsPermissive(t *testing.T) {
	m := NewIgnoreMatcher(nil)
	if m.Match("node_modules/foo") {
		t.Errorf("empty matcher should not match")
	}
}
