package secrets

import (
	"errors"
	"testing"
)

// TestClipboardCommand_PicksFirstAvailable — given a PATH lookup
// stub, the command-detection logic picks the first binary in the
// platform-specific preference order.
func TestClipboardCommand_PicksFirstAvailable(t *testing.T) {
	have := func(name string) bool {
		return name == "wl-copy" // pretend only wl-copy is on PATH
	}
	cmd, args, err := pickClipboardCmd("linux", have)
	if err != nil {
		t.Fatalf("pickClipboardCmd: %v", err)
	}
	if cmd != "wl-copy" {
		t.Errorf("cmd = %q; want %q", cmd, "wl-copy")
	}
	if len(args) != 0 {
		t.Errorf("args = %v; want empty", args)
	}
}

// TestClipboardCommand_NoBinaryAvailable — when no clipboard binary
// is on PATH, pickClipboardCmd returns ErrNoClipboard. The caller
// MUST fail loudly (no stdout fallback).
func TestClipboardCommand_NoBinaryAvailable(t *testing.T) {
	have := func(name string) bool { return false }
	_, _, err := pickClipboardCmd("linux", have)
	if !errors.Is(err, ErrNoClipboard) {
		t.Fatalf("err = %v; want ErrNoClipboard", err)
	}
}

// TestClipboardCommand_DarwinUsesPbcopy — pbcopy is the macOS native
// clipboard binary and is always installed.
func TestClipboardCommand_DarwinUsesPbcopy(t *testing.T) {
	have := func(name string) bool { return true }
	cmd, _, err := pickClipboardCmd("darwin", have)
	if err != nil {
		t.Fatalf("pickClipboardCmd: %v", err)
	}
	if cmd != "pbcopy" {
		t.Errorf("cmd = %q; want pbcopy on darwin", cmd)
	}
}

// TestClipboardCommand_LinuxPrefersWaylandThenX11 — on Linux,
// wl-copy is preferred over xclip when both are available, because
// Wayland is the modern target.
func TestClipboardCommand_LinuxPrefersWaylandThenX11(t *testing.T) {
	have := func(name string) bool { return true }
	cmd, _, err := pickClipboardCmd("linux", have)
	if err != nil {
		t.Fatalf("pickClipboardCmd: %v", err)
	}
	if cmd != "wl-copy" {
		t.Errorf("cmd = %q; want wl-copy preferred over xclip", cmd)
	}
}

// TestClipboardCommand_XclipUsesSelectionClipboardArg — xclip without
// "-selection clipboard" writes to the X11 PRIMARY selection (paste
// with middle-click) instead of the clipboard (paste with Ctrl-V).
// We always pass the clipboard arg.
func TestClipboardCommand_XclipUsesSelectionClipboardArg(t *testing.T) {
	have := func(name string) bool { return name == "xclip" }
	cmd, args, err := pickClipboardCmd("linux", have)
	if err != nil {
		t.Fatalf("pickClipboardCmd: %v", err)
	}
	if cmd != "xclip" {
		t.Fatalf("cmd = %q; want xclip", cmd)
	}
	wantArgs := []string{"-selection", "clipboard"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v; want %v", args, wantArgs)
	}
	for i := range args {
		if args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q; want %q", i, args[i], wantArgs[i])
		}
	}
}
