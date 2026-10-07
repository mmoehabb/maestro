package core

import (
	"context"
	"fmt"

	"github.com/gofrs/flock"

	"github.com/mmoehabb/maestro/internal/store"
)

// Mutations outside a runtime must acquire the project lock, even if this
// service is already in use by a runtime. Runtime archive uses its task lock.
func (s *TaskService) mutateTask(ctx context.Context, slug string, mutate func(store.Task) error) error {
	lock := flock.New(s.LockPath)
	ok, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("this project is busy in another Maestro process; use the active TUI to edit tasks")
	}
	defer lock.Close()
	task, err := s.Find(ctx, slug)
	if err != nil {
		return err
	}
	return mutate(task)
}

func (s *TaskService) Archive(ctx context.Context, slug string) error {
	_, err := s.Workflow(ctx, slug, "archive", WorkflowOptions{})
	return err
}

func (s *TaskService) Reopen(ctx context.Context, slug string) error {
	_, err := s.Workflow(ctx, slug, "reopen", WorkflowOptions{})
	return err
}

func (s *TaskService) DeleteArchived(ctx context.Context, slug string) error {
	return s.mutateTask(ctx, slug, func(t store.Task) error { return s.Store.DeleteArchived(ctx, t.ID) })
}

// Archive waits for final history persistence before hiding the task.
func (r *Runtime) Archive(ctx context.Context, task store.Task) error {
	_, err := r.Workflow(ctx, task, "archive", WorkflowOptions{StopAgent: true})
	return err
}
