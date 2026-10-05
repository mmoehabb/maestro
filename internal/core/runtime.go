package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mmoehabb/maestro/internal/agent"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type Event struct {
	TaskID int64
	State  term.State
	Err    error
}
type running struct {
	task     store.Task
	pane     *term.Pane
	session  store.Session
	finished chan struct{}
	ignoreID string
}

type Runtime struct {
	Service     *TaskService
	Events      chan Event
	mu          sync.Mutex
	panes       map[int64]*running
	closed      bool
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	errMu       sync.Mutex
	writeErrors []error
}

func (r *Runtime) Create(ctx context.Context, in NewTask) (store.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return store.Task{}, errors.New("runtime is closed")
	}
	operation, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return r.Service.Create(operation, in)
}

func (s *TaskService) OpenRuntime() (*Runtime, error) {
	if err := s.acquire(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{Service: s, Events: make(chan Event, 128), panes: make(map[int64]*running), ctx: ctx, cancel: cancel}, nil
}

func (r *Runtime) Start(task store.Task, cols, rows int, fresh bool) (*term.Pane, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("runtime is closed")
	}
	if old := r.panes[task.ID]; old != nil {
		old.pane.Stop()
		<-old.finished
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	cfg, ok := r.Service.Config.Agents[task.Agent]
	if !ok {
		return nil, fmt.Errorf("agent %q is no longer configured", task.Agent)
	}
	g := agent.Generic{Name: task.Agent, Config: cfg}
	previous, err := r.Service.Store.LatestSession(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	nativeID := previous.NativeID
	ignoreID := ""
	if fresh {
		ignoreID = previous.NativeID
		if ignoreID == "" {
			ignoreID, _ = g.DiscoverSession(ctx, task.Worktree, previous.StartedAt)
		}
		nativeID = ""
		previous = store.Session{}
	}
	if previous.ID != 0 && nativeID == "" && len(cfg.Resume) > 0 {
		nativeID, err = g.DiscoverSession(ctx, task.Worktree, previous.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("discover saved session: %w", err)
		}
		if nativeID == "" {
			return nil, errors.New("no native session ID found; use prefix R to explicitly start a fresh session")
		}
	}
	newSession := nativeID == ""
	if newSession && cfg.GenerateSessionID {
		nativeID = uuid.NewString()
	}
	prompt := ""
	if newSession {
		prompt = task.Prompt
	}
	cmd, err := g.Command(context.Background(), agent.LaunchSpec{
		Dir: task.Worktree, SessionID: nativeID, NewSession: newSession, Prompt: prompt,
		Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "MAESTRO_TASK=" + task.Slug, "MAESTRO_SESSION_ID=" + nativeID},
	})
	if err != nil {
		return nil, err
	}
	idle, _ := time.ParseDuration(r.Service.Config.Activity.IdleAfter)
	started := time.Now()
	pane, err := term.Start(cmd, cols, rows, idle, g.Hints())
	if err != nil {
		return nil, err
	}
	session, err := r.Service.Store.StartSession(ctx, task, nativeID, started)
	if err != nil {
		pane.Stop()
		return nil, err
	}
	entry := &running{task: task, pane: pane, session: session, finished: make(chan struct{}), ignoreID: ignoreID}
	r.panes[task.ID] = entry
	r.wg.Add(1)
	go r.watch(entry, g)
	return pane, nil
}

func (r *Runtime) emit(e Event) {
	select {
	case r.Events <- e:
	default:
	}
}

func (r *Runtime) recordWriteError(taskID int64, err error) {
	r.errMu.Lock()
	r.writeErrors = append(r.writeErrors, err)
	r.errMu.Unlock()
	r.emit(Event{TaskID: taskID, Err: err})
}

func (r *Runtime) watch(entry *running, g agent.Generic) {
	defer r.wg.Done()
	defer close(entry.finished)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := term.Starting
	lastDiscovery := time.Time{}
	discover := func() {
		if entry.session.NativeID != "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		id, err := g.DiscoverSession(ctx, entry.task.Worktree, entry.session.StartedAt)
		if err != nil {
			r.emit(Event{TaskID: entry.task.ID, Err: fmt.Errorf("session discovery: %w", err)})
			return
		}
		if id != "" && id != entry.ignoreID {
			if err := r.Service.Store.SetNativeID(ctx, entry.session.ID, id); err != nil {
				r.recordWriteError(entry.task.ID, err)
			} else {
				entry.session.NativeID = id
			}
		}
	}
	save := func(final bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := r.Service.Store.SaveScrollback(ctx, entry.session.ID, strings.Join(entry.pane.Scrollback(), "\n"))
		if final {
			err = errors.Join(err, r.Service.Store.EndSession(ctx, entry.session.ID, entry.pane.Snapshot(false).ExitCode))
		}
		if err != nil {
			r.recordWriteError(entry.task.ID, err)
		}
	}
	for {
		select {
		case <-entry.pane.Done():
			discover()
			save(true)
			r.emit(Event{TaskID: entry.task.ID, State: entry.pane.Snapshot(false).State})
			return
		case now := <-ticker.C:
			state := entry.pane.Snapshot(false).State
			if state != last {
				last = state
				r.emit(Event{TaskID: entry.task.ID, State: state})
				if state == term.Done {
					save(false)
				}
			}
			if now.Sub(lastDiscovery) >= time.Second {
				lastDiscovery = now
				discover()
			}
		}
	}
}

func (r *Runtime) Stop(taskID int64) {
	r.mu.Lock()
	entry := r.panes[taskID]
	r.mu.Unlock()
	if entry != nil {
		entry.pane.Stop()
		<-entry.finished
	}
}

func (r *Runtime) Close() error {
	r.cancel()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	entries := make([]*running, 0, len(r.panes))
	for _, entry := range r.panes {
		entries = append(entries, entry)
	}
	r.mu.Unlock()
	var stops sync.WaitGroup
	for _, entry := range entries {
		stops.Go(entry.pane.Stop)
	}
	stops.Wait()
	r.wg.Wait()
	close(r.Events)
	r.errMu.Lock()
	err := errors.Join(r.writeErrors...)
	r.errMu.Unlock()
	return errors.Join(err, r.Service.release())
}
