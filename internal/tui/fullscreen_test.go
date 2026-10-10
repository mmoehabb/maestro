package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFullscreenToggleKeybinding(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24

	if m.fullscreen {
		t.Fatal("expected fullscreen to default to false")
	}

	// Send prefix followed by 'f'
	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.prefixed {
		t.Fatal("expected model to be prefixed")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'f'})
	if m.prefixed {
		t.Fatal("expected prefixed to be reset")
	}
	if !m.fullscreen {
		t.Fatal("expected fullscreen to be true after prefix+f")
	}

	// Toggle it off
	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, _ = m.Update(tea.KeyPressMsg{Code: 'f'})
	if m.fullscreen {
		t.Fatal("expected fullscreen to be false after second prefix+f")
	}
}

func TestFullscreenGeometryAndLayout(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	m.sidebar = true

	// Normal geometry
	normalGeo := m.geometry()
	if normalGeo.padding == 0 {
		t.Errorf("expected normal padding > 0, got %d", normalGeo.padding)
	}
	if normalGeo.footer < 0 {
		t.Errorf("expected normal footer to be visible, got %d", normalGeo.footer)
	}
	if m.sidebarWidth() == 0 {
		t.Fatal("expected normal sidebar width > 0")
	}

	// Enable fullscreen
	m.fullscreen = true
	fsGeo := m.geometry()

	if fsGeo.padding != 0 {
		t.Errorf("expected fullscreen padding 0, got %d", fsGeo.padding)
	}
	if fsGeo.header != -1 {
		t.Errorf("expected fullscreen header -1, got %d", fsGeo.header)
	}
	if fsGeo.tabs != 0 {
		t.Errorf("expected fullscreen tabs at 0, got %d", fsGeo.tabs)
	}
	if fsGeo.tabLine != -1 {
		t.Errorf("expected fullscreen tabLine -1, got %d", fsGeo.tabLine)
	}
	if fsGeo.meta != -1 {
		t.Errorf("expected fullscreen meta -1, got %d", fsGeo.meta)
	}
	if fsGeo.rule != -1 {
		t.Errorf("expected fullscreen rule -1, got %d", fsGeo.rule)
	}
	if fsGeo.status != -1 {
		t.Errorf("expected fullscreen status -1, got %d", fsGeo.status)
	}
	if fsGeo.footer != -1 {
		t.Errorf("expected fullscreen footer -1, got %d", fsGeo.footer)
	}
	if fsGeo.pane.x != 0 {
		t.Errorf("expected fullscreen pane.x 0, got %d", fsGeo.pane.x)
	}
	if fsGeo.pane.y != 1 {
		t.Errorf("expected fullscreen pane.y 1, got %d", fsGeo.pane.y)
	}
	if fsGeo.pane.w != 100 {
		t.Errorf("expected fullscreen pane.w 100, got %d", fsGeo.pane.w)
	}
	if fsGeo.pane.h != 29 {
		t.Errorf("expected fullscreen pane.h 29, got %d", fsGeo.pane.h)
	}

	// Sidebar should be hidden in fullscreen
	if m.sidebarWidth() != 0 {
		t.Errorf("expected sidebarWidth 0 in fullscreen, got %d", m.sidebarWidth())
	}

	// Pane size check
	w, h := m.size()
	if w != 100 || h != 29 {
		t.Errorf("expected size (100, 29), got (%d, %d)", w, h)
	}

	// Render view check
	view := ansi.Strip(m.View().Content)
	lines := strings.Split(view, "\n")
	if len(lines) != 30 {
		t.Errorf("expected 30 lines rendered, got %d", len(lines))
	}
	// Row 0 should contain tabs
	if !strings.Contains(lines[0], "fix-auth") {
		t.Errorf("expected row 0 to contain tab label, got: %q", lines[0])
	}
	// Chrome footer shortcuts should not appear on the screen
	if strings.Contains(view, "alt 1–9 switch tabs") {
		t.Errorf("expected chrome footer shortcuts to be hidden in fullscreen view")
	}
}

func TestFullscreenPaletteAction(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24

	// Open palette
	cmd := m.openPalette()
	if cmd != nil {
		_ = cmd()
	}
	if m.palette == nil {
		t.Fatal("expected palette to be open")
	}

	// Search for fullscreen
	m.palette.input.SetValue("fullscreen")
	entries := m.paletteEntries()
	if len(entries) == 0 {
		t.Fatal("expected to find fullscreen in palette entries")
	}
	if entries[0].key != "f" {
		t.Fatalf("expected top entry key 'f', got %q", entries[0].key)
	}

	// Execute enter on palette
	m.paletteKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.fullscreen {
		t.Fatal("expected fullscreen to be enabled via palette")
	}
}
