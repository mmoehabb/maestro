package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type Client struct {
	Remote
	leaseConn    net.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	events       chan core.Event
	mu           sync.Mutex
	panes        map[int64]*remotePane
	started      map[int64]bool
	tasks        map[int64]store.Task
	cleanup      map[int64]bool
	lastError    string
	input        chan request
	pendingBytes int
	failure      error
}

func Attach(ctx context.Context, address string) (*Client, error) {
	conn, err := connection(ctx, address)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err = encode(conn, request{Version: ProtocolVersion, Method: "attach"}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var reply response
	if err = decode(conn, &reply); err == nil {
		err = responseError(reply)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	var lease string
	if err = json.Unmarshal(reply.Data, &lease); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	local, cancel := context.WithCancel(ctx)
	c := &Client{Remote: Remote{address, lease}, leaseConn: conn, ctx: local, cancel: cancel, events: make(chan core.Event, 128), panes: map[int64]*remotePane{}, started: map[int64]bool{}, tasks: map[int64]store.Task{}, cleanup: map[int64]bool{}, input: make(chan request, 256)}
	c.wg.Go(c.poll)
	c.wg.Go(c.writeInput)
	return c, nil
}

func (c *Client) Close() error {
	deadline := time.Now().Add(3 * time.Second)
	var flushErr error
	for {
		c.mu.Lock()
		pending := c.pendingBytes
		c.mu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) || c.ctx.Err() != nil {
			flushErr = fmt.Errorf("detached before queued input was delivered; inspect the agent before retrying")
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = c.Remote.Call(ctx, "detach", core.Request{}, nil)
	cancel()
	c.cancel()
	err := c.leaseConn.Close()
	c.wg.Wait()
	return errors.Join(flushErr, err)
}

func (c *Client) EventStream() <-chan core.Event { return c.events }
func (c *Client) emit(event core.Event) {
	select {
	case c.events <- event:
	case <-c.ctx.Done():
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.failure != nil {
		c.mu.Unlock()
		return
	}
	c.failure = err
	c.mu.Unlock()
	c.emit(core.Event{Err: fmt.Errorf("daemon connection lost; detach and run maestro attach: %w", err)})
}

func (c *Client) pane(state paneState) *term.Pane {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.panes[state.Generation]
	if p == nil {
		p = &remotePane{client: c, id: state.TaskID, generation: state.Generation}
		p.view = term.NewRemotePane(p)
		c.panes[state.Generation] = p
	}
	p.mu.Lock()
	p.snapshot = state.Snapshot
	if state.InputError != "" {
		p.snapshot.InputError = errors.New(state.InputError)
	}
	p.mu.Unlock()
	return p.view
}

func (c *Client) poll() {
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-timer.C:
		}
		var state syncState
		if err := c.Call(c.ctx, "sync", core.Request{}, &state); err != nil {
			if c.ctx.Err() == nil {
				c.fail(err)
			}
			return
		}
		for _, p := range state.Panes {
			c.pane(p)
		}
		for id := range c.cleanup {
			if !slices.Contains(state.Cleanup, id) {
				delete(c.cleanup, id)
			}
		}
		changed := len(c.tasks) != len(state.Tasks)
		nextTasks := make(map[int64]store.Task, len(state.Tasks))
		for _, t := range state.Tasks {
			before, known := c.tasks[t.ID]
			nextTasks[t.ID] = t
			if !known || before != t {
				changed = true
			}
			cleanup := false
			for _, id := range state.Cleanup {
				if id == t.ID && !c.cleanup[id] {
					cleanup = true
					c.cleanup[id] = true
				}
			}
			if cleanup {
				copy := t
				c.emit(core.Event{TaskID: t.ID, Task: &copy, Cleanup: cleanup})
			}
		}
		c.tasks = nextTasks
		if changed {
			c.emit(core.Event{Tasks: state.Tasks})
		}
		c.mu.Lock()
		for generation := range c.panes {
			if slices.ContainsFunc(state.Panes, func(p paneState) bool { return p.TaskID == c.panes[generation].id && p.Generation > generation }) {
				delete(c.panes, generation)
			}
		}
		c.mu.Unlock()
		if state.Error != "" && state.Error != c.lastError {
			c.lastError = state.Error
			c.emit(core.Event{Err: errors.New(state.Error)})
		}
	}
}

func (c *Client) Start(task store.Task, cols, rows int, fresh bool) (*term.Pane, error) {
	c.mu.Lock()
	ensure := !c.started[task.ID]
	c.mu.Unlock()
	var state paneState
	err := c.Call(c.ctx, "start", core.Request{Task: task, Cols: cols, Rows: rows, Fresh: fresh, Ensure: ensure}, &state)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.started[task.ID] = true
	c.mu.Unlock()
	return c.pane(state), nil
}

func (c *Client) Switch(ctx context.Context, t store.Task, target string, cols, rows int, confirmed bool) (*term.Pane, error) {
	return c.switchAgent(ctx, t, target, cols, rows, confirmed, false)
}

func (c *Client) SwitchFresh(ctx context.Context, t store.Task, target string, cols, rows int, confirmed bool) (*term.Pane, error) {
	return c.switchAgent(ctx, t, target, cols, rows, confirmed, true)
}

func (c *Client) switchAgent(ctx context.Context, t store.Task, target string, cols, rows int, confirmed, fresh bool) (*term.Pane, error) {
	var state paneState
	err := c.Call(ctx, "switch", core.Request{Task: t, Text: target, Cols: cols, Rows: rows, Confirmed: confirmed, Fresh: fresh}, &state)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.started[t.ID] = true
	c.mu.Unlock()
	return c.pane(state), nil
}

func (c *Client) Create(ctx context.Context, in core.NewTask) (store.Task, error) {
	var t store.Task
	err := c.Call(ctx, "create", core.Request{New: in}, &t)
	return t, err
}

func (c *Client) Workflow(ctx context.Context, t store.Task, action string, opts core.WorkflowOptions) (store.Task, error) {
	var result store.Task
	err := c.Call(ctx, "workflow", core.Request{Task: t, Action: action, Options: opts}, &result)
	return result, err
}

func (c *Client) Archive(ctx context.Context, t store.Task) error {
	_, err := c.Workflow(ctx, t, "archive", core.WorkflowOptions{StopAgent: true})
	return err
}

func (c *Client) Checkpoint(ctx context.Context, t store.Task) (string, error) {
	var path string
	err := c.Call(ctx, "checkpoint", core.Request{Task: t}, &path)
	return path, err
}

func (c *Client) SetNotes(ctx context.Context, t store.Task, notes string) error {
	return c.Call(ctx, "notes", core.Request{Task: t, Text: notes}, nil)
}

func (c *Client) Rename(ctx context.Context, t store.Task, title string) error {
	return c.Call(ctx, "rename", core.Request{Task: t, Text: title}, nil)
}

func (c *Client) ReorderTasks(ctx context.Context, ids []int64) error {
	return c.Call(ctx, "reorder", core.Request{IDs: ids}, nil)
}

func (c *Client) Stop(id int64) {
	if err := c.Call(c.ctx, "stop", core.Request{TaskID: id}, nil); err != nil {
		c.emit(core.Event{Err: err})
	}
}

func (c *Client) enqueue(req request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return c.failure
	}
	size := len(req.Args.Text) + len(req.Key.Text) + 128
	if c.pendingBytes+size > 16<<20 {
		c.failure = fmt.Errorf("daemon input queue overflow; detach and reattach, then inspect input before retrying")
		return c.failure
	}
	select {
	case c.input <- req:
		c.pendingBytes += size
		return nil
	default:
		c.failure = fmt.Errorf("daemon input queue overflow; detach and reattach, then inspect input before retrying")
		return c.failure
	}
}

func (c *Client) writeInput() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case req := <-c.input:
			var lines []string
			var out any
			if req.Method == "scrollback" {
				out = &lines
			}
			err := c.call(c.ctx, req, out)
			c.mu.Lock()
			c.pendingBytes -= len(req.Args.Text) + len(req.Key.Text) + 128
			p := c.panes[req.Args.Generation]
			c.mu.Unlock()
			if p != nil && req.Method == "scrollback" {
				p.mu.Lock()
				p.scrollPending = false
				if err == nil {
					p.scroll = lines
				}
				p.mu.Unlock()
			}
			if err != nil && c.ctx.Err() == nil {
				if req.Method == "input" || req.Method == "resize" {
					c.fail(err)
					return
				}
				c.emit(core.Event{Err: err})
			}
		}
	}
}

