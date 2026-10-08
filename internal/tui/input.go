package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// Component replies belong to the input that requested them. In particular,
// Bubbles clipboard replies are not terminal PasteMsg messages and may arrive
// after a dialog has closed or focus has moved to another field.
type inputMsg struct {
	input *textinput.Model
	msg   tea.Msg
}

func inputCommand(input *textinput.Model, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			commands := make(tea.BatchMsg, len(batch))
			for i, child := range batch {
				commands[i] = inputCommand(input, child)
			}
			return commands
		}
		return inputMsg{input: input, msg: msg}
	}
}

func (m *Model) inputReply(msg inputMsg) tea.Cmd {
	if m.palette != nil && msg.input == &m.palette.input {
		return m.paletteInput(msg.msg)
	}
	if d := m.dialog; d != nil && !d.busy && d.field != 2 && msg.input == &d.fields[d.inputIndex()] {
		return m.dialogInput(msg.msg)
	}
	return nil
}
