package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestNotesEditorRoutingAndSave(t *testing.T) {
	m := testModel(t)
	m.tabs[0].task.Notes = "Existing notes"
	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, _ = m.Update(tea.KeyPressMsg{Code: 'n'})
	if m.notes == nil || m.notes.input.Value() != "Existing notes" {
		t.Fatal("notes not loaded")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.active != 0 {
		t.Fatal("tab shortcut escaped notes editor")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, _ = m.Update(tea.PasteMsg{Content: "TODO: test notes"})
	value := m.notes.input.Value()
	if !strings.Contains(value, "\n") || !strings.Contains(value, "TODO: test notes") {
		t.Fatal("multiline input failed", value)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || !m.notes.busy {
		t.Fatal("save not scheduled")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.notes == nil {
		t.Fatal("in-flight save was discarded")
	}
	_, _ = m.Update(notesSavedMsg{m.tabs[0].task.ID, value, errors.New("write failed")})
	if m.notes == nil || m.notes.busy || m.notes.input.Value() != value {
		t.Fatal("failed save lost draft")
	}
	_, _ = m.Update(notesSavedMsg{m.tabs[0].task.ID, value, nil})
	if m.notes != nil || m.tabs[0].task.Notes != value {
		t.Fatal("save not reflected in tab")
	}
	m.openNotes()
	m.notes.input.SetValue("discard me")
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.tabs[0].task.Notes != value {
		t.Fatal("cancel overwrote saved notes")
	}
}

func TestNotesSnapshots(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"80x24", 80, 24}, {"160x48", 160, 48}} {
		m := testModel(t)
		m.width, m.height = size.w, size.h
		m.tabs[0].task.Notes = "Keep the public API compatible.\nTODO: test session expiry."
		m.openNotes()
		got := ansi.Strip(m.View().Content) + "\n"
		path := filepath.Join("testdata", "notes-"+size.name+".golden")
		if updateGolden() {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("%s snapshot differs", path)
		}
	}
}
