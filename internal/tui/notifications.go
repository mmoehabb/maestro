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

func (m *Model) SetNotifier(n notify.Notifier) { m.notifier = n }

// Observe both native events and reconciled pane snapshots: runtime activity
// events are intentionally lossy when the UI queue is full.
func (m *Model) observeActivity() tea.Cmd {
	var cmds []tea.Cmd
	for i, t := range m.tabs {
		previous, known := m.observed[t.task.ID]
		m.observed[t.task.ID] = t.state
		if !known || previous == t.state || (t.state != term.Done && t.state != term.NeedsInput) {
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
