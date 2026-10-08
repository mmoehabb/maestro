//go:build !windows

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xterm "github.com/charmbracelet/x/term"

	"github.com/mmoehabb/maestro/internal/term"
)

func TestMouseAgentProcess(t *testing.T) {
	if os.Getenv("MAESTRO_MOUSE_HELPER") != "1" {
		return
	}
	if _, err := xterm.MakeRaw(os.Stdin.Fd()); err != nil {
		os.Exit(2)
	}
	f, err := os.OpenFile(os.Getenv("MAESTRO_MOUSE_CAPTURE"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(2)
	}
	fmt.Print("\x1b[?1000h\x1b[?1006hmouse-ready")
	buf := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			_, _ = f.Write(buf[:n])
			_ = f.Sync()
			if strings.Contains(string(buf[:n]), "r") {
				fmt.Print("\x1b[?1000lreporting-disabled")
			}
		}
		if err != nil {
			_ = f.Close()
			os.Exit(0)
		}
	}
}

func TestMouseWheelRouting(t *testing.T) {
	t.Run("tabs", func(t *testing.T) { testMouseWheelRouting(t, false) })
	t.Run("sidebar", func(t *testing.T) { testMouseWheelRouting(t, true) })
}

func testMouseWheelRouting(t *testing.T, sidebar bool) {
	path := filepath.Join(t.TempDir(), "mouse-input")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMouseAgentProcess$")
	cmd.Env = append(os.Environ(), "MAESTRO_MOUSE_HELPER=1", "MAESTRO_MOUSE_CAPTURE="+path)
	p, err := term.Start(cmd, 80, 20, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	await := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(p.Snapshot(true).Screen, text) {
			if time.Now().After(deadline) {
				t.Fatal("agent did not print", text)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	await("mouse-ready")
	if !p.Snapshot(false).MouseReporting {
		t.Fatal("pane did not expose agent mouse mode")
	}
	m := testModel(t)
	m.width, m.height = 80, 24
	m.tabs[0].pane = p
	if sidebar {
		m.dispatch("s")
	}
	bounds := m.paneBounds()
	snapshot := p.Snapshot(true)
	view := m.View()
	if snapshot.CursorVisible && (view.Cursor == nil || view.Cursor.X != snapshot.X+bounds.x || view.Cursor.Y != snapshot.Y+bounds.y) {
		t.Fatal("cursor not translated into pane bounds")
	}
	wheel := func(button tea.MouseButton) {
		_, _ = m.Update(tea.MouseWheelMsg{X: bounds.x + 4, Y: bounds.y + 3, Button: button})
	}
	wheel(tea.MouseWheelDown)
	wheel(tea.MouseWheelUp)
	if m.scroll != 0 {
		t.Fatal("agent wheel entered Maestro scroll mode")
	}
	// Explicit Maestro scroll mode overrides the agent's request.
	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, _ = m.Update(tea.KeyPressMsg{Code: '['})
	wheel(tea.MouseWheelUp)
	if m.scroll != 4 {
		t.Fatal("explicit scroll mode did not scroll up")
	}
	wheel(tea.MouseWheelDown)
	if m.scroll != 1 {
		t.Fatal("explicit scroll mode did not scroll down")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	// This key is an input barrier: the agent captures preceding events first.
	_, _ = m.Update(tea.KeyPressMsg{Code: 'r'})
	await("reporting-disabled")
	if p.Snapshot(false).MouseReporting {
		t.Fatal("mouse reporting remained enabled")
	}
	wheel(tea.MouseWheelUp)
	if m.scroll != 3 {
		t.Fatal("disabled mouse mode did not use Maestro scrollback")
	}
	wheel(tea.MouseWheelDown)
	if m.scroll != 0 {
		t.Fatal("disabled mouse mode did not return to live pane")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'r'})
	deadline := time.Now().Add(5 * time.Second)
	want := "\x1b[<65;5;4M\x1b[<64;5;4Mrr"
	for {
		b, err := os.ReadFile(path)
		if err == nil && string(b) == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent mouse input: got %q, want %q (%v)", b, want, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop()
	wheel(tea.MouseWheelUp)
	if m.scroll != 3 {
		t.Fatal("stopped pane scrollback unavailable")
	}
}
