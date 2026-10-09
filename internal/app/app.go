package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmoehabb/maestro/internal/daemon"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/forge/github"
	"github.com/mmoehabb/maestro/internal/forge/gitlab"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

func Open(ctx context.Context, dir string, paths config.Paths) (*core.TaskService, error) {
	s, err := OpenLocal(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	if err = s.DiscoverTasks(ctx); err != nil {
		_ = s.Store.Close()
		return nil, err
	}
	return s, nil
}

// OpenLocal bypasses discovery so conflicting checkpoints can be reconciled.
func OpenLocal(ctx context.Context, dir string, paths config.Paths) (*core.TaskService, error) {
	repo, err := git.Discover(ctx, dir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(paths, repo.Root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(paths.DataDir, "locks"), 0o700); err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, filepath.Join(paths.DataDir, "maestro.db"))
	if err != nil {
		return nil, err
	}
	project, err := db.EnsureProject(ctx, store.Project{Root: repo.Root, Remote: repo.Remote, DefaultBranch: repo.DefaultBranch})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	var provider forge.Provider = github.New(cfg.GitHub.Token)
	host := forge.RemoteHost(repo.Remote)
	if host == "gitlab.com" || (cfg.GitLab.Host != "" && strings.EqualFold(host, cfg.GitLab.Host)) {
		provider = gitlab.New(host, cfg.GitLab.Token)
	}
	service := &core.TaskService{
		Forge: provider, DataDir: paths.DataDir,
		Config: cfg, Repo: repo, Project: project, Store: db,
		LockPath: filepath.Join(paths.DataDir, "locks", repo.Key()+".lock"),
	}
	address, err := daemon.Endpoint(paths.DataDir, repo.Key())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	probe, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err = daemon.Probe(probe, address); err == nil {
		service.Remote = daemon.Remote{Address: address}
	}
	return service, nil
}
