package core

import (
	"context"

	"github.com/mmoehabb/maestro/internal/store"
)

func (s *TaskService) SetNotes(ctx context.Context, slug, notes string) error {
	if s.Remote != nil {
		return s.Remote.Call(ctx, "notes", Request{Slug: slug, Text: notes}, nil)
	}
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

func (s *TaskService) Rename(ctx context.Context, slug, title string) error {
	if s.Remote != nil {
		return s.Remote.Call(ctx, "rename", Request{Slug: slug, Text: title}, nil)
	}
	return s.mutateTask(ctx, slug, func(t store.Task) error { return s.Store.SetTitle(ctx, t.ID, title) })
}

func (r *Runtime) Rename(ctx context.Context, task store.Task, title string) error {
	done, err := r.operation(task.ID)
	if err != nil {
		return err
	}
	defer done()
	return r.Service.Store.SetTitle(ctx, task.ID, title)
}
