package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type checkpointMsg struct {
	id  int64
	err error
}

func (m *Model) checkpoint() tea.Cmd {
	if len(m.tabs) == 0 || m.tabs[m.active].pending {
		return nil
	}
	t := &m.tabs[m.active]
	t.pending = true
	t.pendingHandoff = ""
	t.pendingHandoffFresh = false
	task := t.task
	return func() tea.Msg {
		_, err := m.runtime.Checkpoint(context.Background(), task)
		return checkpointMsg{task.ID, err}
	}
}
