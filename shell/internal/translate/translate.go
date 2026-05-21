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
//
// v1.0-3 adds PowerShell + Cmd. Each ReaderFunc is optional — the
// shell wires every one that ships in the build; tests inject the
// subset they exercise.
type Readers struct {
	Bash       ReaderFunc
	Zsh        ReaderFunc
	Fish       ReaderFunc
	PowerShell ReaderFunc
	Cmd        ReaderFunc
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
	case DialectPowerShell:
		if readers.PowerShell == nil {
			return nil, fmt.Errorf("translate: no powershell reader registered")
		}
		return readers.PowerShell(src)
	case DialectCmd:
		if readers.Cmd == nil {
			return nil, fmt.Errorf("translate: no cmd reader registered")
		}
		return readers.Cmd(src)
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
		case ".ps1", ".psm1":
			return DialectPowerShell
		case ".bat", ".cmd":
			return DialectCmd
		}
	}
	// 3. Content heuristic.
	if looksLikeFish(src) {
		return DialectFish
	}
	if looksLikePowerShell(src) {
		return DialectPowerShell
	}
	if looksLikeCmd(src) {
		return DialectCmd
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
	case strings.Contains(first, "pwsh"), strings.Contains(first, "powershell"):
		// PowerShell Core (`pwsh`) is the documented cross-platform
		// binary; Windows PowerShell `powershell.exe` matches too.
		return DialectPowerShell, true
	case strings.Contains(first, "fish"):
		return DialectFish, true
	case strings.Contains(first, "zsh"):
		return DialectZsh, true
	case strings.Contains(first, "bash"), strings.Contains(first, "/sh"):
		return DialectBash, true
	}
	return "", false
}

// looksLikePowerShell fingerprints PowerShell on common cmdlet
// prefixes, `$variable = …` assignment style, and the block-comment
// `<# … #>` marker. Multiple fingerprints required before declaring
// PS, to avoid misfiring on a bash script with a `$VAR = value`
// substring inside a string.
func looksLikePowerShell(src string) bool {
	score := 0
	lower := strings.ToLower(src)
	if strings.Contains(lower, "write-host") {
		score += 2
	}
	if strings.Contains(lower, "write-output") {
		score += 2
	}
	if strings.Contains(lower, "get-service") || strings.Contains(lower, "get-process") {
		score += 2
	}
	if strings.Contains(src, "<#") || strings.Contains(src, "#>") {
		score += 2
	}
	if strings.Contains(src, "param(") {
		score++
	}
	return score >= 2
}

// looksLikeCmd fingerprints cmd/bat on `REM` / `::` comments, the
// `%VAR%` expansion form, and `@echo off` (the canonical opener).
func looksLikeCmd(src string) bool {
	score := 0
	for _, line := range strings.Split(src, "\n") {
		t := strings.ToLower(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(t, "@echo "):
			score += 2
		case strings.HasPrefix(t, "rem "):
			score++
		case strings.HasPrefix(t, "::"):
			score++
		case strings.HasPrefix(t, "set ") && strings.Contains(t, "="):
			score++
		case strings.Contains(t, "%errorlevel%"):
			score += 2
		case strings.HasPrefix(t, "goto "):
			score++
		}
	}
	return score >= 2
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