type remotePane struct {
	client         *Client
	id, generation int64
	view           *term.Pane
	mu             sync.Mutex
	snapshot       term.Snapshot
	scroll         []string
	scrollAt       time.Time
	scrollPending  bool
}

func (p *remotePane) Snapshot(_ bool) term.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot
}

func (p *remotePane) request(method, action, text string) request {
	return request{Method: method, Args: core.Request{TaskID: p.id, Generation: p.generation, Action: action, Text: text}}
}

func (p *remotePane) send(req request) {
	if err := p.client.enqueue(req); err != nil {
		p.mu.Lock()
		p.snapshot.InputError = err
		p.mu.Unlock()
		select {
		case p.client.events <- core.Event{Err: err}:
		default:
		}
	}
}

func (p *remotePane) Resize(cols, rows int) error {
	req := p.request("resize", "", "")
	req.Args.Cols, req.Args.Rows = cols, rows
	return p.client.enqueue(req)
}

func (p *remotePane) Key(key uv.Key, release bool) {
	req := p.request("input", "key", "")
	req.Key, req.Release = key, release
	p.send(req)
}
func (p *remotePane) Paste(text string) { p.send(p.request("input", "paste", text)) }
func (p *remotePane) Mouse(mouse uv.Mouse, release, motion bool) {
	req := p.request("input", "mouse", "")
	req.Mouse, req.Release, req.Motion = mouse, release, motion
	p.send(req)
}
func (p *remotePane) Stop() { p.client.Stop(p.id) }
func (p *remotePane) Scrollback() []string {
	p.mu.Lock()
	want := !p.scrollPending && time.Since(p.scrollAt) > 250*time.Millisecond
	if want {
		p.scrollPending = true
		p.scrollAt = time.Now()
	}
	lines := append([]string(nil), p.scroll...)
	p.mu.Unlock()
	if want {
		if err := p.client.enqueue(p.request("scrollback", "", "")); err != nil {
			p.mu.Lock()
			p.scrollPending = false
			p.mu.Unlock()
		}
	}
	return lines
}

func (c *Client) Call(ctx context.Context, method string, args core.Request, out any) error {
	op, cancel := context.WithCancel(ctx)
	defer cancel()
	unlink := context.AfterFunc(c.ctx, cancel)
	defer unlink()
	return c.Remote.Call(op, method, args, out)
}

func (p *remotePane) FetchScrollback(ctx context.Context) ([]string, error) {
	var lines []string
	err := p.client.Call(ctx, "scrollback", core.Request{TaskID: p.id, Generation: p.generation}, &lines)
	return lines, err
}
