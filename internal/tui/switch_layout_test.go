package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/store"
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
			if !strings.Contains(view, "handoff?") || !strings.Contains(view, "y/Enter confirm") || !strings.Contains(view, "Esc cancel") {
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
	if !strings.Contains(view, "handoff?") || !strings.Contains(view, "Esc cancel") {
		t.Fatal("long target obscured the confirmation", view)
	}
	for range 100 {
		m.switchKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "TARGET_END") || !strings.Contains(view, "Esc cancel") {
		t.Fatal("long target cannot be inspected with cancel still visible", view)
	}
}

func TestSwitchForceDialogAndOffer(t *testing.T) {
	m := testModel(t)
	m.switcher = &switchDialog{
		task:   store.Task{ID: 1, Slug: "task-1", Agent: "codex"},
		agents: []string{"agy", "codex"},
		labels: []string{"agy", "codex · current"},
	}
	// Pressing f on non-current agent opens force confirmation.
	m.switchKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if !m.switcher.confirm || !m.switcher.offerForce || !m.switcher.force {
		t.Fatalf("expected force confirmation dialog, got %+v", m.switcher)
	}
	for _, size := range [][2]int{{80, 24}, {160, 48}} {
		m.width, m.height = size[0], size[1]
		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "Force switch to agy?") || !strings.Contains(view, "y/Enter force switch") || !strings.Contains(view, "Esc cancel") {
			t.Fatalf("force confirmation missing expected text at %v:\n%s", size, view)
		}
	}
	// Pressing Esc cancels out of force offer.
	m.switchKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.switcher != nil {
		t.Fatal("expected Esc to close force confirmation")
	}

	// Simulate ErrHandoffFailed reopening force offer.
	m.contextMessage(switchedMsg{
		target: "agy",
		id:     1,
		task:   store.Task{ID: 1, Slug: "task-1", Agent: "codex"},
		err:    core.ErrHandoffFailed,
	})
	if m.switcher == nil || !m.switcher.confirm || !m.switcher.offerForce {
		t.Fatalf("expected ErrHandoffFailed to reopen force offer, got %+v", m.switcher)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Force switch to agy?") || !strings.Contains(view, core.ErrHandoffFailed.Error()) {
		t.Fatalf("expected force offer with error, got:\n%s", view)
	}

	// Pressing y confirms force switch and clears dialog.
	cmd := m.switchKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil || m.switcher != nil {
		t.Fatalf("expected force switch command and closed dialog")
	}

	// Simulate wrapped context.Canceled reopening force offer without setting t.err.
	m.tabs = []tab{{task: store.Task{ID: 1, Slug: "task-1", Agent: "codex"}}}
	m.contextMessage(switchedMsg{
		target: "agy",
		id:     1,
		task:   store.Task{ID: 1, Slug: "task-1", Agent: "codex"},
		err:    fmt.Errorf("daemon request interrupted: %w", context.Canceled),
	})
	if m.tabs[0].err != nil {
		t.Fatalf("expected cancelled switch not to set tab err, got %v", m.tabs[0].err)
	}
	if m.switcher == nil || !m.switcher.confirm || !m.switcher.offerForce {
		t.Fatalf("expected wrapped context.Canceled to reopen force offer, got %+v", m.switcher)
	}
	if !strings.Contains(m.switcher.err, "Switch cancelled before the current agent wrote its handoff.") {
		t.Fatalf("unexpected force offer error: %q", m.switcher.err)
	}

	// Simulate forced switch returning ErrFreshStartRequired preserving force=true and fresh=true.
	m.switcher = nil
	m.contextMessage(switchedMsg{
		target: "agy",
		id:     1,
		task:   store.Task{ID: 1, Slug: "task-1", Agent: "codex"},
		err:    core.ErrFreshStartRequired,
		force:  true,
	})
	if m.switcher == nil || !m.switcher.confirm || !m.switcher.fresh || !m.switcher.force {
		t.Fatalf("expected force=true and fresh=true on fresh confirmation, got %+v", m.switcher)
	}
	view = ansi.Strip(m.View().Content)
	if strings.Contains(view, "s summarize") {
		t.Fatalf("expected s summarize omitted when force=true and fresh=true, got:\n%s", view)
	}
}
