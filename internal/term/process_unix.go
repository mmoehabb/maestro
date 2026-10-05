//go:build !windows

package term

import (
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}

func closeSlave(pt xpty.Pty) {
	if p, ok := pt.(*xpty.UnixPty); ok {
		_ = p.Slave().Close()
	}
}
func interruptProcess(cmd *exec.Cmd, _ xpty.Pty) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
func ownProcess(cmd *exec.Cmd) (func(), error) {
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }, nil
}
