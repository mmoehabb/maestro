//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The clipboard package discovers helpers during init. Run an isolated test
// process with controlled helpers so this never reads the user's clipboard.
func TestClipboardInputRouting(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"xclip", "xsel", "wl-copy", "wl-paste", "pbpaste"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf clipboard-query\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestClipboardInputProcess$")
	cmd.Env = append(os.Environ(), "MAESTRO_CLIPBOARD_TEST=1", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clipboard routing: %v\n%s", err, output)
	}
}

func TestClipboardInputProcess(t *testing.T) {
	if os.Getenv("MAESTRO_CLIPBOARD_TEST") != "1" {
		return
	}
	m := testModel(t)
	m.openPalette()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("no clipboard command")
	}
	reply := cmd()
	_, _ = m.Update(reply)
	if m.palette.input.Value() != "clipboard-query" {
		t.Fatalf("clipboard result dropped: %q (%#v)", m.palette.input.Value(), reply)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.dispatch("c")
	_, _ = m.Update(reply)
	if m.dialog.fields[0].Value() != "" {
		t.Fatal("late palette clipboard reply reached the new task")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	_, _ = m.Update(cmd())
	if m.dialog.fields[0].Value() != "clipboard-query" {
		t.Fatal("new task clipboard reply dropped")
	}
}
