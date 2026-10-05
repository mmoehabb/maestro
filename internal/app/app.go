package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

func Open(ctx context.Context, dir string, paths config.Paths) (*core.TaskService, error) {
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
	return &core.TaskService{
		Config: cfg, Repo: repo, Project: project, Store: db,
		LockPath: filepath.Join(paths.DataDir, "locks", repo.Key()+".lock"),
	}, nil
}
