package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/mmoehabb/maestro/internal/config"
)

// Ensure serializes startup separately from the lifetime project lock.
func Ensure(ctx context.Context, address, root, key string, paths config.Paths) error {
	lock := flock.New(filepath.Join(paths.DataDir, "locks", key+".startup.lock"))
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("daemon startup lock unavailable")
	}
	defer lock.Close()
	probe, cancel := context.WithTimeout(ctx, time.Second)
	_, err = Probe(probe, address)
	cancel()
	if err == nil {
		return nil
	}
	// A listening incompatible daemon must not be replaced or hidden by startup.
	probe, cancel = context.WithTimeout(ctx, time.Second)
	conn, dialErr := dial(probe, address)
	cancel()
	if dialErr == nil {
		_ = conn.Close()
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "daemon", "serve", "--dir", root, "--data-dir", paths.DataDir, "--config-file", paths.ConfigFile)
	background(cmd)
	logPath := filepath.Join(paths.DataDir, "locks", key+".daemon.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("daemon did not become ready; inspect %s", logPath)
		case err := <-finished:
			if err != nil {
				return fmt.Errorf("daemon exited during startup; inspect %s: %w", logPath, err)
			}
			return fmt.Errorf("daemon exited during startup; inspect %s", logPath)
		case <-timer.C:
			probe, cancel := context.WithTimeout(ctx, time.Second)
			_, err = Probe(probe, address)
			cancel()
			if err == nil {
				return nil
			}
		}
	}
}
