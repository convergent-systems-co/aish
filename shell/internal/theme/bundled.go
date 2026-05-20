package theme

import proto "github.com/convergent-systems-co/aish/libs/proto/theme"

// BundledBrands returns the brands shipped with every aish binary. They
// are the starter set so the shell is usable with zero network access
// and zero filesystem state.
//
// Long-term, Brand Atoms ships hundreds of brands via theme-atoms.com;
// the bundled set stays as the reliable cold-start floor.
func BundledBrands() []proto.Brand {
	return []proto.Brand{
		defaultBrand(),
		nordPowerlineBrand(),
		monokaiBrand(),
	}
}

// defaultBrand is the no-Nerd-Font monochrome floor. Works on any
// terminal, on any platform, with any font. The single goal: be safe.
func defaultBrand() proto.Brand {
	return proto.Brand{
		Name: "default",
		Type: "shell",
		Palette: proto.Palette{
			"primary": "#7fb3d5",
			"accent":  "#a3be8c",
			"muted":   "#6c757d",
			"error":   "#bf616a",
			"success": "#a3be8c",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.primary",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": ">",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "->",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}

// nordPowerlineBrand is the rich Nerd-Font + Powerline-separator
// flagship theme. Requires a Nerd-Font-capable terminal to render the
// glyphs correctly; falls back gracefully on default terminals (the
// glyphs render as boxes but the colors still apply).
func nordPowerlineBrand() proto.Brand {
	return proto.Brand{
		Name:    "nord-powerline",
		Type:    "shell",
		Extends: "brands/general/nord",
		Palette: proto.Palette{
			// Nord palette — https://www.nordtheme.com/
			"nord0":   "#2e3440", // polar night
			"nord1":   "#3b4252",
			"nord2":   "#434c5e",
			"nord3":   "#4c566a",
			"nord4":   "#d8dee9", // snow storm
			"nord5":   "#e5e9f0",
			"nord6":   "#eceff4",
			"nord7":   "#8fbcbb", // frost
			"nord8":   "#88c0d0",
			"nord9":   "#81a1c1",
			"nord10":  "#5e81ac",
			"nord11":  "#bf616a", // aurora
			"nord12":  "#d08770",
			"nord13":  "#ebcb8b",
			"nord14":  "#a3be8c",
			"nord15":  "#b48ead",
			"primary": "#88c0d0",
			"accent":  "#a3be8c",
			"muted":   "#4c566a",
			"error":   "#bf616a",
			"success": "#a3be8c",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.nord8",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			FiletypeMap: "nerd-default",
			Static: map[string]string{
				"prompt_char": "❯",
				"git_clean":   "",
				"git_dirty":   "±",
				"arrow":       "→",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "prompt"},
			Separators: "powerline",
			Font:       "jetbrainsmono-nerdfont",
		},
	}
}

// monokaiBrand is the classic-warm fallback — high-contrast, no Nerd
// Font required. The pair-of-bundled-light-and-dark intent.
func monokaiBrand() proto.Brand {
	return proto.Brand{
		Name: "monokai",
		Type: "shell",
		Palette: proto.Palette{
			"background": "#272822",
			"foreground": "#f8f8f2",
			"pink":       "#f92672",
			"green":      "#a6e22e",
			"yellow":     "#e6db74",
			"blue":       "#66d9ef",
			"orange":     "#fd971f",
			"purple":     "#ae81ff",
			"comment":    "#75715e",
			"primary":    "#a6e22e",
			"accent":     "#f92672",
			"muted":      "#75715e",
			"error":      "#f92672",
			"success":    "#a6e22e",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.green",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": "λ",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "=>",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}
