package app

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/daemon"
	"github.com/mmoehabb/maestro/internal/notify"
)

func ServeDaemon(ctx context.Context, dir string, paths config.Paths) (err error) {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s, err := OpenLocal(ctx, dir, paths)
	if err != nil {
		return err
	}
	s.Remote = nil
	defer func() { err = errors.Join(err, s.Store.Close()) }()
	if err = s.DiscoverTasks(ctx); err != nil {
		return err
	}
	runtime, err := s.OpenRuntime()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runtime.Close()) }()
	address, err := daemon.Endpoint(paths.DataDir, s.Repo.Key())
	if err != nil {
		return err
	}
	server := daemon.Server{Runtime: runtime, Notifier: notify.Desktop{}}
	return server.Serve(ctx, address)
}
