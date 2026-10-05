package term

import (
	"fmt"
	"io"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Emulator keeps the experimental vt implementation out of Pane and the TUI.
// Its caller serializes screen and input operations; Read runs independently.
type Emulator interface {
	io.ReadWriteCloser
	Resize(int, int)
	Render() string
	Cursor() (int, int, bool)
	Scrollback() []string
	Title() string
	KeyboardMode() int
	Key(uv.Key, bool)
	Paste(string)
	Mouse(uv.Mouse, bool, bool)
}

type virtualTerminal struct {
	vt      *vt.Emulator
	title   string
	visible bool
	flags   int
	stack   []int
}

func NewEmulator(cols, rows int, complete func()) Emulator {
	e := &virtualTerminal{vt: vt.NewEmulator(cols, rows), visible: true}
	e.vt.SetScrollbackSize(10000)
	e.vt.SetCallbacks(vt.Callbacks{Title: func(s string) { e.title = s }, CursorVisibility: func(v bool) { e.visible = v }, Bell: complete})
	for _, osc := range []int{9, 777} {
		e.vt.RegisterOscHandler(osc, func([]byte) bool { complete(); return true })
	}
	e.vt.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(ansi.Params) bool {
		_, _ = fmt.Fprintf(e.vt.InputPipe(), "\x1b[?%du", e.flags)
		return true
	})
	e.vt.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(p ansi.Params) bool {
		if len(e.stack) < 32 {
			e.stack = append(e.stack, e.flags)
		}
		e.flags, _, _ = p.Param(0, 0)
		return true
	})
	e.vt.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(p ansi.Params) bool {
		n, _, _ := p.Param(0, 1)
		for range min(max(n, 1), len(e.stack)) {
			e.flags = e.stack[len(e.stack)-1]
			e.stack = e.stack[:len(e.stack)-1]
		}
		return true
	})
	e.vt.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(p ansi.Params) bool {
		flags, _, _ := p.Param(0, 0)
		mode, _, _ := p.Param(1, 1)
		switch mode {
		case 1:
			e.flags = flags
		case 2:
			e.flags |= flags
		case 3:
			e.flags &^= flags
		}
		return true
	})
	return e
}

func (e *virtualTerminal) Write(b []byte) (int, error) { return e.vt.Write(b) }
func (e *virtualTerminal) Read(b []byte) (int, error)  { return e.vt.Read(b) }

// Close the pipe directly: vt.Close mutates an unsynchronized flag also read by
// its independent Read goroutine. The wrapper owns the emulator lifetime.
func (e *virtualTerminal) Close() error      { return e.vt.InputPipe().(io.Closer).Close() }
func (e *virtualTerminal) Resize(c, r int)   { e.vt.Resize(c, r) }
func (e *virtualTerminal) Render() string    { return e.vt.Render() }
func (e *virtualTerminal) Title() string     { return e.title }
func (e *virtualTerminal) KeyboardMode() int { return e.flags }
func (e *virtualTerminal) Cursor() (int, int, bool) {
	p := e.vt.CursorPosition()
	return p.X, p.Y, e.visible
}
func (e *virtualTerminal) Paste(s string) { e.vt.Paste(s) }
func (e *virtualTerminal) Scrollback() []string {
	lines := make([]string, e.vt.ScrollbackLen())
	for y := range lines {
		var b strings.Builder
		for x := 0; x < e.vt.Width(); x++ {
			if c := e.vt.ScrollbackCellAt(x, y); c != nil {
				b.WriteString(c.Content)
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return append(lines, strings.Split(e.vt.String(), "\n")...)
}

func (e *virtualTerminal) Mouse(m uv.Mouse, release, motion bool) {
	var event uv.MouseEvent = uv.MouseClickEvent(m)
	if release {
		event = uv.MouseReleaseEvent(m)
	} else if motion {
		event = uv.MouseMotionEvent(m)
	}
	e.vt.SendMouse(event)
}

func (e *virtualTerminal) Key(k uv.Key, release bool) {
	if seq, handled := encodeEnhanced(k, release, e.flags); handled {
		_, _ = io.WriteString(e.vt.InputPipe(), seq)
		return
	}
	// vt's legacy encoder compares complete structs, including Text. Normalize
	// metadata first, preserving composed text and modified navigation keys.
	if release {
		return
	}
	if k.Text != "" && k.Mod & ^(uv.ModShift|uv.ModCapsLock|uv.ModNumLock) == 0 {
		e.vt.SendText(k.Text)
		return
	}
	k.Text = ""
	k.IsRepeat = false
	k.ShiftedCode = 0
	k.BaseCode = 0
	k.Mod &^= uv.ModCapsLock | uv.ModNumLock
	if k.Mod != 0 {
		if suffix, ok := navigation[k.Code]; ok {
			_, _ = fmt.Fprintf(e.vt.InputPipe(), "\x1b[1;%d%s", 1+int(k.Mod), suffix)
			return
		}
	}
	e.vt.SendKey(uv.KeyPressEvent(k))
}
