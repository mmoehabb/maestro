package core

import (
	"context"
	"fmt"

	"github.com/mmoehabb/maestro/internal/store"
)

func (s *TaskService) Find(ctx context.Context, slug string) (store.Task, error) {
	tasks, err := s.List(ctx, true)
	if err != nil {
		return store.Task{}, err
	}
	for _, t := range tasks {
		if t.Slug == slug {
			return t, nil
		}
	}
	return store.Task{}, fmt.Errorf("task %q not found", slug)
}

func (s *TaskService) History(ctx context.Context, slug string) (store.History, error) {
	task, err := s.Find(ctx, slug)
	if err != nil {
		return store.History{}, err
	}
	return s.Store.History(ctx, task)
}
