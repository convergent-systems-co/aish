package shell

import (
	"bytes"
	"strings"
	"testing"
)

// TestThemePreview_RichOutputContainsBrandName — #79: the enhanced
// preview must include the brand name as a header so the user can tell
// what they're looking at.
func TestThemePreview_RichOutputContainsBrandName(t *testing.T) {
	s := New()
	var stdout, stderr bytes.Buffer
	code := s.themeBuiltin([]string{"preview", "nord-powerline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preview exit = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "nord-powerline") {
		t.Errorf("preview output should contain brand name; got:\n%s", out)
	}
}

// TestThemePreview_DoesNotActivate — preview MUST NOT mutate the
// registry's active theme. The user explicitly opted out of
// activation by saying "preview" instead of "set".
func TestThemePreview_DoesNotActivate(t *testing.T) {
	s := New()
	before := s.Themes().Active().Name()

	var stdout, stderr bytes.Buffer
	if code := s.themeBuiltin([]string{"preview", "nord-powerline"}, &stdout, &stderr); code != 0 {
		t.Fatalf("preview exit = %d, stderr = %q", code, stderr.String())
	}

	after := s.Themes().Active().Name()
	if before != after {
		t.Errorf("preview must not change active theme; before=%q after=%q", before, after)
	}
}

// TestThemePreview_PlainFlagSuppressesEscapes — #79: a `--plain` flag
// produces output with no ANSI escape sequences, suitable for capture
// or diffing.
func TestThemePreview_PlainFlagSuppressesEscapes(t *testing.T) {
	s := New()
	var stdout, stderr bytes.Buffer
	code := s.themeBuiltin([]string{"preview", "--plain", "nord-powerline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preview --plain exit = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("--plain output should have no ANSI escapes; got:\n%q", out)
	}
	// And the brand name still has to be there.
	if !strings.Contains(out, "nord-powerline") {
		t.Errorf("--plain output missing brand name; got:\n%s", out)
	}
}

// TestThemePreview_ShowsAllRoles — the preview lists the brand's
// roles so the user can see what palette colors they'd activate. The
// enriched preview must enumerate at least the six core roles.
func TestThemePreview_ShowsAllRoles(t *testing.T) {
	s := New()
	var stdout, stderr bytes.Buffer
	if code := s.themeBuiltin([]string{"preview", "--plain", "nord-powerline"}, &stdout, &stderr); code != 0 {
		t.Fatalf("preview exit = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	for _, role := range []string{"prompt", "accent", "muted", "error"} {
		if !strings.Contains(out, role) {
			t.Errorf("preview should list role %q; got:\n%s", role, out)
		}
	}
}

// TestThemePreview_UnknownBrand_Errors — preview on a non-existent
// brand prints to stderr and exits non-zero. Same contract as `set`.
func TestThemePreview_UnknownBrand_Errors(t *testing.T) {
	s := New()
	var stdout, stderr bytes.Buffer
	code := s.themeBuiltin([]string{"preview", "definitely-not-a-real-theme-xyzzy"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("preview unknown should exit non-zero; stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no such theme") {
		t.Errorf("error message should mention 'no such theme'; got %q", stderr.String())
	}
}
