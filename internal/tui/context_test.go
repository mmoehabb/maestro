package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/store"
)

func fixtureHistory() store.History {
	ts := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	return store.History{Task: store.Task{ID: 1, Title: "Fix authentication"}, Sessions: []store.Session{{ID: 1, Agent: "codex", StartedAt: ts}, {ID: 2, Agent: "agy", StartedAt: ts.Add(time.Minute)}}, Events: []store.TimelineEvent{{Kind: "created", TS: ts}, {Kind: "switched", TS: ts.Add(time.Minute)}}, Turns: []store.Turn{{SessionID: 1, Role: "user", Content: "Fix the login cookie."}, {SessionID: 1, Role: "tool_result", Content: "PASS"}, {SessionID: 1, Role: "assistant", Content: "Cookie validation fixed."}}}
}

func TestContextSnapshots(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"80x24", 80, 24}, {"160x48", 160, 48}} {
		for _, kind := range []string{"switch", "confirm", "history"} {
			m := testModel(t)
			m.width, m.height = size.w, size.h
			if kind == "history" {
				m.history = &historyView{data: fixtureHistory(), expanded: map[int64]bool{1: true}}
			} else {
				m.switcher = &switchDialog{task: m.tabs[0].task, agents: []string{"agy", "codex", "opencode"}, labels: []string{"agy · installed", "codex · current · previously used", "opencode · not installed"}, selected: 0, confirm: kind == "confirm"}
			}
			got := ansi.Strip(m.View().Content) + "\n"
			path := filepath.Join("testdata", kind+"-"+size.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != got {
				t.Fatalf("%s snapshot differs", path)
			}
		}
	}
}

func TestContextModalRouting(t *testing.T) {
	m := testModel(t)
	m.history = &historyView{data: fixtureHistory(), expanded: map[int64]bool{}}
	_, _ = m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.active != 0 {
		t.Fatal("tab shortcut escaped history")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.history.expanded[1] {
		t.Fatal("session not expanded")
	}
	before := m.history.View(80, 20)
	if strings.Contains(before, "\nPASS") {
		t.Fatal("tool output expanded by default")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 't'})
	if !strings.Contains(m.history.View(80, 20), "PASS") {
		t.Fatal("tool toggle failed")
	}
	_, _ = m.Update(tea.PasteMsg{Content: "ignored"})
	_, _ = m.Update(tea.KeyReleaseMsg{Code: 't'})
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.history != nil {
		t.Fatal("history did not close")
	}
	m.switcher = &switchDialog{agents: []string{"agy", "codex"}, labels: []string{"agy", "codex"}}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.switcher.selected != 1 {
		t.Fatal("picker selection failed")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.switcher != nil {
		t.Fatal("picker did not close")
	}
}

func TestFreshTargetConfirmationFromCLI(t *testing.T) {
	m := testModel(t)
	task := m.tabs[0].task
	m.contextMessage(switchedMsg{id: task.ID, task: task, target: "agy", err: core.ErrFreshStartRequired})
	if m.switcher == nil || !m.switcher.confirm || !m.switcher.fresh || m.switcher.agents[m.switcher.selected] != "agy" {
		t.Fatal("missing target confirmation")
	}
	if !strings.Contains(m.switcher.View(), "start agy fresh with a handoff") {
		t.Fatal(m.switcher.View())
	}
	if m.tabs[0].err != nil {
		t.Fatal("confirmation replaced outgoing pane with an error")
	}
	m.switchKey(tea.KeyPressMsg{Code: 'n'})
	if m.switcher.confirm || m.switcher.fresh {
		t.Fatal("cancel retained fresh-start consent")
	}
	m.contextMessage(switchedMsg{id: task.ID, task: task, target: "agy", err: core.ErrFreshStartRequired})
	if cmd := m.switchKey(tea.KeyPressMsg{Code: 'y'}); cmd == nil || !m.switcher.busy {
		t.Fatal("confirmation did not schedule switch")
	}
}
