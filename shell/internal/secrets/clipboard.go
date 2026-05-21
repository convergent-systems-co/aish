package secrets

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// ErrNoClipboard is returned when no clipboard binary is available
// on PATH. The caller MUST surface this loudly to the user — falling
// back to stdout would leak the secret per Common.md §4.
var ErrNoClipboard = errors.New("secrets: no clipboard utility found on PATH")

// CopyToClipboard writes value to the OS clipboard using the
// first-available platform-appropriate binary:
//
//   - darwin  → pbcopy
//   - linux   → wl-copy (Wayland), then xclip -selection clipboard
//   - windows → powershell Set-Clipboard
//
// The value is written via the child process's stdin pipe; it never
// appears on argv, in env, or in this process's stdout/stderr.
// The caller MUST NOT call Zero on value before this returns — the
// child consumes the pipe.
func CopyToClipboard(value []byte) error {
	cmd, args, err := pickClipboardCmd(runtime.GOOS, func(name string) bool {
		_, lookErr := exec.LookPath(name)
		return lookErr == nil
	})
	if err != nil {
		return err
	}
	c := exec.Command(cmd, args...)
	// Inherit a clean environment. The clipboard binary doesn't need
	// the parent's env beyond DISPLAY / WAYLAND_DISPLAY which it
	// already inherits from the process default.
	stdin, err := c.StdinPipe()
	if err != nil {
		return fmt.Errorf("secrets: stdin pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		return fmt.Errorf("secrets: start clipboard: %w", err)
	}
	if _, err := stdin.Write(value); err != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return fmt.Errorf("secrets: write clipboard: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = c.Wait()
		return fmt.Errorf("secrets: close clipboard stdin: %w", err)
	}
	if err := c.Wait(); err != nil {
		return fmt.Errorf("secrets: clipboard exit: %w", err)
	}
	return nil
}

// pickClipboardCmd returns (binary, args) for the first available
// clipboard utility on this platform. Factored out so tests can
// inject a fake PATH-lookup function.
//
// Preference order:
//   - darwin: pbcopy (always present)
//   - linux: wl-copy, then xclip
//   - windows: powershell Set-Clipboard
//
// Anything else (freebsd / openbsd / etc) tries wl-copy/xclip; if
// neither is present, ErrNoClipboard.
func pickClipboardCmd(goos string, have func(string) bool) (string, []string, error) {
	type opt struct {
		bin  string
		args []string
	}
	var candidates []opt
	switch goos {
	case "darwin":
		candidates = []opt{{bin: "pbcopy"}}
	case "linux":
		candidates = []opt{
			{bin: "wl-copy"},
			{bin: "xclip", args: []string{"-selection", "clipboard"}},
			{bin: "xsel", args: []string{"--clipboard", "--input"}},
		}
	case "windows":
		// PowerShell's Set-Clipboard reads stdin via a pipeline:
		// `powershell -Command Set-Clipboard` will read until EOF.
		// We use -NoProfile to avoid loading user PS profiles.
		candidates = []opt{
			{bin: "powershell", args: []string{"-NoProfile", "-Command", "Set-Clipboard"}},
		}
	default:
		candidates = []opt{
			{bin: "wl-copy"},
			{bin: "xclip", args: []string{"-selection", "clipboard"}},
		}
	}
	for _, c := range candidates {
		if have(c.bin) {
			return c.bin, c.args, nil
		}
	}
	return "", nil, ErrNoClipboard
}
