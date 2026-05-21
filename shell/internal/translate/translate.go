package translate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Read dispatches to the dialect-specific reader.
//
// To avoid import cycles (the readers depend on this package), Read
// accepts an injected reader function for each dialect. The shell
// builtin wires this to the reader package's exported Read{Bash,Zsh,
// Fish} functions; tests inject fakes.
type ReaderFunc func(src string) (*Script, error)

// Readers is the set of dialect readers passed into Read. Allows
// the translate package to stay decoupled from the reader package.
type Readers struct {
	Bash ReaderFunc
	Zsh  ReaderFunc
	Fish ReaderFunc
}

// Read parses src according to the requested dialect. When dialect
// is empty, Detect is used.
func Read(dialect Dialect, src string, readers Readers) (*Script, error) {
	switch dialect {
	case DialectBash:
		if readers.Bash == nil {
			return nil, fmt.Errorf("translate: no bash reader registered")
		}
		return readers.Bash(src)
	case DialectZsh:
		if readers.Zsh == nil {
			return nil, fmt.Errorf("translate: no zsh reader registered")
		}
		return readers.Zsh(src)
	case DialectFish:
		if readers.Fish == nil {
			return nil, fmt.Errorf("translate: no fish reader registered")
		}
		return readers.Fish(src)
	default:
		return nil, fmt.Errorf("translate: unknown dialect %q", dialect)
	}
}

// Detect classifies a script's dialect from (in order):
//  1. Shebang (`#!/usr/bin/env bash`, `#!/bin/zsh`, `#!/usr/bin/fish`).
//  2. File extension (`.sh`, `.bash` → bash; `.zsh` → zsh; `.fish` → fish).
//  3. Content heuristic: presence of `function … end` or `set -l` ⇒
//     fish; presence of `fi`/`done`/`esac` ⇒ bash; otherwise bash
//     (the safe default — bash is the most common).
//
// The path is optional (only used for the extension test); pass ""
// to skip.
func Detect(path string, src string) Dialect {
	// 1. Shebang.
	if d, ok := dialectFromShebang(src); ok {
		return d
	}
	// 2. Extension.
	if path != "" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".fish":
			return DialectFish
		case ".zsh":
			return DialectZsh
		case ".sh", ".bash":
			return DialectBash
		}
	}
	// 3. Content heuristic.
	if looksLikeFish(src) {
		return DialectFish
	}
	return DialectBash
}

func dialectFromShebang(src string) (Dialect, bool) {
	if !strings.HasPrefix(src, "#!") {
		return "", false
	}
	nl := strings.IndexByte(src, '\n')
	if nl < 0 {
		nl = len(src)
	}
	first := src[:nl]
	switch {
	case strings.Contains(first, "fish"):
		return DialectFish, true
	case strings.Contains(first, "zsh"):
		return DialectZsh, true
	case strings.Contains(first, "bash"), strings.Contains(first, "/sh"):
		return DialectBash, true
	}
	return "", false
}

func looksLikeFish(src string) bool {
	// Quick lexical fingerprints. We require multiple matches before
	// declaring fish, to avoid misfiring on bash scripts that happen
	// to contain "end" in a string.
	score := 0
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "end":
			score += 2
		case strings.HasPrefix(t, "set -l "), strings.HasPrefix(t, "set -g "), strings.HasPrefix(t, "set -x "):
			score += 2
		case strings.HasPrefix(t, "function ") && strings.Contains(line, " "):
			score++
		}
	}
	return score >= 2
}

// LoadFile reads a script from disk and returns (path, source).
// Convenience wrapper used by the built-ins.
func LoadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
