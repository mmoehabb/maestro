package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInputRepliesStayWithTheirOwner(t *testing.T) {
	m := testModel(t)
	m.openPalette()
	m.palette.selected = 4
	input := &m.palette.input
	cmd := inputCommand(input, func() tea.Msg { return tea.PasteMsg{Content: "push"} })
	_, _ = m.Update(cmd())
	if input.Value() != "push" || m.palette.selected != 0 {
		t.Fatal("input reply did not update query and reset selection")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.openPalette()
	_, _ = m.Update(cmd())
	if m.palette.input.Value() != "" {
		t.Fatal("closed palette's reply reached the replacement")
	}
	m.palette = nil
	m.dispatch("c")
	_, _ = m.Update(cmd())
	if m.dialog.fields[0].Value() != "" {
		t.Fatal("closed palette's reply reached another view")
	}
	d := m.dialog
	fieldReply := inputCommand(&d.fields[0], func() tea.Msg { return tea.PasteMsg{Content: "title"} })
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	_, _ = m.Update(fieldReply())
	if d.fields[0].Value() != "" || d.fields[1].Value() != "main" {
		t.Fatal("clipboard reply survived a field focus change")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, _ = m.Update(fieldReply()) // No active input: must not forward to an agent.
}

func TestInputBatchRepliesAndBracketedPaste(t *testing.T) {
	m := testModel(t)
	m.openPalette()
	cmd := inputCommand(&m.palette.input, tea.Batch(
		func() tea.Msg { return tea.PasteMsg{Content: "task"} },
		func() tea.Msg { return tea.PasteMsg{Content: " name"} },
	))
	for _, child := range cmd().(tea.BatchMsg) {
		if _, ok := child().(inputMsg); !ok {
			t.Fatal("batch child lost its input owner")
		}
		_, _ = m.Update(child())
	}
	if m.palette.input.Value() != "task name" {
		t.Fatal(m.palette.input.Value())
	}
	m.palette.selected = 2
	_, _ = m.Update(tea.PasteMsg{Content: " pasted"})
	if m.palette.input.Value() != "task name pasted" || m.palette.selected != 0 {
		t.Fatal("terminal paste failed")
	}
}
