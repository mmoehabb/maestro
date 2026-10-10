package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/notify"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/handoff"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type tab struct {
	task                store.Task
	pane                *term.Pane
	err                 error
	pending             bool
	pendingHandoff      string
	pendingHandoffFresh bool
	status              git.Status
	state               term.State
	stateRevision       uint64
	statePane           *term.Pane
}
type (
	tickMsg           time.Time
	prefixTimeoutMsg  struct{}
	editorFinishedMsg struct{ err error }
	loadedMsg         struct {
		tasks []store.Task
		err   error
	}
)

type startedMsg struct {
	id   int64
	pane *term.Pane
	err  error
}
type createdMsg struct {
	task store.Task
	err  error
}
type statusMsg struct {
	values map[int64]git.Status
	err    error
}
type (
	stoppedMsg  struct{ id int64 }
	archivedMsg struct {
		id  int64
		err error
	}
	shellMsg struct{ err error }
)

type Model struct {
	credentials                         chan credentialRequest
	credentialUI                        *credentialDialog
	themes                              *themePicker
	palette                             *paletteView
	diff                                *diffView
	sidebar, light, blurred, fullscreen bool
	backgroundKnown                     bool
	helpOffset                          int
	dragID                              int64
	dragMoved                           bool
	orderPending                        bool
	notifier                            notify.Notifier
	notificationFailed                  bool
	observed                            map[int64]activityObservation
	attention                           map[int64]time.Time

	forgeUI                                                   *forgeDialog
	cleanupQueue                                              []store.Task
	initialAgent, manualInstruction                           string
	switcher                                                  *switchDialog
	history                                                   *historyView
	notes                                                     *notesDialog
	service                                                   *core.TaskService
	runtime                                                   Runtime
	cfg                                                       config.Config
	tabs                                                      []tab
	active, width, height, frame                              int
	focus, prefix, toast                                      string
	enhanced, prefixed, prefixSettled, loaded, help, quitting bool
	dialog                                                    *newTaskDialog
	scroll                                                    int
	toastUntil                                                time.Time
	nextStatus                                                time.Time
	statusPending                                             bool
	swallowed                                                 map[rune]bool
}

func New(s *core.TaskService, r Runtime, focus string) *Model {
	return &Model{
		credentials: make(chan credentialRequest, 16),
		service:     s, runtime: r, cfg: s.Config, focus: focus, width: 80, height: 24,
		observed: make(map[int64]activityObservation), attention: make(map[int64]time.Time),
		prefix: term.ActivePrefix(s.Config.Prefix, s.Config.PrefixFallback, false),
	}
}

func tick() tea.Cmd             { return tea.Tick(time.Second/30, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m *Model) event() tea.Cmd { return func() tea.Msg { return <-m.runtime.EventStream() } }
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, tick(), m.event(), tea.Tick(600*time.Millisecond, func(time.Time) tea.Msg { return prefixTimeoutMsg{} }), func() tea.Msg {
		tasks, err := m.service.List(context.Background(), false)
		return loadedMsg{tasks, err}
	})
}

