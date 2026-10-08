package core

import (
	"context"

	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

func (s *TaskService) Diff(ctx context.Context, task store.Task) (git.DiffResult, error) {
	return git.WorktreeDiff(ctx, task.Worktree, task.BaseBranch)
}

func (r *Runtime) ReorderTasks(ctx context.Context, ids []int64) error {
	// Project-wide ordering is serialized independently of per-task operations;
	// the store transaction rejects a concurrent archive/create snapshot.
	unlock, err := r.operation(0)
	if err != nil {
		return err
	}
	defer unlock()
	return r.Service.Store.ReorderTasks(ctx, r.Service.Project.ID, ids)
}
