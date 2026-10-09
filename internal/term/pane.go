package term

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/xpty"
)

type Snapshot struct {
	State          State
	Revision       uint64
	Screen, Title  string
	X, Y           int
	CursorVisible  bool
	ExitCode       int
	InputError     error `json:"-"`
	HasOutput      bool
	MouseReporting bool
}

type Pane struct {
	remote    RemotePane
	mu        sync.Mutex
	pty       xpty.Pty
	emu       Emulator
	cmd       *exec.Cmd
	activity  Activity
	done      chan struct{}
	dirty     chan struct{}
	exitCode  int
	stop      sync.Once
	kill      func()
	input     *inputQueue
	hasOutput bool
}

func Start(cmd *exec.Cmd, cols, rows int, idle time.Duration, hints []string) (*Pane, error) {
	pt, err := xpty.NewPty(max(1, cols), max(1, rows))
	if err != nil {
		return nil, err
	}
	p := &Pane{pty: pt, cmd: cmd, done: make(chan struct{}), dirty: make(chan struct{}, 1), activity: Activity{State: Starting, IdleAfter: idle, Hints: hints}}
	p.emu = NewEmulator(max(1, cols), max(1, rows), p.activity.Complete)
	prepareProcess(cmd)
	if err := pt.Start(cmd); err != nil {
		_ = pt.Close()
		_ = p.emu.Close()
		return nil, err
	}
	p.kill, err = ownProcess(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = xpty.WaitProcess(context.Background(), cmd)
		_ = pt.Close()
		_ = p.emu.Close()
		return nil, err
	}
	closeSlave(pt)
	transport, err := paneTransport(pt)
	if err != nil {
		p.kill()
		_ = xpty.WaitProcess(context.Background(), cmd)
		_ = pt.Close()
		_ = p.emu.Close()
		return nil, err
	}
	p.input = newInputQueue()
	readDone := make(chan struct{})
	writeDone := make(chan struct{})
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		_, _ = io.Copy(p.input, p.emu)
		// Overflow must release synchronous emulator writers as well.
		_ = p.emu.Close()
	}()
	go func() { defer close(writeDone); _, _ = io.Copy(transport, p.input) }()
	go func() {
		defer close(readDone)
		buf := make([]byte, 32768)
		for {
			n, err := transport.Read(buf)
			if n > 0 {
				p.mu.Lock()
				p.hasOutput = true
				p.activity.Output(buf[:n], time.Now())
				_, _ = p.emu.Write(buf[:n])
				p.mu.Unlock()
				p.invalidate()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		_ = xpty.WaitProcess(context.Background(), cmd)
		// Drain final output before closing. A descendant may retain the PTY;
		// do not let that prevent shutdown or retain subprocesses after exit.
		p.kill()
		select {
		case <-readDone:
		case <-time.After(150 * time.Millisecond):
		}
		p.input.Close()
		_ = transport.Close()
		_ = pt.Close()
		// Closing the emulator's input pipe releases a writer blocked on replies.
		_ = p.emu.Close()
		<-readDone
		<-writeDone
		<-inputDone
		p.mu.Lock()
		p.exitCode = -1
		if cmd.ProcessState != nil {
			p.exitCode = cmd.ProcessState.ExitCode()
		}
		state := Exited
		if p.exitCode != 0 {
			state = Crashed
		}
		p.activity.setState(state)
		p.mu.Unlock()
		p.invalidate()
		close(p.done)
	}()
	return p, nil
}

func (p *Pane) invalidate() {
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}
func (p *Pane) Dirty() <-chan struct{} { return p.dirty }
func (p *Pane) Done() <-chan struct{}  { return p.done }
func (p *Pane) Snapshot(render bool) Snapshot {
	if p.remote != nil {
		return p.remote.Snapshot(render)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activity.Tick(time.Now())
	s := Snapshot{State: p.activity.State, Revision: p.activity.Revision, Title: p.emu.Title(), ExitCode: p.exitCode, InputError: p.input.Err(), HasOutput: p.hasOutput, MouseReporting: p.emu.MouseReporting()}
	if render {
		s.Screen = p.emu.Render()
		s.X, s.Y, s.CursorVisible = p.emu.Cursor()
	}
	return s
}

func (p *Pane) Scrollback() []string {
	if p.remote != nil {
		return p.remote.Scrollback()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.emu.Scrollback()
}

func (p *Pane) Resize(cols, rows int) error {
	if p.remote != nil {
		return p.remote.Resize(cols, rows)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu.Resize(max(1, cols), max(1, rows))
	select {
	case <-p.done:
		return nil
	default:
		return p.pty.Resize(max(1, cols), max(1, rows))
	}
}

func (p *Pane) Key(k uv.Key, release bool) {
	if p.remote != nil {
		p.remote.Key(k, release)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activity.State == Exited || p.activity.State == Crashed {
		return
	}
	if !release {
		p.activity.Input(time.Now())
	}
	p.emu.Key(k, release)
}

func (p *Pane) Paste(s string) {
	if p.remote != nil {
		p.remote.Paste(s)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activity.State == Exited || p.activity.State == Crashed {
		return
	}
	p.activity.Input(time.Now())
	p.emu.Paste(s)
}

func (p *Pane) Mouse(m uv.Mouse, release, motion bool) {
	if p.remote != nil {
		p.remote.Mouse(m, release, motion)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activity.State == Exited || p.activity.State == Crashed {
		return
	}
	p.emu.Mouse(m, release, motion)
}

func (p *Pane) Stop() {
	if p.remote != nil {
		p.remote.Stop()
		return
	}
	p.stop.Do(func() {
		select {
		case <-p.done:
			return
		default:
		}
		interruptProcess(p.cmd, p.input)
		select {
		case <-p.done:
			return
		case <-time.After(2 * time.Second):
			p.kill()
		}
		<-p.done
	})
}

func (p *Pane) NativeEvent(kind string) Snapshot {
	p.mu.Lock()
	p.activity.NativeEvent(kind)
	state := Snapshot{State: p.activity.State, Revision: p.activity.Revision}
	p.mu.Unlock()
	p.invalidate()
	return state
}

// RemotePane supplies cached render state and ordered input to a daemon pane.
type RemotePane interface {
	FetchScrollback(context.Context) ([]string, error)
	Snapshot(bool) Snapshot
	Scrollback() []string
	Resize(int, int) error
	Key(uv.Key, bool)
	Paste(string)
	Mouse(uv.Mouse, bool, bool)
	Stop()
}

func NewRemotePane(remote RemotePane) *Pane { return &Pane{remote: remote} }

// FetchScrollback obtains current history for an explicit copy operation.
func (p *Pane) FetchScrollback(ctx context.Context) ([]string, error) {
	if p.remote != nil {
		return p.remote.FetchScrollback(ctx)
	}
	return p.Scrollback(), nil
}

func (p *Pane) IsRemote() bool { return p.remote != nil }
