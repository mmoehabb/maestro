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
	return s.mutateTask(ctx, slug, func(t store.Task) error { return s.Store.SetArchived(ctx, t.ID, true) })
}

func (s *TaskService) Reopen(ctx context.Context, slug string) error {
	return s.mutateTask(ctx, slug, func(t store.Task) error { return s.Store.SetArchived(ctx, t.ID, false) })
}

func (s *TaskService) DeleteArchived(ctx context.Context, slug string) error {
	return s.mutateTask(ctx, slug, func(t store.Task) error { return s.Store.DeleteArchived(ctx, t.ID) })
}

// Archive waits for final history persistence before hiding the task.
func (r *Runtime) Archive(ctx context.Context, task store.Task) error {
	done, err := r.operation(task.ID)
	if err != nil {
		return err
	}
	defer done()
	task, err = r.Service.Find(ctx, task.Slug)
	if err != nil {
		return err
	}
	if old := r.entry(task.ID); old != nil {
		old.pane.Stop()
		<-old.finished
		if old.saveErr != nil {
			if err = r.retryHistory(old); err != nil {
				return err
			}
		}
	}
	if err = r.Service.Store.SetArchived(ctx, task.ID, true); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.panes, task.ID)
	r.mu.Unlock()
	return nil
}
