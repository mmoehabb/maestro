package tui

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/term"
)

func TestThemesPreserveExplicitAgentColors(t *testing.T) {
	m := testModel(t)
	dark, _ := config.BuiltinTheme("dark")
	m.cfg.Themes = map[string]config.Palette{"custom": dark.Palette}
	for _, name := range []string{"dark", "light", "catppuccin", "tokyo-night", "custom"} {
		for _, sidebar := range []bool{false, true} {
			m.cfg.Theme, m.sidebar = name, sidebar
			width, rows := m.size()
			agent := term.NewEmulator(width, rows, func() {})
			_, err := agent.Write([]byte("\x1b[38;2;17;34;51;48;2;68;85;102mAGENT\x1b[0m"))
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Split(agent.Render(), "\n")
			if err = agent.Close(); err != nil {
				t.Fatal(err)
			}
			for len(body) < rows {
				body = append(body, "")
			}
			content := m.composeShell(body, m.shellFooter(m.width))
			screen := vt.NewEmulator(m.width, m.height)
			_, err = screen.Write([]byte(strings.ReplaceAll(content, "\n", "\r\n")))
			if err != nil {
				t.Fatal(err)
			}
			g := m.geometry()
			cell := screen.CellAt(g.pane.x, g.pane.y)
			if cell == nil || cell.Content != "A" || cell.Style.Fg == nil || cell.Style.Bg == nil {
				t.Fatalf("agent color cell missing in %s/sidebar=%v: %+v", name, sidebar, cell)
			}
			if color.NRGBAModel.Convert(cell.Style.Fg) != (color.NRGBA{R: 17, G: 34, B: 51, A: 255}) || color.NRGBAModel.Convert(cell.Style.Bg) != (color.NRGBA{R: 68, G: 85, B: 102, A: 255}) {
				t.Fatalf("theme overwrote agent colors in %s: %+v", name, cell.Style)
			}
			mark := screen.CellAt(g.padding, g.header)
			if mark == nil || mark.Style.Fg == nil || color.NRGBAModel.Convert(mark.Style.Fg) != color.NRGBAModel.Convert(lipgloss.Color(m.paletteColors().Accent)) {
				t.Fatalf("theme accent missing from header in %s: %+v", name, mark)
			}
			if err = screen.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
