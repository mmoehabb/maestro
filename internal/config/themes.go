package config

// Theme describes one of the four shipped palettes. Auto is a selection mode,
// not a fifth palette; dark/light IDs remain compatible with existing configs.
type Theme struct {
	ID, Name, Description string
	Palette               Palette
}

func BuiltinThemes() []Theme {
	return []Theme{
		{"dark", "Forest", "Forest green, peach accents, sage highlights", Palette{Background: "#242b26", Foreground: "#e5e8dd", Muted: "#aab5a8", Accent: "#e4a78c", Success: "#b8ce96", Warning: "#e4cb7e", Error: "#e58d83", Merged: "#c2add4"}},
		{"light", "Paper", "Warm cream, dark ink, terracotta accents", Palette{Background: "#f5f2e9", Foreground: "#292d26", Muted: "#65685e", Accent: "#b73c29", Success: "#49683b", Warning: "#856619", Error: "#a53329", Merged: "#77578a"}},
		{"catppuccin", "Catppuccin", "Soft charcoal with pastel blue and lavender", Palette{Background: "#1e1e2e", Foreground: "#cdd6f4", Muted: "#a6adc8", Accent: "#89b4fa", Success: "#a6e3a1", Warning: "#f9e2af", Error: "#f38ba8", Merged: "#cba6f7"}},
		{"tokyo-night", "Tokyo Night", "Deep navy, cool blue, and violet", Palette{Background: "#1a1b26", Foreground: "#c0caf5", Muted: "#9aa5ce", Accent: "#7aa2f7", Success: "#9ece6a", Warning: "#e0af68", Error: "#f7768e", Merged: "#bb9af7"}},
	}
}

func BuiltinTheme(id string) (Theme, bool) {
	for _, t := range BuiltinThemes() {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}
