package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/mmoehabb/maestro/internal/store"
)

type notesDialog struct {
	task   store.Task
	input  textarea.Model
	rename bool
	busy   bool
	err    string
}

type notesSavedMsg struct {
	id     int64
	notes  string
	rename bool
	err    error
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
			if d.rename {
				return notesSavedMsg{task.ID, strings.TrimSpace(notes), true, m.runtime.Rename(context.Background(), task, notes)}
			}
			return notesSavedMsg{task.ID, notes, false, m.runtime.SetNotes(context.Background(), task, notes)}
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
	if d.rename {
		footer = strings.ReplaceAll(footer, " · Enter new line", "")
		return "Rename task · " + d.task.Slug + "\nEdit the display title.\n\n" + d.input.View() + "\n" + footer
	}
	return "Task notes · " + d.task.Slug + "\nIncluded in the next agent handoff.\n\n" + d.input.View() + "\n" + footer
}

func (m *Model) notesSaved(msg notesSavedMsg) {
	if msg.err == nil {
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.id {
				if msg.rename {
					m.tabs[i].task.Title = msg.notes
				} else {
					m.tabs[i].task.Notes = msg.notes
				}
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
	if msg.rename {
		m.notify("Title saved.")
	} else {
		m.notify("Notes saved.")
	}
}

func (m *Model) openRename() tea.Cmd {
	cmd := m.openNotes()
	if m.notes != nil {
		m.notes.rename = true
		m.notes.input.CharLimit = 512
		m.notes.input.Placeholder = "Task title"
		m.notes.input.SetValue(m.notes.task.Title)
		m.notes.input.SetHeight(2)
	}
	return cmd
}
