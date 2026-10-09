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
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/handoff"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type Event struct {
	Tasks     []store.Task
	Task      *store.Task
	Cleanup   bool
	TaskID    int64
	SessionID int64
	Pane      *term.Pane
	State     term.State
	Revision  uint64
	Err       error
}
type running struct {
	task     store.Task
	pane     *term.Pane
	session  store.Session
	finished chan struct{}
	ignoreID string
	seen     map[string]bool
	saveErr  error // Only read after finished closes.
}

// Generic agents have no readiness protocol. Require output and a short live
// startup window before accepting delivery; native turn signals or a clean exit
// can acknowledge it earlier. Until then the handoff survives crashes/restarts.
const handoffStartupWindow = 2 * time.Second

type Runtime struct {
	Service     *TaskService
	Events      chan Event
	gate        sync.RWMutex
	mu          sync.Mutex
	panes       map[int64]*running
	locks       map[int64]*sync.Mutex
	closed      bool
	wg          sync.WaitGroup
	operations  sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	errMu       sync.Mutex
	writeErrors []error
	pollEnabled bool
	pollRepo    forge.Repo
	pollCleanup string
}

func (s *TaskService) OpenRuntime() (*Runtime, error) {
	if err := s.acquire(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = git.WithCredentialPrompt(ctx, func(context.Context, string) (string, error) {
		return "", fmt.Errorf("git credentials required; retry the action in Maestro or unlock the SSH key with ssh-add")
	})
	pollRepo, pollErr := forge.ParseRemote(s.Repo.Remote, s.Config.GitLab.Host)
	r := &Runtime{Service: s, Events: make(chan Event, 128), panes: map[int64]*running{}, locks: map[int64]*sync.Mutex{}, ctx: ctx, cancel: cancel, pollEnabled: pollErr == nil && s.Forge != nil, pollRepo: pollRepo, pollCleanup: s.Config.Git.Cleanup}
	r.wg.Add(1)
	go r.poll()
	return r, nil
}

func (r *Runtime) operation(id int64) (func(), error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("runtime is closed")
	}
	lock := r.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[id] = lock
	}
	r.operations.Add(1)
	r.mu.Unlock()
	if id == 0 {
		r.gate.Lock()
	} else {
		r.gate.RLock()
	}
	lock.Lock()
	return func() {
		lock.Unlock()
		if id == 0 {
			r.gate.Unlock()
		} else {
			r.gate.RUnlock()
		}
		r.operations.Done()
	}, nil
}

