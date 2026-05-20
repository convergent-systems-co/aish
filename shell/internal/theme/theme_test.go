package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	proto "github.com/convergent-systems-co/aish/libs/proto/theme"
)

// ---------- Compile ----------

func TestCompile_BasicShape(t *testing.T) {
	b := proto.Brand{
		Name: "test",
		Type: "shell",
		Palette: proto.Palette{
			"primary": "#88c0d0",
			"accent":  "#a3be8c",
		},
		Roles: proto.Roles{
			"prompt": "$palette.primary",
			"accent": "$palette.accent",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{"prompt_char": "❯"},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "prompt"},
			Separators: "powerline",
		},
	}
	tm, err := Compile(b)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	if tm.Name() != "test" {
		t.Errorf("Name() = %q, want %q", tm.Name(), "test")
	}
	if got := tm.Glyph("prompt_char", "?"); got != "❯" {
		t.Errorf("Glyph(prompt_char) = %q, want %q", got, "❯")
	}
	if got := tm.Glyph("missing", "default"); got != "default" {
		t.Errorf("Glyph(missing) = %q, want fallback %q", got, "default")
	}
	if got := tm.Segments(); len(got) != 2 || got[0] != "cwd" || got[1] != "prompt" {
		t.Errorf("Segments() = %v, want [cwd prompt]", got)
	}
}

func TestCompile_RejectsNameless(t *testing.T) {
	if _, err := Compile(proto.Brand{}); err == nil {
		t.Fatal("Compile(empty brand) returned nil error; want non-nil")
	}
}

func TestCompile_ColorPromptWrapsInAnsi(t *testing.T) {
	b := proto.Brand{
		Name: "test",
		Palette: proto.Palette{
			"primary": "#88c0d0",
		},
		Roles: proto.Roles{
			"prompt": "$palette.primary",
		},
	}
	tm, _ := Compile(b)
	got := tm.ColorPrompt("hi")
	if !strings.HasPrefix(got, "\x1b[") {
		t.Errorf("ColorPrompt(%q) = %q; want ANSI prefix", "hi", got)
	}
	if !strings.HasSuffix(got, AnsiReset) {
		t.Errorf("ColorPrompt(%q) = %q; want AnsiReset suffix", "hi", got)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("ColorPrompt(%q) = %q; want inner string", "hi", got)
	}
}

func TestCompile_NoPromptColorPassesThrough(t *testing.T) {
	b := proto.Brand{Name: "bare"}
	tm, _ := Compile(b)
	if got := tm.ColorPrompt("hi"); got != "hi" {
		t.Errorf("ColorPrompt on theme with no prompt color = %q, want %q", got, "hi")
	}
}

func TestCompile_InvalidHexDropsRole(t *testing.T) {
	b := proto.Brand{
		Name: "broken",
		Palette: proto.Palette{
			"primary": "not-a-hex-color",
		},
		Roles: proto.Roles{
			"prompt": "$palette.primary",
		},
	}
	tm, err := Compile(b)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	// Invalid hex → ColorPrompt is a no-op.
	if got := tm.ColorPrompt("hi"); got != "hi" {
		t.Errorf("ColorPrompt with invalid hex = %q, want fallthrough %q", got, "hi")
	}
}

// ---------- hexToAnsi ----------

func TestHexToAnsi(t *testing.T) {
	tests := []struct {
		hex  string
		want string
	}{
		{"#88c0d0", "\x1b[38;2;136;192;208m"},
		{"#000000", "\x1b[38;2;0;0;0m"},
		{"#ffffff", "\x1b[38;2;255;255;255m"},
		{"", ""},
		{"#88c0d", ""},    // too short
		{"#88c0d0aa", ""}, // too long
		{"88c0d0", ""},    // missing leading #
		{"#XXYYZZ", ""},   // invalid hex
	}
	for _, tc := range tests {
		t.Run(tc.hex, func(t *testing.T) {
			if got := hexToAnsi(tc.hex); got != tc.want {
				t.Errorf("hexToAnsi(%q) = %q, want %q", tc.hex, got, tc.want)
			}
		})
	}
}

// ---------- Registry ----------

func TestRegistry_BundledLoad(t *testing.T) {
	r := NewRegistry()
	names := r.List()
	// Bundled set MUST include "default" — that's the fallback contract.
	found := false
	for _, n := range names {
		if n == "default" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("NewRegistry should bundle %q; got names %v", "default", names)
	}
	if r.Active() == nil || r.Active().Name() != "default" {
		t.Errorf("NewRegistry should activate %q by default; got %v", "default", r.Active())
	}
}

func TestRegistry_LookupUnknown(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup("definitely-not-a-theme-xyzzy"); ok {
		t.Error("Lookup of unknown name should return ok=false")
	}
}

func TestRegistry_SetActive(t *testing.T) {
	r := NewRegistry()
	if err := r.SetActive("nord-powerline"); err != nil {
		t.Fatalf("SetActive(nord-powerline) error: %v", err)
	}
	if r.Active().Name() != "nord-powerline" {
		t.Errorf("Active().Name() = %q, want %q", r.Active().Name(), "nord-powerline")
	}
	if err := r.SetActive("never-bundled"); err == nil {
		t.Error("SetActive(unknown) should return error")
	}
}

// ---------- Config persistence ----------

func TestReadWriteActiveTheme(t *testing.T) {
	tmp := t.TempDir()
	if got := ReadActiveTheme(tmp); got != "" {
		t.Errorf("ReadActiveTheme on empty dir = %q, want empty", got)
	}
	if err := WriteActiveTheme(tmp, "nord-powerline"); err != nil {
		t.Fatalf("WriteActiveTheme: %v", err)
	}
	if got := ReadActiveTheme(tmp); got != "nord-powerline" {
		t.Errorf("ReadActiveTheme = %q, want %q", got, "nord-powerline")
	}
	// File exists at the documented path.
	path := filepath.Join(tmp, ".aish", "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config.toml not at %s: %v", path, err)
	}
}

func TestReadActiveTheme_TolerantParsing(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".aish")
	_ = os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "config.toml")
	content := `# a comment
# another comment

[other]
key = "ignore-me"

[theme]
# pre-key comment
active = "nord-powerline"
`
	_ = os.WriteFile(path, []byte(content), 0o644)
	if got := ReadActiveTheme(tmp); got != "nord-powerline" {
		t.Errorf("ReadActiveTheme tolerant parse = %q, want %q", got, "nord-powerline")
	}
}

func TestReadActiveTheme_EmptyHomeReturnsEmpty(t *testing.T) {
	if got := ReadActiveTheme(""); got != "" {
		t.Errorf("ReadActiveTheme(empty) = %q, want empty", got)
	}
}

func TestWriteActiveTheme_EmptyHomeErrors(t *testing.T) {
	if err := WriteActiveTheme("", "x"); err == nil {
		t.Error("WriteActiveTheme with empty home should error")
	}
}

func TestWriteActiveTheme_EmptyNameErrors(t *testing.T) {
	if err := WriteActiveTheme(t.TempDir(), ""); err == nil {
		t.Error("WriteActiveTheme with empty name should error")
	}
}
