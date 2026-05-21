package translate

import "testing"

func TestDetectShebang(t *testing.T) {
	cases := map[string]Dialect{
		"#!/usr/bin/env bash\necho hi\n": DialectBash,
		"#!/bin/zsh\necho hi\n":          DialectZsh,
		"#!/usr/bin/fish\necho hi\n":     DialectFish,
		"#!/bin/sh\necho hi\n":           DialectBash,
		"#!/usr/bin/env fish\necho hi\n": DialectFish,
	}
	for src, want := range cases {
		got := Detect("", src)
		if got != want {
			t.Errorf("Detect(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestDetectExtensionFallback(t *testing.T) {
	if got := Detect("script.fish", "echo hi\n"); got != DialectFish {
		t.Errorf("Detect by .fish ext = %q, want fish", got)
	}
	if got := Detect("script.zsh", "echo hi\n"); got != DialectZsh {
		t.Errorf("Detect by .zsh ext = %q, want zsh", got)
	}
	if got := Detect("script.sh", "echo hi\n"); got != DialectBash {
		t.Errorf("Detect by .sh ext = %q, want bash", got)
	}
}

func TestDetectContentHeuristic(t *testing.T) {
	fishLike := "function greet\n  echo hi\nend\nset -l name world\n"
	if got := Detect("", fishLike); got != DialectFish {
		t.Errorf("content-heuristic fish = %q, want fish", got)
	}
	bashLike := "if true; then\n  echo yes\nfi\nfor x in a b; do\n  echo $x\ndone\n"
	if got := Detect("", bashLike); got != DialectBash {
		t.Errorf("content-heuristic bash = %q, want bash", got)
	}
}