func (r *Runtime) Create(ctx context.Context, in NewTask) (store.Task, error) {
	done, err := r.operation(0)
	if err != nil {
		return store.Task{}, err
	}
	defer done()
	op, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return r.Service.Create(op, in)
}
func (r *Runtime) entry(id int64) *running { r.mu.Lock(); defer r.mu.Unlock(); return r.panes[id] }
func (r *Runtime) Start(task store.Task, cols, rows int, fresh bool) (*term.Pane, error) {
	done, err := r.operation(task.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	// The database owns the selected agent, including a pending failed switch.
	task, err = r.Service.Find(r.ctx, task.Slug)
	if err != nil {
		return nil, err
	}
	return r.start(r.ctx, task, cols, rows, fresh, nil)
}

func (r *Runtime) adapter(name string) (agent.Adapter, error) {
	return (agent.Registry{Agents: r.Service.Config.Agents}).Get(name)
}

func (r *Runtime) resume(ctx context.Context, task store.Task, a agent.Adapter, fresh bool) (store.Session, string, error) {
	previous, err := r.Service.Store.LatestAgentSession(ctx, task.ID, task.Agent)
	if err != nil {
		return previous, "", err
	}
	if fresh || len(r.Service.Config.Agents[task.Agent].Resume) == 0 {
		return previous, "", nil
	}
	id := previous.NativeID
	if previous.ID != 0 && id == "" && len(r.Service.Config.Agents[task.Agent].Resume) > 0 {
		id, err = a.DiscoverSession(ctx, task.Worktree, previous.StartedAt)
		if err != nil {
			return previous, "", fmt.Errorf("discover saved session: %w", err)
		}
		if id == "" {
			return previous, "", fmt.Errorf("%s: %w; use prefix R for the current agent or select the agent in the switch dialog", task.Agent, ErrFreshStartRequired)
		}
		if err = r.Service.Store.SetNativeID(ctx, previous.ID, id); err != nil {
			return previous, "", err
		}
	}
	return previous, id, nil
}

// handoffFrom requests a context transfer even if stopping the outgoing agent
// acknowledged its pending handoff. A nil value still refreshes pending retries
// and supplies saved context when explicitly replacing an existing session.
func (r *Runtime) start(operation context.Context, task store.Task, cols, rows int, fresh bool, handoffFrom *int64) (*term.Pane, error) {
	if task.Lifecycle == "archived" || task.CleanupPending {
		return nil, fmt.Errorf("task %q is archived; use maestro reopen %s first", task.Slug, task.Slug)
	}
	if err := operation.Err(); err != nil {
		return nil, err
	}
	if err := r.Service.ensurePortableWorktree(operation, task); err != nil {
		return nil, err
	}
	portableState, err := r.Service.Store.Portable(operation, task.ID)
	if err != nil {
		return nil, err
	}
	fresh = fresh || portableState.Fresh
	if old := r.entry(task.ID); old != nil {
		old.pane.Stop()
		<-old.finished
		if old.saveErr != nil {
			if err := r.retryHistory(old); err != nil {
				return nil, err
			}
		}
	}
	ctx, cancel := context.WithTimeout(operation, 10*time.Second)
	defer cancel()
	a, err := r.adapter(task.Agent)
	if err != nil {
		return nil, err
	}
	if _, err = a.Detect(); err != nil {
		return nil, err
	}
	cfg := r.Service.Config.Agents[task.Agent]
	previous, nativeID, err := r.resume(ctx, task, a, fresh)
	if err != nil {
		return nil, err
	}
	ignoreID := ""
	if fresh {
		ignoreID = previous.NativeID
		if ignoreID == "" {
			ignoreID, _ = a.DiscoverSession(ctx, task.Worktree, previous.StartedAt)
		}
	}
	isNew := nativeID == ""
	prompt := ""
	if isNew {
		prompt = task.Prompt
	}
	pending, err := r.Service.Store.PendingHandoff(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	if pending.ID != 0 || handoffFrom != nil || (fresh && previous.ID != 0) {
		from := previous.ID
		if handoffFrom != nil {
			from = *handoffFrom
		}
		if pending.ID != 0 && pending.Agent == task.Agent {
			from = pending.FromSession
		}
		content, e := r.prepareHandoff(ctx, task, from)
		if e != nil {
			return nil, e
		}
		if err = handoff.Write(task.Worktree, content); err != nil {
			return nil, err
		}
		prompt = handoff.Prompt(task.Worktree)
	}
	if cfg.ManualPrompt {
		prompt = ""
	}
	// Persist the handoff before allocating a native session, so a failed
	// session_create command leaves the selected target and context recoverable.
	if isNew && cfg.GenerateSessionID {
		nativeID = uuid.NewString()
	}
	if isNew && len(cfg.SessionCreate) > 0 {
		nativeID, err = a.CreateSession(ctx, task.Worktree)
		if err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	if !isNew {
		if snapshot, e := a.Read(ctx, nativeID, task.Worktree); e == nil {
			for _, event := range snapshot.Events {
				seen[event.Key] = true
			}
		}
	}
	cmd, err := a.Command(context.Background(), agent.LaunchSpec{Dir: task.Worktree, SessionID: nativeID, NewSession: isNew, Prompt: prompt, Env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "MAESTRO_TASK=" + task.Slug, "MAESTRO_SESSION_ID=" + nativeID}})
	if err != nil {
		return nil, err
	}
	idle, _ := time.ParseDuration(r.Service.Config.Activity.IdleAfter)
	started := time.Now()
	pane, err := term.Start(cmd, cols, rows, idle, a.Hints())
	if err != nil {
		return nil, err
	}
	session, err := r.Service.Store.StartSession(ctx, task, nativeID, started)
	if err != nil {
		pane.Stop()
		return nil, err
	}
	entry := &running{task: task, pane: pane, session: session, finished: make(chan struct{}), ignoreID: ignoreID, seen: seen}
	r.mu.Lock()
	r.panes[task.ID] = entry
	r.mu.Unlock()
	r.wg.Add(1)
	go r.watch(entry, a)
	return pane, nil
}

var ErrFreshStartRequired = errors.New("no native session ID found; confirm a fresh session")

var ErrInterruptRequired = errors.New("agent is active; confirm interruption to switch")

func (r *Runtime) Switch(ctx context.Context, task store.Task, target string, cols, rows int, confirmed bool) (*term.Pane, error) {
	return r.switchAgent(ctx, task, target, cols, rows, confirmed, false)
}

// SwitchFresh explicitly starts the selected target with the saved handoff.
// Interruption of an active outgoing agent still requires confirmation.
func (r *Runtime) SwitchFresh(ctx context.Context, task store.Task, target string, cols, rows int, confirmed bool) (*term.Pane, error) {
	return r.switchAgent(ctx, task, target, cols, rows, confirmed, true)
}

func (r *Runtime) switchAgent(ctx context.Context, task store.Task, target string, cols, rows int, confirmed, fresh bool) (*term.Pane, error) {
	done, err := r.operation(task.ID)
	if err != nil {
		return nil, err
	}
	defer done()
	op, cancel := context.WithCancel(r.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	task, err = r.Service.Find(op, task.Slug)
	if err != nil {
		return nil, err
	}
	if task.Lifecycle == "archived" {
		return nil, fmt.Errorf("task %q is archived; reopen it first", task.Slug)
	}
	pending, err := r.Service.Store.PendingHandoff(op, task.ID)
	if err != nil {
		return nil, err
	}
	if target == task.Agent && pending.ID == 0 && !fresh {
		if old := r.entry(task.ID); old != nil {
			return old.pane, nil
		}
		return r.start(op, task, cols, rows, false, nil)
	}
	candidate := task
	candidate.Agent = target
	a, err := r.adapter(target)
	if err != nil {
		return nil, err
	}
	if _, err = a.Detect(); err != nil {
		return nil, err
	}
	preflight, cancelPreflight := context.WithTimeout(op, 5*time.Second)
	_, targetID, resumeErr := r.resume(preflight, candidate, a, fresh)
	err = resumeErr
	cancelPreflight()
	if err != nil {
		return nil, err
	}
	// Validate argument templates before stopping the outgoing agent.
	prompt := handoff.Prompt(task.Worktree)
	if r.Service.Config.Agents[target].ManualPrompt {
		prompt = ""
	}
	if _, err = a.Command(op, agent.LaunchSpec{Dir: task.Worktree, SessionID: targetID, NewSession: targetID == "", Prompt: prompt}); err != nil {
		return nil, err
	}
	if old := r.entry(task.ID); old != nil {
		state := old.pane.Snapshot(false).State
		if !confirmed && (state == term.Working || state == term.Starting || state == term.NeedsInput) {
			return nil, ErrInterruptRequired
		}
		old.pane.Stop()
		<-old.finished
		if old.saveErr != nil {
			if err := r.retryHistory(old); err != nil {
				return nil, err
			}
		}
	}
	if err = op.Err(); err != nil {
		return nil, err
	}
	// Also refresh history when switch is the first operation after reopening.
	previous, err := r.Service.Store.LatestAgentSession(op, task.ID, task.Agent)
	if err != nil {
		return nil, err
	}
	if previous.ID != 0 && previous.NativeID != "" {
		oldAdapter, e := r.adapter(task.Agent)
		if e == nil {
			readCtx, stopRead := context.WithTimeout(op, 5*time.Second)
			snapshot, readErr := oldAdapter.Read(readCtx, previous.NativeID, task.Worktree)
			stopRead()
			if len(snapshot.Records) > 0 {
				if e = r.importRecords(op, previous.ID, snapshot.Records); e != nil {
					return nil, e
				}
			}
			if e = r.Service.Store.SetNativeComplete(op, previous.ID, readErr == nil && !snapshot.Incomplete && len(snapshot.Records) > 0); e != nil {
				return nil, e
			}
			if readErr != nil && !errors.Is(readErr, agent.ErrUnsupported) {
				r.emit(Event{TaskID: task.ID, Err: fmt.Errorf("native history unavailable; using saved history: %w", readErr)})
			}
		}
	}
	return r.start(op, candidate, cols, rows, fresh, &previous.ID)
}

func (r *Runtime) prepareHandoff(ctx context.Context, task store.Task, from int64) (string, error) {
	history, err := r.Service.Store.History(ctx, task)
	if err != nil {
		return "", err
	}
	gitCtx, stopGit := context.WithTimeout(ctx, 5*time.Second)
	defer stopGit()
	p, err := r.Service.Store.Portable(ctx, task.ID)
	if err != nil {
		return "", err
	}
	content, err := r.Service.continuation(gitCtx, history, p)
	if err != nil {
		return "", err
	}
	if err = r.Service.Store.PrepareHandoff(ctx, task.ID, from, task.Agent, content); err != nil {
		return "", err
	}
	return content, nil
}

func (r *Runtime) emit(e Event) {
	// Lifecycle updates describe persisted mutations, including deletion of the
	// worktree. Wait for the UI to consume them, with bounded runtime shutdown.
	if e.Task != nil {
		select {
		case r.Events <- e:
		case <-r.ctx.Done():
		}
		return
	}
	select {
	case r.Events <- e:
	default:
	}
}

func (r *Runtime) recordWriteError(entry *running, err error) {
	if entry.saveErr != nil && entry.saveErr.Error() == err.Error() {
		return
	}
	entry.saveErr = err
	r.errMu.Lock()
	r.writeErrors = append(r.writeErrors, err)
	r.errMu.Unlock()
	r.emit(Event{TaskID: entry.task.ID, SessionID: entry.session.ID, Pane: entry.pane, Err: err})
}

func (r *Runtime) importRecords(ctx context.Context, id int64, records []agent.Record) error {
	turns := make([]store.Turn, 0, len(records))
	for _, t := range records {
		turns = append(turns, store.Turn{SourceKey: t.Key, Role: t.Role, Content: t.Content, ToolCallID: t.ToolCallID, TS: t.TS})
	}
	return r.Service.Store.ImportTurns(ctx, id, turns)
}

func (r *Runtime) watch(entry *running, a agent.Adapter) {
	defer r.wg.Done()
	defer close(entry.finished)
	// Watchers live through process drain, even when Close cancels operations.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	var updates <-chan agent.Update
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := term.Starting
	lastDiscovery := time.Time{}
	lastWarning := ""
	nativeTurns := a.ID() == "codex" || a.ID() == "agy" || a.ID() == "opencode"
	var inputWarning error
	handoffConfirmed := false
	confirmHandoff := func() {
		if handoffConfirmed {
			return
		}
		c, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		if err := r.Service.Store.ConfirmHandoff(c, entry.session.ID); err != nil {
			r.recordWriteError(entry, err)
		} else {
			handoffConfirmed = true
		}
	}
	imported := map[string]agent.Record{}
	// Baseline events belong to a previous launch, even if their timestamps are absent.
	baseline := map[string]bool{}
	for key := range entry.seen {
		baseline[key] = true
	}
	recovering := false
	emit := func(state term.Snapshot, err error) {
		r.emit(Event{TaskID: entry.task.ID, SessionID: entry.session.ID, Pane: entry.pane, State: state.State, Revision: state.Revision, Err: err})
	}
	warn := func(err error) {
		if err == nil {
			lastWarning = ""
			return
		}
		if err.Error() != lastWarning {
			lastWarning = err.Error()
			emit(term.Snapshot{}, err)
		}
	}
	discover := func() {
		if entry.session.NativeID != "" {
			return
		}
		c, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		id, err := a.DiscoverSession(c, entry.task.Worktree, entry.session.StartedAt)
		if err != nil {
			warn(fmt.Errorf("session discovery: %w", err))
			return
		}
		if id != "" && id != entry.ignoreID {
			if err = r.Service.Store.SetNativeID(c, entry.session.ID, id); err != nil {
				r.recordWriteError(entry, err)
			} else {
				entry.session.NativeID = id
			}
		}
	}
	save := func() {
		c, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		if err := r.Service.Store.SaveScrollback(c, entry.session.ID, strings.Join(entry.pane.Scrollback(), "\n")); err != nil {
			r.recordWriteError(entry, err)
		}
	}
	apply := func(u agent.Update) {
		recovering = recovering || u.Reset
		importedOK := true
		c, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		changed := []agent.Record{}
		latest := map[string]agent.Record{}
		for _, record := range u.Transcript.Records {
			latest[record.Key] = record
		}
		for _, record := range u.Transcript.Records {
			if record != latest[record.Key] {
				continue
			}
			if previous, ok := imported[record.Key]; !ok || previous != record {
				changed = append(changed, record)
			}
		}
		if len(changed) > 0 {
			if err := r.importRecords(c, entry.session.ID, changed); err != nil {
				importedOK = false
				r.recordWriteError(entry, err)
			} else {
				for _, record := range changed {
					imported[record.Key] = record
				}
			}
		}
		if err := r.Service.Store.SetNativeComplete(c, entry.session.ID, importedOK && u.Err == nil && !u.Transcript.Incomplete && len(u.Transcript.Records) > 0); err != nil {
			r.recordWriteError(entry, err)
		}
		if u.Err != nil {
			recovering = true
			entry.pane.NativeEvent("unavailable")
			if !errors.Is(u.Err, agent.ErrUnsupported) || a.ID() == "codex" || a.ID() == "agy" || a.ID() == "opencode" {
				warn(fmt.Errorf("native transcript unavailable; using terminal fallback: %w", u.Err))
			}
			return
		}
		warn(nil)
		if recovering {
			// Rebuild activity independently of deduplication. Never replay an old
			// completion over fresh terminal input or clear an approval prompt.
			activity := ""
			for _, e := range u.Transcript.Events {
				if baseline[e.Key] || (!e.TS.IsZero() && e.TS.Before(entry.session.StartedAt)) {
					continue
				}
				activity = e.Kind
			}
			switch activity {
			case "started":
				entry.pane.NativeEvent("restored_started")
			case "done", "interrupted":
				entry.pane.NativeEvent("restored_idle")
			default:
				entry.pane.NativeEvent("unavailable")
			}
			recovering = false
		}
		for _, e := range u.Transcript.Events {
			if entry.seen[e.Key] {
				continue
			}
			entry.seen[e.Key] = true
			if !e.TS.IsZero() && e.TS.Before(entry.session.StartedAt) {
				continue
			}
			state := entry.pane.NativeEvent(e.Kind)
			// Capture state and revision together, before history persistence or
			// another turn can advance the pane. Snapshots use the same revision.
			last = state.State
			emit(state, nil)
			if e.Kind == "started" || e.Kind == "done" {
				confirmHandoff()
			}
			if e.Kind == "done" || e.Kind == "interrupted" {
				if err := r.Service.Store.AddEvent(c, entry.task.ID, "turn_"+e.Kind, map[string]any{"session_id": entry.session.ID, "source_key": e.Key}); err != nil {
					r.recordWriteError(entry, err)
				}
				save()
			}
		}
	}
	for {
		if updates == nil && entry.session.NativeID != "" {
			updates = agent.Watch(watchCtx, a, entry.session.NativeID, entry.task.Worktree)
		}
		select {
		case <-entry.pane.Done():
			stopWatch()
			if updates != nil {
				for update := range updates {
					// Keep records already read even if the source vanishes during shutdown.
					if errors.Is(update.Err, context.Canceled) {
						update.Err = nil
					}
					apply(update)
				}
			}
			discover()
			if entry.session.NativeID != "" {
				finalCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				snapshot, err := a.Read(finalCtx, entry.session.NativeID, entry.task.Worktree)
				stop()
				apply(agent.Update{Transcript: snapshot, Err: err})
			}
			save()
			finalCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			if err := r.Service.Store.EndSession(finalCtx, entry.session.ID, entry.pane.Snapshot(false).ExitCode); err != nil {
				r.recordWriteError(entry, err)
			}
			stop()
			if entry.pane.Snapshot(false).ExitCode == 0 {
				confirmHandoff()
			}
			emit(entry.pane.Snapshot(false), nil)
			return
		case u, ok := <-updates:
			if ok {
				apply(u)
			} else {
				updates = nil
			}
		case now := <-ticker.C:
			paneState := entry.pane.Snapshot(false)
			if paneState.InputError != nil && inputWarning == nil {
				inputWarning = paneState.InputError
				emit(term.Snapshot{}, inputWarning)
			}
			state := paneState.State
			if !nativeTurns && paneState.HasOutput && now.Sub(entry.session.StartedAt) >= handoffStartupWindow && (state == term.Working || state == term.Done || state == term.NeedsInput) {
				confirmHandoff()
			}
			if state != last {
				last = state
				emit(paneState, nil)
				if state == term.Done {
					save()
					// Native completion updates last in apply, so a Done transition here
					// is a terminal/quiet-timer completion even after native monitoring fails.
					c, stop := context.WithTimeout(ctx, 5*time.Second)
					if err := r.Service.Store.AddEvent(c, entry.task.ID, "turn_done", map[string]any{"session_id": entry.session.ID, "source": "terminal"}); err != nil {
						r.recordWriteError(entry, err)
					}
					stop()
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
	done, err := r.operation(taskID)
	if err != nil {
		return
	}
	defer done()
	if entry := r.entry(taskID); entry != nil {
		entry.pane.Stop()
		<-entry.finished
	}
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.operations.Wait()
	r.mu.Lock()
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

// retryHistory permits recovery after a transient persistence failure without
// discarding the stopped pane's final output or selecting a fresh native session.
func (r *Runtime) retryHistory(entry *running) error {
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	if entry.session.NativeID != "" {
		if adapter, err := r.adapter(entry.task.Agent); err == nil {
			transcript, readErr := adapter.Read(ctx, entry.session.NativeID, entry.task.Worktree)
			if len(transcript.Records) > 0 {
				if err = r.importRecords(ctx, entry.session.ID, transcript.Records); err != nil {
					return err
				}
			}
			if err := r.Service.Store.SetNativeComplete(ctx, entry.session.ID, readErr == nil && !transcript.Incomplete && len(transcript.Records) > 0); err != nil {
				return err
			}
		}
	}
	if err := r.Service.Store.SaveScrollback(ctx, entry.session.ID, strings.Join(entry.pane.Scrollback(), "\n")); err != nil {
		return err
	}
	return r.Service.Store.EndSession(ctx, entry.session.ID, entry.pane.Snapshot(false).ExitCode)
}
