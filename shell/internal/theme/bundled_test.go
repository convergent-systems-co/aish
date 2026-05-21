package theme

import (
	"strings"
	"testing"
)

// TestBundledBrands_TenAtLaunch — #77 requires ten curated shell brands
// shipped as the cold-start floor. The set MUST include the v0.1
// originals plus seven additions covering the popular palette space.
func TestBundledBrands_TenAtLaunch(t *testing.T) {
	got := BundledBrands()
	if len(got) < 10 {
		t.Fatalf("BundledBrands count = %d, want >= 10", len(got))
	}

	want := []string{
		"default", "nord-powerline", "monokai",
		"dracula", "solarized-dark", "solarized-light",
		"gruvbox-dark", "tokyo-night", "catppuccin", "one-dark",
	}
	have := map[string]bool{}
	for _, b := range got {
		have[b.Name] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("BundledBrands missing %q", n)
		}
	}
}

// TestBundledBrands_AllCompile — every bundled brand must compile
// without error. A bundled brand that doesn't compile is a defect — the
// "cold-start floor" promise is broken.
func TestBundledBrands_AllCompile(t *testing.T) {
	for _, b := range BundledBrands() {
		t.Run(b.Name, func(t *testing.T) {
			tm, err := Compile(b)
			if err != nil {
				t.Fatalf("Compile(%s): %v", b.Name, err)
			}
			if tm.Name() != b.Name {
				t.Errorf("compiled name = %q, want %q", tm.Name(), b.Name)
			}
			if len(tm.Segments()) == 0 {
				t.Errorf("%s: Segments() is empty; brand must declare at least one", b.Name)
			}
			// Prompt char must always resolve, even on minimal brands.
			if got := tm.Glyph("prompt_char", ""); got == "" {
				t.Errorf("%s: prompt_char glyph is empty", b.Name)
			}
		})
	}
}

// TestBundledBrands_AllValidate — every bundled brand must pass
// `proto.Brand.Validate()`. This is the contract the catalog enforces;
// our own bundle is the strictest test of it.
func TestBundledBrands_AllValidate(t *testing.T) {
	for _, b := range BundledBrands() {
		t.Run(b.Name, func(t *testing.T) {
			if err := b.Validate(); err != nil {
				t.Errorf("%s: Validate() = %v, want nil", b.Name, err)
			}
		})
	}
}

// TestBundledBrands_RegistryRegistersAll — after NewRegistry, every
// BundledBrands entry must be Lookup-able. The active default must be
// the well-known "default".
func TestBundledBrands_RegistryRegistersAll(t *testing.T) {
	r := NewRegistry()
	for _, b := range BundledBrands() {
		if _, ok := r.Lookup(b.Name); !ok {
			t.Errorf("Registry missing bundled brand %q", b.Name)
		}
	}
	if r.Active() == nil || r.Active().Name() != DefaultThemeName {
		t.Errorf("Active() = %v, want %q", r.Active(), DefaultThemeName)
	}
}

// TestBundledBrands_PaletteHexesAreValid — every palette entry across
// every bundled brand must parse as a 6-digit hex. Catches typos like
// "#88c0d" or "primary" in palette values.
func TestBundledBrands_PaletteHexesAreValid(t *testing.T) {
	for _, b := range BundledBrands() {
		for k, v := range b.Palette {
			if !strings.HasPrefix(v, "#") || len(v) != 7 {
				t.Errorf("%s palette[%s] = %q is not a 6-digit hex", b.Name, k, v)
			}
		}
	}
}
