package core

import (
	"context"

	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

// Caller forwards mutations to the process holding the project lock.
type Caller interface {
	Call(context.Context, string, Request, any) error
}
type Request struct {
	Slug, Text, Action                string
	TaskID, Generation                int64
	Task                              store.Task
	New                               NewTask
	Options                           WorkflowOptions
	IDs                               []int64
	Cols, Rows                        int
	Fresh, Confirmed, Ensure, Replace bool
}

func (r *Runtime) EventStream() <-chan Event { return r.Events }

type PaneInfo struct {
	TaskID, Generation int64
	Pane               *term.Pane
}

func (r *Runtime) Panes() []PaneInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]PaneInfo, 0, len(r.panes))
	for id, entry := range r.panes {
		result = append(result, PaneInfo{id, entry.session.ID, entry.pane})
	}
	return result
}

// EnsureStarted attaches to an existing process, including its exit screen.
func (r *Runtime) EnsureStarted(task store.Task, cols, rows int) (*term.Pane, error) {
	done, err := r.operation(task.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	if entry := r.entry(task.ID); entry != nil {
		return entry.pane, entry.pane.Resize(cols, rows)
	}
	task, err = r.Service.Find(r.ctx, task.Slug)
	if err != nil {
		return nil, err
	}
	return r.start(r.ctx, task, cols, rows, false, nil)
}