func (m *Model) notify(s string)  { m.toast = s; m.toastUntil = time.Now().Add(6 * time.Second) }
func (m *Model) size() (int, int) { b := m.paneBounds(); return b.w, b.h }
func (m *Model) launch(index int, fresh bool) tea.Cmd {
	t := &m.tabs[index]
	if t.pending {
		return nil
	}
	t.pending = true
	t.pendingHandoff = ""
	t.pendingHandoffFresh = false
	t.err = nil
	task := t.task
	cols, rows := m.size()
	return func() tea.Msg { p, err := m.runtime.Start(task, cols, rows, fresh); return startedMsg{task.ID, p, err} }
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.contextMessage(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case githubLoginMsg:
		if msg.err != nil {
			m.notify("Git host login failed: " + msg.err.Error())
		} else {
			if m.service.Remote != nil {
				_ = m.service.Remote.Call(context.Background(), "auth-reset", core.Request{}, nil)
			}
			if provider, ok := m.service.Forge.(interface{ ResetAuth() }); ok {
				provider.ResetAuth()
			}
			m.notify("Git host login completed. Retry the command.")
		}
	case inputMsg:
		return m, m.inputReply(msg)
	case themeSavedMsg:
		if m.themes == msg.picker {
			m.themes.busy = false
			if msg.err != nil {
				m.themes.err = msg.err.Error()
			} else {
				m.themes = nil
				m.notify("Theme saved: " + m.cfg.Theme)
			}
		}
	case tea.BackgroundColorMsg:
		m.light = !msg.IsDark()
		m.backgroundKnown = true
	case tea.FocusMsg:
		m.blurred = false
	case tea.BlurMsg:
		m.blurred = true
	case notificationMsg:
		if msg.err != nil && !m.notificationFailed {
			m.notificationFailed = true
			m.notify("Desktop notifications unavailable; alerts remain in Maestro.")
		}
	case diffMsg:
		if m.diff == msg.view {
			m.diff.loading = false
			m.diff.result = msg.result
			m.diff.lines = nil
			m.diff.err = msg.err
		}
	case orderedMsg:
		m.orderPending = false
		if msg.err != nil {
			m.notify("Could not reorder tasks: " + msg.err.Error())
		} else {
			m.applyOrder(msg.ids)
		}
	case workflowMsg:
		return m, m.workflowResult(msg)
	case prDraftMsg:
		if m.forgeUI == nil || m.forgeUI != msg.dialog {
			return m, nil
		}
		if msg.err != nil {
			m.forgeUI.busy = false
			m.forgeUI.err = msg.err.Error()
			return m, nil
		}
		m.applyTask(msg.task)
		m.forgeUI = newPRDialog(msg.task, msg.draft, m.width, m.height)
	case reopenListMsg:
		if m.forgeUI == nil || m.forgeUI != msg.dialog {
			return m, nil
		}
		m.forgeUI.busy = false
		if msg.err != nil {
			m.forgeUI.err = msg.err.Error()
			return m, nil
		}
		for _, task := range msg.tasks {
			if task.Lifecycle == "archived" {
				m.forgeUI.archived = append(m.forgeUI.archived, task)
			}
		}
	case browserMsg:
		if msg.err != nil {
			m.notify(msg.err.Error())
		}
	case notesSavedMsg:
		m.notesSaved(msg)
	case checkpointMsg:
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.id {
				m.tabs[i].pending = false
			}
		}
		if msg.err != nil {
			m.notify(msg.err.Error())
		} else {
			m.notify("Checkpoint saved. Review, commit and push .maestro/tasks; restart the agent with prefix r.")
		}
	case tea.WindowSizeMsg:
		m.dragID = 0
		m.dragMoved = false
		m.width, m.height = msg.Width, msg.Height
		m.styleInputs()
		m.resizePanes()
	case tea.KeyboardEnhancementsMsg:
		m.enhanced = msg.SupportsKeyDisambiguation()
		m.prefix = term.ActivePrefix(m.cfg.Prefix, m.cfg.PrefixFallback, m.enhanced)
		m.prefixSettled = true
	case prefixTimeoutMsg:
		m.backgroundKnown = true
		if !m.prefixSettled && m.prefix != m.cfg.Prefix {
			m.notify("Keyboard disambiguation unavailable; prefix is " + m.prefix + ". Enter goes to the agent.")
		}
		m.prefixSettled = true
	case editorFinishedMsg:
		if msg.err != nil {
			m.notify("Editor exited with error: " + msg.err.Error())
		}
		return m, nil
	case loadedMsg:
		m.loaded = true
		if msg.err != nil {
			m.notify(msg.err.Error())
			return m, nil
		}
		var cmds []tea.Cmd
		for _, task := range msg.tasks {
			m.tabs = append(m.tabs, tab{task: task, state: term.Starting})
			m.observed[task.ID] = activityObservation{state: term.Starting}
			if task.Slug == m.focus {
				m.active = len(m.tabs) - 1
			}
			if task.Slug == m.focus && m.initialAgent != "" {
				cmds = append(cmds, m.switchTask(task, m.initialAgent, false))
			} else {
				cmds = append(cmds, m.launch(len(m.tabs)-1, false))
			}
		}
		if len(m.tabs) == 0 {
			m.dialog = newDialog(m.cfg, m.service.Repo.DefaultBranch)
			cmds = append(cmds, inputCommand(&m.dialog.fields[0], m.dialog.fields[0].Focus()))
		}
		return m, tea.Batch(cmds...)
	case startedMsg:
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.id {
				t := &m.tabs[i]
				t.pending = false
				t.attachPane(msg.pane)
				if msg.pane != nil && msg.pane.IsRemote() {
					snapshot := msg.pane.Snapshot(false)
					t.acceptActivity(msg.pane, snapshot.State, snapshot.Revision)
					m.observed[t.task.ID] = activityObservation{pane: msg.pane, state: snapshot.State, revision: snapshot.Revision}
				}
				t.err = msg.err
				if msg.err == nil {
					// Start may have completed a persisted pending switch from an earlier run.
					if m.cfg.Agents[t.task.Agent].ManualPrompt {
						m.manualInstruction = handoff.Prompt(t.task.Worktree)
					}
					if t.task.Lifecycle == "new" {
						t.task.Lifecycle = "active"
					}
					c, r := m.size()
					_ = t.pane.Resize(c, r)
				} else {
					t.state = term.Crashed
					m.notify(msg.err.Error())
				}
				break
			}
		}
	case createdMsg:
		if msg.err != nil {
			if m.dialog != nil {
				m.dialog.busy = false
				m.dialog.err = msg.err.Error()
			}
			return m, nil
		}
		m.dialog = nil
		for i, t := range m.tabs {
			if t.task.ID == msg.task.ID {
				m.active = i
				return m, nil
			}
		}
		m.tabs = append(m.tabs, tab{task: msg.task, state: term.Starting})
		m.observed[msg.task.ID] = activityObservation{state: term.Starting}
		m.active = len(m.tabs) - 1
		return m, m.launch(m.active, false)
	case archivedMsg:
		for i := range m.tabs {
			if m.tabs[i].task.ID != msg.id {
				continue
			}
			m.tabs[i].pending = false
			if msg.err != nil {
				m.notify(msg.err.Error())
				return m, nil
			}
			slug := m.tabs[i].task.Slug
			delete(m.observed, msg.id)
			delete(m.attention, msg.id)
			m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
			if i < m.active {
				m.active--
			}
			m.active = max(0, min(m.active, len(m.tabs)-1))
			m.scroll = 0
			m.notify("Archived " + slug + "; restore with maestro reopen " + slug)
			break
		}
	case stoppedMsg:
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.id {
				m.tabs[i].pending = false
			}
		}
	case shellMsg:
		if msg.err != nil {
			m.notify("Shell: " + msg.err.Error())
		}
	case core.Event:
		var reconcile tea.Cmd
		if msg.Tasks != nil && m.loaded {
			reconcile = m.reconcileTasks(msg.Tasks)
		}
		if msg.Task != nil {
			m.applyTask(*msg.Task)
			if msg.Cleanup {
				m.queueCleanup(*msg.Task)
			}
			m.notify(msg.Task.Slug + ": " + msg.Task.Lifecycle)
		}
		if msg.Err != nil {
			m.notify(msg.Err.Error())
		}
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.TaskID && m.tabs[i].acceptActivity(msg.Pane, msg.State, msg.Revision) {
				if msg.State == term.Done {
					m.nextStatus = time.Time{}
					if target := m.tabs[i].pendingHandoff; target != "" {
						fresh := m.tabs[i].pendingHandoffFresh
						m.tabs[i].pendingHandoff = ""
						m.tabs[i].pendingHandoffFresh = false
						switchCmd := m.switchTaskPending(m.tabs[i].task, target, true, fresh)
						if reconcile == nil {
							reconcile = switchCmd
						} else {
							reconcile = tea.Batch(reconcile, switchCmd)
						}
					}
				}
			}
		}
		return m, tea.Batch(m.event(), m.observeActivity(), reconcile)
	case statusMsg:
		m.statusPending = false
		for i := range m.tabs {
			if s, ok := msg.values[m.tabs[i].task.ID]; ok {
				m.tabs[i].status = s
			}
		}
		if msg.err != nil {
			m.notify("Git status: " + msg.err.Error())
		}
	case tickMsg:
		if len(m.cleanupQueue) > 0 && m.forgeUI == nil && m.notes == nil && m.dialog == nil && m.switcher == nil && m.history == nil && m.palette == nil && m.diff == nil && m.themes == nil && !m.help {
			task := m.cleanupQueue[0]
			consume := true
			for _, t := range m.tabs {
				if t.task.ID == task.ID {
					if t.pending {
						consume = false
						break
					}
					m.forgeUI = &forgeDialog{task: t.task, action: "cleanup"}
					break
				}
			}
			if consume {
				m.cleanupQueue = m.cleanupQueue[1:]
			}
		}
		m.frame++
		for i := range m.tabs {
			if m.tabs[i].pane != nil {
				s := m.tabs[i].pane.Snapshot(false)
				m.tabs[i].acceptActivity(m.tabs[i].pane, s.State, s.Revision)
			}
		}
		var refresh tea.Cmd
		if m.loaded && !m.statusPending && time.Time(msg).After(m.nextStatus) {
			m.statusPending = true
			m.nextStatus = time.Time(msg).Add(5 * time.Second)
			tasks := make([]store.Task, len(m.tabs))
			for i := range m.tabs {
				tasks[i] = m.tabs[i].task
			}
			refresh = func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				values := map[int64]git.Status{}
				var failure error
				for _, t := range tasks {
					s, err := m.service.Status(ctx, t)
					if err != nil {
						failure = err
					} else {
						values[t.ID] = s
					}
				}
				return statusMsg{values, failure}
			}
		}
		return m, tea.Batch(tick(), refresh, m.observeActivity(), m.pollCredentials())
	case tea.KeyPressMsg:
		msg = tea.KeyPressMsg(term.NormalizeKey(uv.Key(msg)))
		if m.swallowed == nil {
			m.swallowed = make(map[rune]bool)
		}
		m.swallowed[msg.Code] = true
		if m.credentialUI != nil {
			return m, m.credentialKey(msg)
		}
		if m.themes != nil {
			return m, m.themeKey(msg)
		}
		if m.palette != nil {
			return m, m.paletteKey(msg)
		}
		if m.diff != nil {
			return m, m.diffKey(msg)
		}
		if m.forgeUI != nil {
			return m, m.forgeKey(msg)
		}
		if m.notes != nil {
			return m, m.notesKey(msg)
		}
		if m.switcher != nil {
			return m, m.switchKey(msg)
		}
		if m.history != nil {
			return m, m.historyKey(msg)
		}
		if m.dialog != nil {
			return m, m.dialogKey(msg)
		}
		if m.help {
			m.helpKey(msg)
			return m, nil
		}
		if m.scroll > 0 {
			switch msg.String() {
			case "esc", "q":
				m.scroll = 0
			case "k", "up":
				m.scroll++
			case "j", "down":
				m.scroll = max(0, m.scroll-1)
			case "pgup":
				_, rows := m.size()
				m.scroll += rows
			case "pgdown":
				_, rows := m.size()
				m.scroll = max(0, m.scroll-rows)
			case "y":
				if len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
					pane := m.tabs[m.active].pane
					m.scroll = 0
					return m, func() tea.Msg {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						lines, err := pane.FetchScrollback(ctx)
						if err != nil {
							return browserMsg{err}
						}
						return tea.SetClipboard(strings.Join(lines, "\n"))()
					}
				}
			}
			return m, nil
		}
		key := msg.String()
		if m.prefixed {
			m.prefixed = false
			if key == m.prefix {
				m.forwardKey(msg, false)
				return m, nil
			}
			return m, m.dispatch(key)
		}
		if key == m.prefix {
			m.prefixed = true
			return m, nil
		}
		if key == "alt+h" {
			m.selectTab(m.active - 1)
			return m, nil
		}
		if key == "alt+l" {
			m.selectTab(m.active + 1)
			return m, nil
		}
		if len(key) == 5 && strings.HasPrefix(key, "alt+") && key[4] >= '1' && key[4] <= '9' {
			index := int(key[4] - '1')
			if index < len(m.tabs) {
				m.selectTab(index)
			}
			return m, nil
		}
		m.forwardKey(msg, false)
	case tea.KeyReleaseMsg:
		if m.swallowed[msg.Code] {
			delete(m.swallowed, msg.Code)
			return m, nil
		}
		if m.credentialUI == nil && !m.prefixed && m.themes == nil && m.palette == nil && m.diff == nil && m.forgeUI == nil && m.notes == nil && m.dialog == nil && m.switcher == nil && m.history == nil && !m.help && m.scroll == 0 && len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
			m.tabs[m.active].pane.Key(uv.Key(msg), true)
		}
	case tea.PasteMsg:
		if m.credentialUI != nil {
			var cmd tea.Cmd
			m.credentialUI.input, cmd = m.credentialUI.input.Update(msg)
			return m, cmd
		}
		if m.themes != nil {
			return m, nil
		}
		if m.palette != nil {
			return m, m.paletteInput(msg)
		}
		if m.diff != nil {
			return m, nil
		}
		if m.forgeUI != nil {
			return m, m.forgeInput(msg)
		}
		if m.notes != nil {
			if m.notes.busy {
				return m, nil
			}
			var cmd tea.Cmd
			m.notes.input, cmd = m.notes.input.Update(msg)
			return m, cmd
		}
		if m.switcher != nil || m.history != nil {
			return m, nil
		}
		if m.dialog != nil {
			return m, m.dialogInput(msg)
		}
		if !m.help && m.scroll == 0 && len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
			m.tabs[m.active].pane.Paste(msg.Content)
		}
	case tea.MouseMsg:
		if m.credentialUI != nil {
			return m, nil
		}
		return m, m.mouse(msg)

	}
	if m.credentialUI != nil {
		var cmd tea.Cmd
		m.credentialUI.input, cmd = m.credentialUI.input.Update(msg)
		return m, cmd
	}
	if m.forgeUI != nil {
		return m, m.forgeInput(msg)
	}
	if m.notes != nil && !m.notes.busy {
		var cmd tea.Cmd
		m.notes.input, cmd = m.notes.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) selectTab(i int) {
	if len(m.tabs) > 0 {
		m.active = (i + len(m.tabs)) % len(m.tabs)
		m.scroll = 0
		delete(m.attention, m.tabs[m.active].task.ID)
	}
}

