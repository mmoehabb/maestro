package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/git"
)

func TestDiffRetainedLinesRemainReachable(t *testing.T) {
	for _, prefix := range []string{strings.Repeat("x", 5000), strings.Repeat("界\t", 1000), "short "} {
		m := testModel(t)
		m.diff = &diffView{result: git.DiffResult{Patch: "+" + prefix + "IMPORTANT_END\n"}}
		for range 1000 {
			m.diffKey(tea.KeyPressMsg{Code: tea.KeyRight})
		}
		width, rows := m.size()
		view := ansi.Strip(m.diff.view(width, rows, m.paletteColors()))
		if !strings.Contains(view, "IMPORTANT_END") {
			t.Fatalf("line end unreachable at %d: %s", m.diff.horizontal, view)
		}
		m.width = 160
		width, rows = m.size()
		view = ansi.Strip(m.diff.view(width, rows, m.paletteColors()))
		if !strings.Contains(view, "IMPORTANT_END") || m.diff.horizontal > max(0, m.diff.columns-width) {
			t.Fatal("resize left the diff past its right edge")
		}
		m.diffKey(tea.KeyPressMsg{Code: tea.KeyHome})
		if m.diff.horizontal != 0 {
			t.Fatal("Home did not return to the start")
		}
		m.diffKey(tea.KeyPressMsg{Code: 'r'})
		m.diffKey(tea.KeyPressMsg{Code: tea.KeyRight})
		if m.diff.horizontal != 0 || m.diff.lines != nil {
			t.Fatal("loading diff retained old bounds")
		}
	}
}
