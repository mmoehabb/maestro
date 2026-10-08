package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/config"
)

type themePicker struct {
	selected                   int
	original, path, scope, err string
	busy                       bool
}
type themeSavedMsg struct {
	picker *themePicker
	err    error
}

func (m *Model) openThemes() tea.Cmd {
	d := &themePicker{original: m.cfg.Theme, path: config.DefaultPaths().ConfigFile, scope: "global configuration"}
	if m.service.Repo.Root != "" {
		local := filepath.Join(m.service.Repo.Root, ".maestro.toml")
		override, err := config.FileTheme(local)
		if err != nil {
			m.notify("Read repository theme: " + err.Error())
			return nil
		}
		if override != "" {
			d.path = local
			d.scope = "repository configuration"
		}
	}
	selected := m.cfg.Theme
	if selected == "auto" {
		selected = "dark"
		if m.light {
			selected = "light"
		}
	}
	for i, t := range config.BuiltinThemes() {
		if t.ID == selected {
			d.selected = i
		}
	}
	m.themes = d
	return nil
}

func (m *Model) themeKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.themes
	if d.busy {
		return nil
	}
	options := config.BuiltinThemes()
	switch msg.String() {
	case "esc", "q":
		m.cfg.Theme = d.original
		m.themes = nil
	case "up", "k", "shift+tab":
		d.selected = (d.selected + len(options) - 1) % len(options)
		m.cfg.Theme = options[d.selected].ID
		d.err = ""
	case "down", "j", "tab":
		d.selected = (d.selected + 1) % len(options)
		m.cfg.Theme = options[d.selected].ID
		d.err = ""
	case "enter":
		name := options[d.selected].ID
		m.cfg.Theme = name
		d.busy = true
		return func() tea.Msg { return themeSavedMsg{d, config.SaveTheme(d.path, name)} }
	}
	return nil
}

func (m *Model) themeView(width, height int) string {
	d := m.themes
	p := m.paletteColors()
	lines := []string{colored(p.Accent).Bold(true).Render("Choose a theme"), m.mutedStyle().Render("Preview with ↑/↓ · Enter save · Esc cancel"), ""}
	for i, t := range config.BuiltinThemes() {
		marker := "  "
		if i == d.selected {
			marker = "> "
		}
		label := fmt.Sprintf("%s%-12s  %s", marker, t.Name, t.Description)
		if width < 65 {
			label = fmt.Sprintf("%s%s  (%s)", marker, t.Name, t.ID)
		}
		if i == d.selected {
			label = colored(p.Accent).Bold(true).Render(label)
		} else {
			label = colored(p.Muted).Render(label)
		}
		lines = append(lines, ansi.Truncate(label, width, "…"))
		if height >= 17 {
			swatches := "    "
			for _, color := range []string{t.Palette.Background, t.Palette.Foreground, t.Palette.Accent, t.Palette.Success, t.Palette.Warning} {
				swatches += colored(color).Render("██ ")
			}
			lines = append(lines, swatches)
		}
	}
	lines = append(lines, "", m.mutedStyle().Render("Save to "+d.scope+" · Current: "+d.original))
	if d.busy {
		lines = append(lines, "Saving theme…")
	}
	if d.err != "" {
		lines = append(lines, colored(p.Error).Render(d.err))
	}
	return strings.Join(lines, "\n")
}
