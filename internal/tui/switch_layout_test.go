package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSwitchSelectionAndConfirmationFitPane(t *testing.T) {
	for _, sidebar := range []bool{false, true} {
		m := testModel(t)
		m.sidebar = sidebar
		d := &switchDialog{task: m.tabs[0].task}
		for i := range 20 {
			name := fmt.Sprintf("agent-%02d", i)
			d.agents = append(d.agents, name)
			d.labels = append(d.labels, name)
		}
		m.switcher = d
		for _, size := range [][2]int{{160, 48}, {80, 24}, {60, 16}, {80, 24}} {
			_, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for range 21 {
				m.switchKey(tea.KeyPressMsg{Code: tea.KeyDown})
				view := ansi.Strip(m.View().Content)
				if !strings.Contains(view, "› "+d.agents[d.selected]) || !strings.Contains(view, "Enter switch") {
					t.Fatalf("selected agent or instructions hidden at %v, sidebar=%v:\n%s", size, sidebar, view)
				}
			}
			d.confirm, d.fresh, d.offset = true, true, 0
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "handoff?") || !strings.Contains(view, "y/Enter confirm") || !strings.Contains(view, "n/Esc cancel") {
				t.Fatalf("confirmation clipped at %v:\n%s", size, view)
			}
			m.switchKey(tea.KeyPressMsg{Code: 'n'})
			if d.confirm || d.fresh {
				t.Fatal("cancel retained fresh-start consent")
			}
		}
	}
}

func TestSwitchLongErrorsAreScrollable(t *testing.T) {
	m := testModel(t)
	m.sidebar = true
	d := &switchDialog{agents: []string{"codex"}, labels: []string{"codex"}, err: strings.Repeat("A long diagnostic with details. ", 100) + "ERROR_END"}
	m.switcher = d
	for range 100 {
		m.switchKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "ERROR_END") || !strings.Contains(view, "Esc cancel") {
		t.Fatal("error tail or escape instructions unreachable", view)
	}
	m.switchKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(ansi.Strip(m.View().Content), "› codex") {
		t.Fatal("navigation did not reveal selection after error scroll")
	}
}

func TestSwitchLongTargetKeepsConfirmationKeysVisible(t *testing.T) {
	m := testModel(t)
	m.sidebar = true
	name := strings.Repeat("custom-agent-", 30) + "TARGET_END"
	m.switcher = &switchDialog{agents: []string{name}, labels: []string{name}, confirm: true, fresh: true}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "handoff?") || !strings.Contains(view, "n/Esc cancel") {
		t.Fatal("long target obscured the confirmation", view)
	}
	for range 100 {
		m.switchKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "TARGET_END") || !strings.Contains(view, "n/Esc cancel") {
		t.Fatal("long target cannot be inspected with cancel still visible", view)
	}
}
