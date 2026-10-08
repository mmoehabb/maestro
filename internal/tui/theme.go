package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/mmoehabb/maestro/internal/config"
)

func resolvePalette(cfg config.Config, light bool) config.Palette {
	name := cfg.Theme
	if name == "auto" {
		if light {
			name = "light"
		} else {
			name = "dark"
		}
	}
	if theme, ok := config.BuiltinTheme(name); ok {
		return theme.Palette
	}
	return cfg.Themes[name]
}

func colored(color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
func (m *Model) paletteColors() config.Palette { return resolvePalette(m.cfg, m.light) }
func (m *Model) accentStyle() lipgloss.Style   { return colored(m.paletteColors().Accent) }
func (m *Model) mutedStyle() lipgloss.Style    { return colored(m.paletteColors().Muted) }

func inputStyles(p config.Palette) textinput.Styles {
	s := textinput.DefaultDarkStyles()
	for _, state := range []*textinput.StyleState{&s.Focused, &s.Blurred} {
		state.Text = colored(p.Foreground)
		state.Prompt = colored(p.Accent)
		state.Placeholder = colored(p.Muted)
		state.Suggestion = colored(p.Muted)
	}
	s.Cursor.Color = lipgloss.Color(p.Accent)
	return s
}

func areaStyles(p config.Palette) textarea.Styles {
	s := textarea.DefaultDarkStyles()
	for _, state := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		state.Base = colored(p.Foreground)
		state.Text = colored(p.Foreground)
		state.CursorLine = colored(p.Foreground)
		state.Prompt = colored(p.Accent)
		state.Placeholder = colored(p.Muted)
		state.LineNumber = colored(p.Muted)
		state.CursorLineNumber = colored(p.Accent)
		state.EndOfBuffer = colored(p.Muted)
		state.Selection = colored(p.Background).Background(lipgloss.Color(p.Accent))
	}
	s.Cursor.Color = lipgloss.Color(p.Accent)
	return s
}

func (m *Model) styleInputs() {
	cols, rows := m.size()
	p := m.paletteColors()
	if m.notes != nil {
		m.notes.input.SetStyles(areaStyles(p))
		m.notes.input.SetWidth(max(1, cols-2))
		m.notes.input.SetHeight(max(1, rows-5))
	}
	if m.forgeUI != nil {
		d := m.forgeUI
		d.title.SetStyles(inputStyles(p))
		d.base.SetStyles(inputStyles(p))
		d.confirm.SetStyles(inputStyles(p))
		if d.action == "pr" && !d.busy && d.task.PRNumber == 0 {
			d.body.SetStyles(areaStyles(p))
			d.title.SetWidth(max(1, cols-9))
			d.base.SetWidth(max(1, cols-9))
			d.confirm.SetWidth(max(1, cols-2))
			d.body.SetWidth(max(1, cols-2))
			d.body.SetHeight(max(1, rows-9))
		}
	}
}
