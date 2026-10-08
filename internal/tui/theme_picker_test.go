package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mmoehabb/maestro/internal/config"
)

func TestThemePickerPreviewCancelSave(t *testing.T) {
	m := testModel(t)
	m.service.Repo.Root = t.TempDir()
	m.dispatch("T")
	if m.themes == nil {
		t.Fatal("picker missing")
	}
	original := m.cfg.Theme
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cfg.Theme != "light" {
		t.Fatal("live preview missing", m.cfg.Theme)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.active != 0 {
		t.Fatal("shortcut escaped picker")
	}
	_, _ = m.Update(tea.PasteMsg{Content: "dark"})
	if m.cfg.Theme != "light" {
		t.Fatal("paste changed choice")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.cfg.Theme != original || m.themes != nil {
		t.Fatal("cancel did not restore")
	}
	local := filepath.Join(m.service.Repo.Root, ".maestro.toml")
	if err := os.WriteFile(local, []byte("theme = 'dark'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.dispatch("T")
	if m.themes.path != local {
		t.Fatal("repository override ignored")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("save missing")
	}
	_, _ = m.Update(cmd())
	if m.themes != nil || m.cfg.Theme != "light" {
		t.Fatal("theme not applied")
	}
	if name, err := config.FileTheme(local); err != nil || name != "light" {
		t.Fatal(name, err)
	}
	m.dispatch("T")
	m.themes.path = m.service.Repo.Root // saving to a directory must fail
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, _ = m.Update(cmd())
	if m.themes == nil || m.themes.err == "" || m.themes.busy {
		t.Fatal("save failure was not recoverable")
	}
}
