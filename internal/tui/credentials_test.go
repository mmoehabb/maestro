package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/git"
)

func TestCredentialDialogSubmitAndCancel(t *testing.T) {
	for _, cancelKey := range []rune{0, tea.KeyEscape} {
		m := testModel(t)
		m.width, m.height = 80, 24
		m.forgeUI = &forgeDialog{action: "pr", busy: true}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result := make(chan credentialReply, 1)
		go func() {
			value, err := m.promptCredentials(ctx, "Enter passphrase for key '/tmp/key':")
			result <- credentialReply{value, err}
		}()
		select {
		case request := <-m.credentials:
			m.credentials <- request
		case <-ctx.Done():
			t.Fatal("prompt not queued")
		}
		m.pollCredentials()
		if m.credentialUI == nil {
			t.Fatal("prompt not opened")
		}
		secret := " p@ss Word!?é "
		m.Update(tea.PasteMsg{Content: secret})
		view := ansi.Strip(m.View().Content)
		if strings.Contains(view, secret) || !strings.Contains(view, "•••") {
			t.Fatal("credential was not masked")
		}
		if len(strings.Split(view, "\n")) != m.height {
			t.Fatal("credential prompt overflowed the screen")
		}
		key := tea.KeyEnter
		if cancelKey != 0 {
			key = cancelKey
		}
		m.Update(tea.KeyPressMsg{Code: key})
		if m.credentialUI != nil {
			t.Fatal("credential prompt remained visible")
		}
		select {
		case reply := <-result:
			if cancelKey == 0 {
				if reply.err != nil || reply.value != secret {
					t.Fatal("passphrase was changed during submission")
				}
			} else if !errors.Is(reply.err, git.ErrCredentialCanceled) || m.forgeUI != nil {
				t.Fatal("Escape did not cancel authentication and close the busy dialog")
			}
		case <-ctx.Done():
			t.Fatal("prompt did not finish")
		}
	}
}

func TestCredentialDialogTimeoutAndKeyboard(t *testing.T) {
	m := testModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.credentials <- credentialRequest{ctx: ctx, prompt: "Passphrase:", reply: make(chan credentialReply, 1)}
	m.pollCredentials()
	if m.credentialUI == nil {
		t.Fatal("prompt not opened")
	}
	var want strings.Builder
	for code := rune(' '); code <= '~'; code++ {
		m.Update(tea.KeyPressMsg{Code: code})
		want.WriteRune(code)
	}
	if m.credentialUI.input.Value() != want.String() {
		t.Fatal("printable passphrase characters were changed")
	}
	m.Update(tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt})
	if m.active != 0 {
		t.Fatal("tab shortcut escaped the credential dialog")
	}
	cancel()
	m.pollCredentials()
	if m.credentialUI != nil {
		t.Fatal("expired prompt remained visible")
	}
}

func TestLongCredentialPromptKeepsInputVisible(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 20, 12
	m.credentials <- credentialRequest{
		ctx: context.Background(), prompt: "Enter passphrase for key '" + strings.Repeat("long/path/", 100) + "':",
		reply: make(chan credentialReply, 1),
	}
	m.pollCredentials()
	m.Update(tea.PasteMsg{Content: "secret"})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "••••••") || !strings.Contains(view, "Enter submit") {
		t.Fatal("long prompt hid credential input or submission hint")
	}
	if len(strings.Split(view, "\n")) != m.height {
		t.Fatal("credential dialog overflowed terminal height")
	}
}
