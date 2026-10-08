package config

import (
	"fmt"
	"regexp"
)

// Palette uses semantic colors shared by all Maestro chrome and views.
type Palette struct {
	Background string `toml:"background"`
	Foreground string `toml:"foreground"`
	Muted      string `toml:"muted"`
	Accent     string `toml:"accent"`
	Success    string `toml:"success"`
	Warning    string `toml:"warning"`
	Error      string `toml:"error"`
	Merged     string `toml:"merged"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (c Config) validatePresentation() error {
	if _, ok := BuiltinTheme(c.Theme); !ok && c.Theme != "auto" {
		if _, ok := c.Themes[c.Theme]; !ok {
			return fmt.Errorf("unknown theme %q", c.Theme)
		}
	}
	for name, p := range c.Themes {
		if _, builtin := BuiltinTheme(name); name == "" || name == "auto" || builtin {
			return fmt.Errorf("custom theme name %q is reserved or empty", name)
		}
		for key, color := range map[string]string{"background": p.Background, "foreground": p.Foreground, "muted": p.Muted, "accent": p.Accent, "success": p.Success, "warning": p.Warning, "error": p.Error, "merged": p.Merged} {
			if !hexColor.MatchString(color) {
				return fmt.Errorf("theme %s.%s must be a #RRGGBB color", name, key)
			}
		}
	}
	for _, event := range c.Activity.NotifyOn {
		if !oneOf(event, "done", "needs_input") {
			return fmt.Errorf("invalid activity.notify_on event %q", event)
		}
	}
	return nil
}
