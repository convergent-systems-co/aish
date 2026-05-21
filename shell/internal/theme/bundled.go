package theme

import proto "github.com/convergent-systems-co/aish/libs/proto/theme"

// BundledBrands returns the brands shipped with every aish binary. They
// are the starter set so the shell is usable with zero network access
// and zero filesystem state.
//
// The ten-brand launch set covers the breadth of the popular palette
// space: a monochrome floor (default), Nord (powerline showcase),
// Monokai (classic warm), Dracula (high-contrast dark), Solarized
// (light/dark pair, the de-facto sRGB-balanced palettes), Gruvbox
// (retro warm), Tokyo Night (modern cool dark), Catppuccin (pastel
// dark), One Dark (Atom's flagship palette).
//
// Long-term, Brand Atoms ships hundreds of brands via theme-atoms.com;
// the bundled set stays as the reliable cold-start floor.
func BundledBrands() []proto.Brand {
	return []proto.Brand{
		defaultBrand(),
		nordPowerlineBrand(),
		monokaiBrand(),
		draculaBrand(),
		solarizedDarkBrand(),
		solarizedLightBrand(),
		gruvboxDarkBrand(),
		tokyoNightBrand(),
		catppuccinBrand(),
		oneDarkBrand(),
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

// draculaBrand — the popular high-contrast dark palette from
// https://draculatheme.com. Wide ecosystem support; chosen as the
// "every editor has this theme" baseline.
func draculaBrand() proto.Brand {
	return proto.Brand{
		Name: "dracula",
		Type: "shell",
		Palette: proto.Palette{
			"background": "#282a36",
			"current":    "#44475a",
			"foreground": "#f8f8f2",
			"comment":    "#6272a4",
			"cyan":       "#8be9fd",
			"green":      "#50fa7b",
			"orange":     "#ffb86c",
			"pink":       "#ff79c6",
			"purple":     "#bd93f9",
			"red":        "#ff5555",
			"yellow":     "#f1fa8c",
			"primary":    "#bd93f9",
			"accent":     "#ff79c6",
			"muted":      "#6272a4",
			"error":      "#ff5555",
			"success":    "#50fa7b",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.purple",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": "❯",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "→",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}

// solarizedDarkBrand — Ethan Schoonover's Solarized, dark variant.
// Color-relationship-balanced palette designed for screen-and-print
// parity. Half of the bundled light/dark pair.
func solarizedDarkBrand() proto.Brand {
	return proto.Brand{
		Name: "solarized-dark",
		Type: "shell",
		Palette: proto.Palette{
			"base03":  "#002b36",
			"base02":  "#073642",
			"base01":  "#586e75",
			"base00":  "#657b83",
			"base0":   "#839496",
			"base1":   "#93a1a1",
			"base2":   "#eee8d5",
			"base3":   "#fdf6e3",
			"yellow":  "#b58900",
			"orange":  "#cb4b16",
			"red":     "#dc322f",
			"magenta": "#d33682",
			"violet":  "#6c71c4",
			"blue":    "#268bd2",
			"cyan":    "#2aa198",
			"green":   "#859900",
			"primary": "#268bd2",
			"accent":  "#2aa198",
			"muted":   "#586e75",
			"error":   "#dc322f",
			"success": "#859900",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.blue",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": "→",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "→",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}

// solarizedLightBrand — the light variant of Solarized; the only
// bundled brand intended for light terminal backgrounds. Same palette
// hex codes, inverted base mapping.
func solarizedLightBrand() proto.Brand {
	return proto.Brand{
		Name: "solarized-light",
		Type: "shell",
		Palette: proto.Palette{
			"base03":  "#002b36",
			"base02":  "#073642",
			"base01":  "#586e75",
			"base00":  "#657b83",
			"base0":   "#839496",
			"base1":   "#93a1a1",
			"base2":   "#eee8d5",
			"base3":   "#fdf6e3",
			"yellow":  "#b58900",
			"orange":  "#cb4b16",
			"red":     "#dc322f",
			"magenta": "#d33682",
			"violet":  "#6c71c4",
			"blue":    "#268bd2",
			"cyan":    "#2aa198",
			"green":   "#859900",
			"primary": "#268bd2",
			"accent":  "#d33682",
			"muted":   "#93a1a1",
			"error":   "#dc322f",
			"success": "#859900",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.blue",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": "→",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "→",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}

// gruvboxDarkBrand — Pavel Pertsev's Gruvbox, dark variant. Retro
// warm palette, popular for its high-saturation comfort on long
// sessions.
func gruvboxDarkBrand() proto.Brand {
	return proto.Brand{
		Name: "gruvbox-dark",
		Type: "shell",
		Palette: proto.Palette{
			"bg":      "#282828",
			"bg1":     "#3c3836",
			"fg":      "#ebdbb2",
			"fg2":     "#d5c4a1",
			"red":     "#fb4934",
			"green":   "#b8bb26",
			"yellow":  "#fabd2f",
			"blue":    "#83a598",
			"purple":  "#d3869b",
			"aqua":    "#8ec07c",
			"orange":  "#fe8019",
			"gray":    "#928374",
			"primary": "#fabd2f",
			"accent":  "#fe8019",
			"muted":   "#928374",
			"error":   "#fb4934",
			"success": "#b8bb26",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.yellow",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": "$",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "→",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}

// tokyoNightBrand — folke/tokyonight.nvim's flagship dark palette.
// Modern cool-toned alternative to Dracula; popular on Neovim and
// VS Code.
func tokyoNightBrand() proto.Brand {
	return proto.Brand{
		Name:    "tokyo-night",
		Type:    "shell",
		Extends: "brands/general/tokyo-night",
		Palette: proto.Palette{
			"bg":      "#1a1b26",
			"bg_dark": "#16161e",
			"fg":      "#c0caf5",
			"fg_dark": "#a9b1d6",
			"blue":    "#7aa2f7",
			"cyan":    "#7dcfff",
			"green":   "#9ece6a",
			"magenta": "#bb9af7",
			"orange":  "#ff9e64",
			"red":     "#f7768e",
			"yellow":  "#e0af68",
			"comment": "#565f89",
			"primary": "#7aa2f7",
			"accent":  "#bb9af7",
			"muted":   "#565f89",
			"error":   "#f7768e",
			"success": "#9ece6a",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.blue",
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
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "powerline",
			Font:       "jetbrainsmono-nerdfont",
		},
	}
}

// catppuccinBrand — the "Mocha" flavor of catppuccin/palette. Pastel
// dark palette that's exploded in popularity since 2023; the modern
// pastel counterweight to Nord's frost-toned cool.
func catppuccinBrand() proto.Brand {
	return proto.Brand{
		Name:    "catppuccin",
		Type:    "shell",
		Extends: "brands/general/catppuccin-mocha",
		Palette: proto.Palette{
			"base":      "#1e1e2e",
			"mantle":    "#181825",
			"crust":     "#11111b",
			"text":      "#cdd6f4",
			"subtext":   "#bac2de",
			"overlay":   "#9399b2",
			"surface":   "#45475a",
			"rosewater": "#f5e0dc",
			"flamingo":  "#f2cdcd",
			"pink":      "#f5c2e7",
			"mauve":     "#cba6f7",
			"red":       "#f38ba8",
			"maroon":    "#eba0ac",
			"peach":     "#fab387",
			"yellow":    "#f9e2af",
			"green":     "#a6e3a1",
			"teal":      "#94e2d5",
			"sky":       "#89dceb",
			"sapphire":  "#74c7ec",
			"blue":      "#89b4fa",
			"lavender":  "#b4befe",
			"primary":   "#cba6f7",
			"accent":    "#f5c2e7",
			"muted":     "#9399b2",
			"error":     "#f38ba8",
			"success":   "#a6e3a1",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.mauve",
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
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "powerline",
			Font:       "jetbrainsmono-nerdfont",
		},
	}
}

// oneDarkBrand — Atom's flagship dark palette, later ported to most
// editors. The "professional default" of modern editor themes.
func oneDarkBrand() proto.Brand {
	return proto.Brand{
		Name: "one-dark",
		Type: "shell",
		Palette: proto.Palette{
			"background": "#282c34",
			"foreground": "#abb2bf",
			"cursor":     "#5c6370",
			"red":        "#e06c75",
			"green":      "#98c379",
			"yellow":     "#e5c07b",
			"blue":       "#61afef",
			"magenta":    "#c678dd",
			"cyan":       "#56b6c2",
			"comment":    "#5c6370",
			"primary":    "#61afef",
			"accent":     "#c678dd",
			"muted":      "#5c6370",
			"error":      "#e06c75",
			"success":    "#98c379",
		},
		Roles: proto.Roles{
			"prompt":  "$palette.blue",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
			"muted":   "$palette.muted",
			"error":   "$palette.error",
			"success": "$palette.success",
		},
		Glyphs: proto.Glyphs{
			Static: map[string]string{
				"prompt_char": "❯",
				"git_clean":   "",
				"git_dirty":   "*",
				"arrow":       "→",
			},
		},
		Prompt: proto.PromptConfig{
			Segments:   []string{"cwd", "git-status", "exit-code", "prompt"},
			Separators: "minimal",
			Font:       "monospace",
		},
	}
}
