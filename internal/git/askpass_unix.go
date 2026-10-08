//go:build !windows

package git

import (
	"os/exec"
	"syscall"
)

func prepareCredentialCommand(cmd *exec.Cmd) {
	// Isolate background Git from Maestro's terminal and stop its SSH/helper
	// descendants together when the user cancels authentication.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
