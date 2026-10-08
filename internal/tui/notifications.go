package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mmoehabb/maestro/internal/notify"
	"github.com/mmoehabb/maestro/internal/term"
)

type notificationMsg struct{ err error }

type activityObservation struct {
	pane     *term.Pane
	state    term.State
	revision uint64
}

func (t *tab) attachPane(pane *term.Pane) {
	if t.pane != pane {
		t.state, t.stateRevision, t.statePane = term.Starting, 0, pane
	}
	t.pane = pane
}

// Revisions are local to a pane. A delayed event cannot roll back a newer
// snapshot, but a replacement pane starts its own sequence at zero.
func (t *tab) acceptActivity(pane *term.Pane, state term.State, revision uint64) bool {
	if state == "" || pane != t.pane {
		return false
	}
	if pane == t.statePane && revision <= t.stateRevision {
		return false
	}
	t.statePane, t.state, t.stateRevision = pane, state, revision
	return true
}

func (m *Model) SetNotifier(n notify.Notifier) { m.notifier = n }

// Observe both native events and reconciled pane snapshots: runtime activity
// events are intentionally lossy when the UI queue is full.
func (m *Model) observeActivity() tea.Cmd {
	var cmds []tea.Cmd
	for i, t := range m.tabs {
		previous, known := m.observed[t.task.ID]
		current := activityObservation{pane: t.pane, state: t.state, revision: t.stateRevision}
		m.observed[t.task.ID] = current
		if known && previous.pane != current.pane {
			previous = activityObservation{pane: current.pane, state: term.Starting}
		}
		if !known || previous == current || (t.state != term.Done && t.state != term.NeedsInput) {
			continue
		}
		if i == m.active && !m.blurred {
			continue
		}
		m.attention[t.task.ID] = time.Now().Add(3 * time.Second)
		text := t.task.Slug + ": " + string(t.state)
		m.notify(text)
		if m.notifier != nil && slices.Contains(m.cfg.Activity.NotifyOn, string(t.state)) {
			notifier := m.notifier
			cmds = append(cmds, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				return notificationMsg{notifier.Send(ctx, "Maestro", text)}
			})
		}
	}
	return tea.Batch(cmds...)
}
