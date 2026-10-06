package tui

import (
	"context"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/mmoehabb/maestro/internal/store"
)

type notesDialog struct {
	task  store.Task
	input textarea.Model
	busy  bool
	err   string
}

type notesSavedMsg struct {
	id    int64
	notes string
	err   error
}

func (m *Model) openNotes() tea.Cmd {
	if len(m.tabs) == 0 || m.tabs[m.active].pending {
		return nil
	}
	task := m.tabs[m.active].task
	input := textarea.New()
	input.SetVirtualCursor(true)
	input.CharLimit = store.MaxNotesBytes
	input.MaxHeight = 0
	input.MaxWidth = 0
	input.ShowLineNumbers = false
	input.Prompt = ""
	input.Placeholder = "Decisions, constraints, and TODOs for the next agent…"
	input.SetWidth(max(1, m.width-2))
	input.SetHeight(max(1, m.height-9))
	input.SetValue(task.Notes)
	m.notes = &notesDialog{task: task, input: input}
	return m.notes.input.Focus()
}

func (m *Model) notesKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.notes
	if d.busy {
		return nil
	}
	switch msg.String() {
	case "esc":
		m.notes = nil
		return nil
	case "ctrl+s":
		d.busy = true
		d.err = ""
		task, notes := d.task, d.input.Value()
		return func() tea.Msg {
			return notesSavedMsg{task.ID, notes, m.runtime.SetNotes(context.Background(), task, notes)}
		}
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (d *notesDialog) View() string {
	footer := "Ctrl+S save · Esc cancel · Enter new line"
	if d.busy {
		footer = "Saving notes…"
	}
	if d.err != "" {
		footer = d.err + "\n" + footer
	}
	return "Task notes · " + d.task.Slug + "\nIncluded in the next agent handoff.\n\n" + d.input.View() + "\n" + footer
}

func (m *Model) notesSaved(msg notesSavedMsg) {
	if msg.err == nil {
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.id {
				m.tabs[i].task.Notes = msg.notes
			}
		}
	}
	if m.notes == nil || m.notes.task.ID != msg.id {
		return
	}
	if msg.err != nil {
		m.notes.busy = false
		m.notes.err = msg.err.Error()
		return
	}
	m.notes = nil
	m.notify("Notes saved.")
}
