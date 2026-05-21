package term

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestBuiltinCompleter_FirstToken — the built-in completer matches
// the shell's reserved built-ins for the first-token slot only.
func TestBuiltinCompleter_FirstToken(t *testing.T) {
	c := BuiltinCompleter{}
	got, _ := c.Complete(CompletionContext{
		Token:      "c",
		FirstToken: true,
	})
	sort.Strings(got)
	want := []string{"cache", "cd"}
	if !equalSlices(got, want) {
		t.Fatalf("first-token completer with prefix \"c\": want %v, got %v", want, got)
	}

	// Second-token slot: built-ins must NOT complete.
	got, _ = c.Complete(CompletionContext{
		Token:      "c",
		FirstToken: false,
	})
	if len(got) != 0 {
		t.Fatalf("second-token built-in completer should return empty; got %v", got)
	}
}

// TestPathCompleter_RelativeToCwd — completes against the test's
// temp filesystem (cwd-relative).
func TestPathCompleter_RelativeToCwd(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"alpha.txt", "beta.txt", "carry.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := NewPathCompleter(dir)
	got, _ := c.Complete(CompletionContext{Token: "be", FirstToken: false})
	if !equalSlices(got, []string{"beta.txt"}) {
		t.Fatalf("want [beta.txt], got %v", got)
	}
}

// TestPathCompleter_NestedPath — completion of `sub/`.
func TestPathCompleter_NestedPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "inner.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewPathCompleter(dir)
	got, _ := c.Complete(CompletionContext{Token: "sub/in", FirstToken: false})
	if !equalSlices(got, []string{"sub/inner.txt"}) {
		t.Fatalf("want [sub/inner.txt], got %v", got)
	}
}

// TestPathCompleter_DirectoryHasTrailingSlash — directories complete
// with a trailing `/` so the user can immediately recurse with Tab.
func TestPathCompleter_DirectoryHasTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewPathCompleter(dir)
	got, _ := c.Complete(CompletionContext{Token: "sub", FirstToken: false})
	if !equalSlices(got, []string{"subdir/"}) {
		t.Fatalf("want [subdir/], got %v", got)
	}
}

// TestPathCompleter_TildeExpansion — a leading `~/` expands to $HOME
// during completion (the returned candidate keeps the `~/` prefix).
func TestPathCompleter_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "hello.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewPathCompleter(t.TempDir()) // cwd is unrelated
	got, _ := c.Complete(CompletionContext{Token: "~/hello", FirstToken: false})
	if !equalSlices(got, []string{"~/hello.txt"}) {
		t.Fatalf("want [~/hello.txt], got %v", got)
	}
}

// TestBinaryCompleter — when given a first-token slot, returns names
// from a stubbed PATH directory.
func TestBinaryCompleter_FirstToken(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"foo-tool", "bar-tool", "fooBar"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := NewBinaryCompleter([]string{dir})
	got, _ := c.Complete(CompletionContext{Token: "foo", FirstToken: true})
	sort.Strings(got)
	want := []string{"foo-tool", "fooBar"}
	if !equalSlices(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}

	// Second-token slot: binary completer MUST NOT fire.
	got, _ = c.Complete(CompletionContext{Token: "foo", FirstToken: false})
	if len(got) != 0 {
		t.Fatalf("binary completer on non-first token should be empty; got %v", got)
	}
}

// TestComposite — Composite dispatches to every wrapped completer
// and returns the union, de-duplicated and sorted.
func TestComposite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cd-script"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewComposite(BuiltinCompleter{}, NewBinaryCompleter([]string{dir}))
	got, _ := c.Complete(CompletionContext{Token: "cd", FirstToken: true})
	sort.Strings(got)
	want := []string{"cd", "cd-script"}
	if !equalSlices(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// equalSlices is a one-line helper. Cannot use slices.Equal everywhere
// because the test reads better with this name.
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
