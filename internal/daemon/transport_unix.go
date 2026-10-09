//go:build !windows

package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func endpoint(key string) (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("maestro-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || stat.Uid != uint32(os.Getuid()) {
		return "", fmt.Errorf("unsafe daemon socket directory %s", dir)
	}
	return filepath.Join(dir, key+".sock"), nil
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}

func listen(address string) (net.Listener, error) {
	// Called only after acquiring the project lock; an old endpoint is stale.
	if info, err := os.Lstat(address); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("daemon endpoint is not a socket")
		}
		if err = os.Remove(address); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}
func background(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
