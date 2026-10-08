package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWebsiteShellGeometry(t *testing.T) {
	m := testModel(t)
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}, {160, 48}} {
		m.width, m.height = size[0], size[1]
		for _, sidebar := range []bool{false, true} {
			m.sidebar = sidebar
			g := m.geometry()
			if g.pane.x+g.pane.w > m.width || g.pane.y+g.pane.h > g.status {
				t.Fatalf("pane overlaps chrome at %v: %+v", size, g)
			}
			if g.pane.w < 1 || g.pane.h < 1 {
				t.Fatal("empty pane")
			}
			if m.taskAt(g.pane.x, g.pane.y) != -1 {
				t.Fatal("pane overlaps task hit target")
			}
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			if len(lines) != m.height {
				t.Fatalf("height %d at %v", len(lines), size)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > m.width {
					t.Fatalf("overflow %q at %v", line, size)
				}
			}
		}
	}
}

func TestWebsitePaletteAndTabWidths(t *testing.T) {
	m := testModel(t)
	m.cfg.Theme = "dark"
	if p := m.paletteColors(); p.Background != "#242b26" || p.Accent != "#e4a78c" {
		t.Fatalf("website palette not applied: %+v", p)
	}
	m.cfg.Theme = "catppuccin"
	if m.paletteColors().Background != "#1e1e2e" {
		t.Fatal("explicit Catppuccin theme changed")
	}
	m.cfg.Theme = "dark"
	m.width = 120
	m.height = 40
	regions, _ := m.tabLayout()
	// Each tab expands beyond its label, and its exact visible rectangle is clickable.
	for i, r := range regions {
		if r.w < 30 || m.taskAt(r.x+r.w/2, r.y) != i {
			t.Fatalf("tab %d geometry: %+v", i, r)
		}
	}
	content := m.renderTabs() + m.tabRule()
	if !strings.Contains(content, "48;2;48;57;47") {
		t.Fatal("active tab missing website surface")
	}
	if strings.Contains(ansi.Strip(content), "[38;") {
		t.Fatal("styling corrupted nested ANSI")
	}
}