func (m *Model) forwardKey(msg tea.KeyPressMsg, release bool) {
	delete(m.swallowed, msg.Code)
	if len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
		m.tabs[m.active].pane.Key(uv.Key(msg), release)
	}
}

func icon(s term.State, frame int, mode string) string {
	if mode == "ascii" {
		switch s {
		case term.Working:
			return string("-\\|/"[(frame/3)%4])
		case term.Done:
			return "+"
		case term.NeedsInput:
			return "?"
		case term.Exited:
			return "x"
		case term.Crashed:
			return "!"
		default:
			return "o"
		}
	}
	switch s {
	case term.Working:
		return string([]rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")[(frame/3)%10])
	case term.Done:
		return "✓"
	case term.NeedsInput:
		return "⚑"
	case term.Exited:
		return "■"
	case term.Crashed:
		return "⚠"
	default:
		return "○"
	}
}

func fit(s string, width int) string { return ansi.Truncate(s, max(1, width), "") }
func (m *Model) View() tea.View {
	m.styleInputs()
	cols, rows := m.size()
	body := "No tasks yet. " + m.prefix + " c creates a task."
	var cursor *tea.Cursor
	if len(m.tabs) > 0 {
		t := m.tabs[m.active]

		switch {
		case t.pane != nil:
			s := t.pane.Snapshot(true)
			body = s.Screen
			if s.CursorVisible && s.State != term.Exited && s.State != term.Crashed {
				bounds := m.paneBounds()
				cursor = tea.NewCursor(s.X+bounds.x, s.Y+bounds.y)
			}
		case t.err != nil:
			body = "Could not start " + t.task.Agent + ":\n" + t.err.Error() + "\n\n" + m.prefix + " r retry/resume · " + m.prefix + " R fresh session"
		default:
			body = "Starting " + t.task.Agent + "…"
		}
		if m.scroll > 0 && t.pane != nil {
			lines := t.pane.Scrollback()
			end := max(0, len(lines)-m.scroll)
			begin := max(0, end-rows)
			body = strings.Join(lines[begin:end], "\n")
			cursor = nil
		}
	}
	if m.help {
		body = m.helpView(cols, rows)
		cursor = nil
	}

	if m.dialog != nil {
		body = m.dialog.view(cols, rows, m.paletteColors())
		cursor = nil
	}
	if m.switcher != nil {
		body = m.switcher.View(cols, rows)
		cursor = nil
	}
	if m.history != nil {
		body = m.history.View(cols, rows)
		cursor = nil
	}
	if m.notes != nil {
		body = m.notes.View()
		cursor = nil
	}
	if m.forgeUI != nil {
		body = m.forgeUI.View(m.cfg.Git.MergeMethod, rows)
		cursor = nil
	}
	if m.diff != nil {
		body = m.diff.view(cols, rows, m.paletteColors())
		cursor = nil
	}
	if m.palette != nil {
		body = m.paletteView(cols, rows)
		cursor = nil
	}
	if m.themes != nil {
		body = m.themeView(cols, rows)
		cursor = nil
	}
	if m.credentialUI != nil {
		body = m.credentialView(cols, rows)
		cursor = nil
	}
	lines := strings.Split(body, "\n")
	for len(lines) < rows {
		lines = append(lines, "")
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for i := range lines {
		lines[i] = fit(lines[i], cols)
	}
	footer := m.shellFooter(m.width - 2*m.geometry().padding)
	if m.prefixed {
		footer = "Prefix: a agent · h history · n notes · e editor · c new · x stop · r resume · R fresh · t shell · z suspend · [ scroll · q quit"
	}
	if time.Now().Before(m.toastUntil) {
		text := strings.Join(strings.Fields(ansi.Strip(m.toast)), " ")
		footer = ansi.Truncate(text, max(1, m.width-2*m.geometry().padding), "…")
	}
	if m.quitting {
		footer = "Stopping agents and saving sessions…"
	}
	v := tea.NewView(m.composeShell(lines, footer))
	// Auto must query the terminal's original background before changing it.
	if m.cfg.Theme != "auto" || m.backgroundKnown {
		v.BackgroundColor = lipgloss.Color(m.paletteColors().Background)
		v.ForegroundColor = lipgloss.Color(m.paletteColors().Foreground)
	}
	v.ReportFocus = true
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = cursor
	v.WindowTitle = "Maestro"
	v.KeyboardEnhancements.ReportEventTypes = true
	v.KeyboardEnhancements.ReportAlternateKeys = true
	return v
}

func (m *Model) openEditor(worktree string) tea.Cmd {
	editor := strings.TrimSpace(m.cfg.Editor)
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	parts = append(parts, ".")
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = worktree
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorFinishedMsg{err}
	})
}
