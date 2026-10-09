package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type Runtime interface {
	EventStream() <-chan core.Event
	Start(store.Task, int, int, bool) (*term.Pane, error)
	Switch(context.Context, store.Task, string, int, int, bool) (*term.Pane, error)
	SwitchFresh(context.Context, store.Task, string, int, int, bool) (*term.Pane, error)
	Create(context.Context, core.NewTask) (store.Task, error)
	Workflow(context.Context, store.Task, string, core.WorkflowOptions) (store.Task, error)
	Archive(context.Context, store.Task) error
	Checkpoint(context.Context, store.Task) (string, error)
	SetNotes(context.Context, store.Task, string) error
	Rename(context.Context, store.Task, string) error
	ReorderTasks(context.Context, []int64) error
	Stop(int64)
}

func (m *Model) reconcileTasks(tasks []store.Task) tea.Cmd {
	var active int64
	if len(m.tabs) > 0 {
		active = m.tabs[m.active].task.ID
	}
	previous := make(map[int64]tab, len(m.tabs))
	for _, t := range m.tabs {
		previous[t.task.ID] = t
	}
	var next []tab
	var commands []tea.Cmd
	for _, task := range tasks {
		if task.Lifecycle == "archived" {
			continue
		}
		t, known := previous[task.ID]
		t.task = task
		if !known {
			t.state = term.Starting
		}
		next = append(next, t)
	}
	m.tabs = next
	m.active = 0
	for i, t := range m.tabs {
		if t.task.ID == active {
			m.active = i
		}
		if _, known := previous[t.task.ID]; !known {
			commands = append(commands, m.launch(i, false))
		}
	}
	return tea.Batch(commands...)
}
