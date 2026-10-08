package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/git"
)

type credentialReply struct {
	value string
	err   error
}

type credentialRequest struct {
	ctx    context.Context
	prompt string
	reply  chan credentialReply
}

type credentialDialog struct {
	request credentialRequest
	input   textinput.Model
}

func (m *Model) promptCredentials(ctx context.Context, prompt string) (string, error) {
	request := credentialRequest{ctx: ctx, prompt: prompt, reply: make(chan credentialReply, 1)}
	select {
	case m.credentials <- request:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case reply := <-request.reply:
		return reply.value, reply.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (m *Model) pollCredentials() tea.Cmd {
	if m.credentialUI != nil && m.credentialUI.request.ctx.Err() != nil {
		m.credentialUI.input.SetValue("")
		m.credentialUI = nil
	}
	if m.credentialUI != nil {
		return nil
	}
	select {
	case request := <-m.credentials:
		if request.ctx.Err() != nil {
			return nil
		}
		input := textinput.New()
		input.SetVirtualCursor(true)
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
		m.credentialUI = &credentialDialog{request: request, input: input}
		return m.credentialUI.input.Focus()
	default:
		return nil
	}
}

func (m *Model) credentialKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.credentialUI
	switch msg.String() {
	case "esc", "ctrl+c":
		d.request.reply <- credentialReply{err: git.ErrCredentialCanceled}
		d.input.SetValue("")
		m.credentialUI = nil
		// Cancel the busy PR/cleanup view as well so the agent is visible again.
		m.forgeUI = nil
		return nil
	case "enter":
		d.request.reply <- credentialReply{value: d.input.Value()}
		d.input.SetValue("")
		m.credentialUI = nil
		return nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (m *Model) credentialView(cols, rows int) string {
	d := m.credentialUI
	d.input.SetStyles(inputStyles(m.paletteColors()))
	d.input.SetWidth(max(1, cols-2))
	prompt := strings.Join(strings.Fields(ansi.Strip(d.request.prompt)), " ")
	lines := strings.Split(ansi.Wrap(prompt, max(1, cols), ""), "\n")
	limit := max(1, rows-6)
	if len(lines) > limit {
		lines = lines[:limit]
		lines[limit-1] = ansi.Truncate(lines[limit-1]+"…", max(1, cols), "…")
	}
	return "Git authentication\n\n" + strings.Join(lines, "\n") + "\n\n" + d.input.View() + "\n\nEnter submit · Esc cancel"
}
