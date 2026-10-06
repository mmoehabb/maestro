package core

import (
	"context"

	"github.com/mmoehabb/maestro/internal/store"
)

func (s *TaskService) SetNotes(ctx context.Context, slug, notes string) error {
	return s.mutateTask(ctx, slug, func(t store.Task) error { return s.Store.SetNotes(ctx, t.ID, notes) })
}

func (r *Runtime) SetNotes(ctx context.Context, task store.Task, notes string) error {
	done, err := r.operation(task.ID)
	if err != nil {
		return err
	}
	defer done()
	task, err = r.Service.Find(ctx, task.Slug)
	if err != nil {
		return err
	}
	return r.Service.Store.SetNotes(ctx, task.ID, notes)
}
