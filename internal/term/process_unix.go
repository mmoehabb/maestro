//go:build !windows

package term

import (
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

// xpty's master is a blocking os.File. Duplicate it in nonblocking mode so Go's
// poller can interrupt both reads and writes when this transport is closed.
func paneTransport(pt xpty.Pty) (io.ReadWriteCloser, error) {
	p := pt.(*xpty.UnixPty)
	fd := -1
	var dupErr error
	if err := p.Control(func(master uintptr) {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		fd, dupErr = syscall.Dup(int(master))
		if dupErr == nil {
			syscall.CloseOnExec(fd)
		}
	}); err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, dupErr
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "maestro-pty"), nil
}

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}

func closeSlave(pt xpty.Pty) {
	if p, ok := pt.(*xpty.UnixPty); ok {
		_ = p.Slave().Close()
	}
}

func interruptProcess(cmd *exec.Cmd, _ *inputQueue) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

func ownProcess(cmd *exec.Cmd) (func(), error) {
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }, nil
}
