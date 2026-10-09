package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/notify"
	"github.com/mmoehabb/maestro/internal/term"
)

type Server struct {
	Runtime       *core.Runtime
	Notifier      notify.Notifier
	mu            sync.Mutex
	lease         string
	cleanup       map[int64]bool
	lastError     string
	notifications chan notification
	cancel        context.CancelFunc
}

// Serve assumes Runtime owns the project lock before replacing a stale socket.
func (s *Server) Serve(ctx context.Context, address string) error {
	listener, err := listen(address)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.cancel = cancel
	s.cleanup = map[int64]bool{}
	s.notifications = make(chan notification, 16)
	stopped := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopped()
	var workers sync.WaitGroup
	workers.Go(func() { s.events(ctx) })
	workers.Go(func() { s.deliverNotifications(ctx) })
	defer workers.Wait()
	defer cancel()
	slots := make(chan struct{}, 64)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		workers.Go(func() { defer func() { <-slots }(); defer conn.Close(); s.serveConn(ctx, conn) })
	}
}

func (s *Server) serveConn(parent context.Context, conn net.Conn) {
	closeOnStop := context.AfterFunc(parent, func() { _ = conn.Close() })
	defer closeOnStop()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	var req request
	if err := decode(conn, &req); err != nil {
		return
	}
	if req.Version != ProtocolVersion {
		_ = encode(conn, result(nil, fmt.Errorf("daemon protocol mismatch: server %d, client %d; stop the old daemon before upgrading", ProtocolVersion, req.Version)))
		return
	}
	if req.Method == "attach" {
		s.mu.Lock()
		if s.lease != "" {
			s.mu.Unlock()
			_ = encode(conn, result(nil, fmt.Errorf("a TUI is already attached to this project")))
			return
		}
		lease := rand.Text()
		s.lease = lease
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			if s.lease == lease {
				s.lease = ""
			}
			s.mu.Unlock()
		}()
		if err := encode(conn, result(lease, nil)); err != nil {
			return
		}
		_ = conn.SetDeadline(time.Time{})
		stop := context.AfterFunc(parent, func() { _ = conn.Close() })
		defer stop()
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	replies := make(chan response, 1)
	// EOF cancels in-flight work; credential answers share this connection.
	go func() {
		defer cancel()
		for {
			var reply response
			if decode(conn, &reply) != nil {
				return
			}
			select {
			case replies <- reply:
			case <-ctx.Done():
				return
			}
		}
	}()
	ctx = git.WithCredentialPrompt(ctx, func(ctx context.Context, prompt string) (string, error) {
		if err := encode(conn, response{Prompt: prompt}); err != nil {
			return "", err
		}
		select {
		case answer := <-replies:
			if answer.Error != "" {
				return "", git.ErrCredentialCanceled
			}
			var secret string
			err := json.Unmarshal(answer.Data, &secret)
			return secret, err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	value, err := s.handle(ctx, req)
	_ = encode(conn, result(value, err))
	if req.Method == "shutdown" {
		s.cancel()
	}
}

func (s *Server) attached(lease string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return lease != "" && s.lease == lease
}

func (s *Server) handle(ctx context.Context, req request) (any, error) {
	r, a := s.Runtime, req.Args
	switch req.Method {
	case "status":
		s.mu.Lock()
		attached := s.lease != ""
		s.mu.Unlock()
		active := 0
		for _, p := range r.Panes() {
			select {
			case <-p.Pane.Done():
			default:
				active++
			}
		}
		return Status{os.Getpid(), attached, active, ProtocolVersion}, nil
	case "detach":
		s.mu.Lock()
		defer s.mu.Unlock()
		if req.Lease != "" && s.lease == req.Lease {
			s.lease = ""
		}
		return nil, nil
	case "shutdown":
		return nil, r.Close()
	case "sync":
		if !s.attached(req.Lease) {
			return nil, fmt.Errorf("TUI detached; run maestro attach to reconnect")
		}
		var state syncState
		tasks, err := r.Service.List(ctx, true)
		if err != nil {
			return nil, err
		}
		state.Tasks = tasks
		for _, info := range r.Panes() {
			state.Panes = append(state.Panes, snapshot(info))
		}
		s.mu.Lock()
		state.Error = s.lastError
		for _, task := range tasks {
			if task.Lifecycle != "merged" && task.Lifecycle != "closed" && !task.CleanupPending {
				delete(s.cleanup, task.ID)
			}
			if s.cleanup[task.ID] && task.Lifecycle != "archived" {
				state.Cleanup = append(state.Cleanup, task.ID)
			}
		}
		s.mu.Unlock()
		return state, nil
	case "create":
		return r.Create(ctx, a.New)
	case "restore":
		return nil, r.RestoreTasks(ctx, a.Text, a.Replace)
	case "reorder":
		return nil, r.ReorderTasks(ctx, a.IDs)
	case "auth-reset":
		if p, ok := r.Service.Forge.(interface{ ResetAuth() }); ok {
			p.ResetAuth()
		}
		return nil, nil
	case "input", "resize", "scrollback":
		if !s.attached(req.Lease) {
			return nil, fmt.Errorf("input requires an attached TUI")
		}
		for _, info := range r.Panes() {
			if info.TaskID != a.TaskID || info.Generation != a.Generation {
				continue
			}
			if req.Method == "resize" {
				if a.Cols < 1 || a.Rows < 1 || a.Cols > 1000 || a.Rows > 500 {
					return nil, fmt.Errorf("invalid terminal dimensions")
				}
				return nil, info.Pane.Resize(a.Cols, a.Rows)
			}
			if req.Method == "scrollback" {
				return boundedScrollback(info.Pane.Scrollback()), nil
			}
			switch a.Action {
			case "key":
				info.Pane.Key(req.Key, req.Release)
			case "paste":
				info.Pane.Paste(a.Text)
			case "mouse":
				info.Pane.Mouse(req.Mouse, req.Release, req.Motion)
			default:
				return nil, fmt.Errorf("unknown input action")
			}
			return nil, nil
		}
		return nil, fmt.Errorf("agent pane was replaced; refresh before sending input")
	}
	task, err := r.Service.Find(ctx, a.Slug)
	if a.Slug == "" && a.Task.Slug != "" {
		task, err = r.Service.Find(ctx, a.Task.Slug)
	}
	if a.Slug == "" && a.Task.Slug == "" && a.TaskID != 0 {
		tasks, e := r.Service.List(ctx, true)
		if e != nil {
			return nil, e
		}
		for _, t := range tasks {
			if t.ID == a.TaskID {
				task = t
				err = nil
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}
	switch req.Method {
	case "start", "switch":
		if !s.attached(req.Lease) {
			return nil, fmt.Errorf("launch requires an attached TUI")
		}
		if a.Cols < 1 || a.Rows < 1 || a.Cols > 1000 || a.Rows > 500 {
			return nil, fmt.Errorf("invalid terminal dimensions")
		}
		var pane *term.Pane
		switch {
		case req.Method == "switch":
			if a.Fresh {
				pane, err = r.SwitchFresh(ctx, task, a.Text, a.Cols, a.Rows, a.Confirmed)
			} else {
				pane, err = r.Switch(ctx, task, a.Text, a.Cols, a.Rows, a.Confirmed)
			}
		case a.Ensure && !a.Fresh:
			pane, err = r.EnsureStarted(task, a.Cols, a.Rows)
		default:
			pane, err = r.Start(task, a.Cols, a.Rows, a.Fresh)
		}
		if err != nil {
			return nil, err
		}
		for _, info := range r.Panes() {
			if info.Pane == pane {
				return snapshot(info), nil
			}
		}
		return nil, fmt.Errorf("pane disappeared during launch")
	case "stop":
		r.Stop(task.ID)
		return nil, nil
	case "workflow":
		return r.Workflow(ctx, task, a.Action, a.Options)
	case "notes":
		return nil, r.SetNotes(ctx, task, a.Text)
	case "rename":
		return nil, r.Rename(ctx, task, a.Text)
	case "delete-task":
		return nil, r.DeleteArchived(ctx, task)
	case "checkpoint":
		return r.CheckpointOnBranch(ctx, task, a.Text)
	default:
		return nil, fmt.Errorf("unknown daemon method %q", req.Method)
	}
}

func snapshot(info core.PaneInfo) paneState {
	state := paneState{TaskID: info.TaskID, Generation: info.Generation, Snapshot: info.Pane.Snapshot(true)}
	if state.Snapshot.InputError != nil {
		state.InputError = state.Snapshot.InputError.Error()
	}
	return state
}

func boundedScrollback(lines []string) []string {
	size := 0
	for i := len(lines) - 1; i >= 0; i-- {
		size += len(lines[i]) + 1
		if size > 2<<20 {
			return append([]string{"[older scrollback omitted from daemon response]"}, lines[i+1:]...)
		}
	}
	return lines
}

func (s *Server) events(ctx context.Context) {
	states := map[int64]term.Snapshot{}
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-s.Runtime.Events:
			if !ok {
				return
			}
			s.mu.Lock()
			if event.Cleanup && event.Task != nil {
				s.cleanup[event.Task.ID] = true
			}
			if event.Err != nil {
				s.lastError = event.Err.Error()
			}
			s.mu.Unlock()
		case <-timer.C:
			panes := s.Runtime.Panes()
			for generation := range states {
				if !slices.ContainsFunc(panes, func(p core.PaneInfo) bool { return p.Generation == generation }) {
					delete(states, generation)
				}
			}
			for _, info := range panes {
				current := info.Pane.Snapshot(false)
				previous, known := states[info.Generation]
				states[info.Generation] = current
				if known && current.Revision == previous.Revision || (current.State != term.Done && current.State != term.NeedsInput) {
					continue
				}
				s.mu.Lock()
				detached := s.lease == ""
				s.mu.Unlock()
				if detached && s.Notifier != nil && slices.Contains(s.Runtime.Service.Config.Activity.NotifyOn, string(current.State)) {
					// Notification delivery must never back up the runtime event consumer.
					select {
					case s.notifications <- notification{info.TaskID, string(current.State)}:
					default:
					}
				}
			}
		}
	}
}

type notification struct {
	id    int64
	state string
}

func (s *Server) deliverNotifications(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-s.notifications:
			s.mu.Lock()
			detached := s.lease == ""
			s.mu.Unlock()
			if !detached {
				continue
			}
			tasks, err := s.Runtime.Service.List(ctx, false)
			if err != nil {
				continue
			}
			for _, task := range tasks {
				if task.ID == n.id {
					op, cancel := context.WithTimeout(ctx, 8*time.Second)
					_ = s.Notifier.Send(op, "Maestro", task.Title+": "+n.state)
					cancel()
					break
				}
			}
		}
	}
}
