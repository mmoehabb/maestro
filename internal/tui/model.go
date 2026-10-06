package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/handoff"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type tab struct {
	task    store.Task
	pane    *term.Pane
	err     error
	pending bool
	status  git.Status
	state   term.State
}
type (
	tickMsg          time.Time
	prefixTimeoutMsg struct{}
	loadedMsg        struct {
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
)

type Model struct {
	initialAgent, manualInstruction                           string
	switcher                                                  *switchDialog
	history                                                   *historyView
	service                                                   *core.TaskService
	runtime                                                   *core.Runtime
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
	tabOffsets                                                []int
	swallowed                                                 map[rune]bool
}

func New(s *core.TaskService, r *core.Runtime, focus string) *Model {
	return &Model{
		service: s, runtime: r, cfg: s.Config, focus: focus, width: 80, height: 24,
		prefix: term.ActivePrefix(s.Config.Prefix, s.Config.PrefixFallback, false),
	}
}

func tick() tea.Cmd             { return tea.Tick(time.Second/30, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m *Model) event() tea.Cmd { return func() tea.Msg { return <-m.runtime.Events } }
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tick(), m.event(), tea.Tick(600*time.Millisecond, func(time.Time) tea.Msg { return prefixTimeoutMsg{} }), func() tea.Msg {
		tasks, err := m.service.List(context.Background(), false)
		return loadedMsg{tasks, err}
	})
}

func (m *Model) notify(s string)  { m.toast = s; m.toastUntil = time.Now().Add(6 * time.Second) }
func (m *Model) size() (int, int) { return max(1, m.width), max(1, m.height-4) }
func (m *Model) launch(index int, fresh bool) tea.Cmd {
	t := &m.tabs[index]
	if t.pending {
		return nil
	}
	t.pending = true
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
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		cols, rows := m.size()
		for i := range m.tabs {
			if m.tabs[i].pane != nil {
				if err := m.tabs[i].pane.Resize(cols, rows); err != nil {
					m.notify(err.Error())
				}
			}
		}
	case tea.KeyboardEnhancementsMsg:
		m.enhanced = msg.SupportsKeyDisambiguation()
		m.prefix = term.ActivePrefix(m.cfg.Prefix, m.cfg.PrefixFallback, m.enhanced)
		m.prefixSettled = true
	case prefixTimeoutMsg:
		if !m.prefixSettled && m.prefix != m.cfg.Prefix {
			m.notify("Keyboard disambiguation unavailable; prefix is " + m.prefix + ". Enter goes to the agent.")
		}
		m.prefixSettled = true
	case loadedMsg:
		m.loaded = true
		if msg.err != nil {
			m.notify(msg.err.Error())
			return m, nil
		}
		var cmds []tea.Cmd
		for _, task := range msg.tasks {
			m.tabs = append(m.tabs, tab{task: task, state: term.Starting})
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
		}
		return m, tea.Batch(cmds...)
	case startedMsg:
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.id {
				t := &m.tabs[i]
				t.pending = false
				t.pane = msg.pane
				t.err = msg.err
				if msg.err == nil {
					// Start may have completed a persisted pending switch from an earlier run.
					if m.cfg.Agents[t.task.Agent].ManualPrompt {
						m.manualInstruction = handoff.Instruction
					}
					t.task.Lifecycle = "active"
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
		m.tabs = append(m.tabs, tab{task: msg.task, state: term.Starting})
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
	case core.Event:
		if msg.Err != nil {
			m.notify(msg.Err.Error())
		}
		for i := range m.tabs {
			if m.tabs[i].task.ID == msg.TaskID && msg.State != "" && (msg.Pane == nil || msg.Pane == m.tabs[i].pane) {
				m.tabs[i].state = msg.State
				if i != m.active && (msg.State == term.Done || msg.State == term.NeedsInput) {
					m.notify(m.tabs[i].task.Slug + ": " + string(msg.State))
				}
				if msg.State == term.Done {
					m.nextStatus = time.Time{}
				}
			}
		}
		return m, m.event()
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
		m.frame++
		for i := range m.tabs {
			if m.tabs[i].pane != nil {
				m.tabs[i].state = m.tabs[i].pane.Snapshot(false).State
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
		return m, tea.Batch(tick(), refresh)
	case tea.KeyPressMsg:
		if m.swallowed == nil {
			m.swallowed = make(map[rune]bool)
		}
		m.swallowed[msg.Code] = true
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
			m.help = false
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
				m.scroll += m.height - 4
			case "pgdown":
				m.scroll = max(0, m.scroll-m.height+4)
			case "y":
				if len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
					text := strings.Join(m.tabs[m.active].pane.Scrollback(), "\n")
					m.scroll = 0
					return m, tea.SetClipboard(text)
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
			switch key {
			case "q":
				m.quitting = true
				return m, tea.Quit
			case "c":
				m.dialog = newDialog(m.cfg, m.service.Repo.DefaultBranch)
			case "a":
				return m, m.openSwitch()
			case "h":
				return m, m.openHistory()
			case "H":
				if m.manualInstruction != "" {
					return m, tea.SetClipboard(m.manualInstruction)
				}
			case "?":
				m.help = true
			case "[":
				m.scroll = 1
			case "r", "R":
				if len(m.tabs) > 0 {
					return m, m.launch(m.active, key == "R")
				}
			case "d":
				if len(m.tabs) > 0 && !m.tabs[m.active].pending {
					t := &m.tabs[m.active]
					t.pending = true
					task := t.task
					return m, func() tea.Msg { return archivedMsg{task.ID, m.runtime.Archive(context.Background(), task)} }
				}
			case "x":
				if len(m.tabs) > 0 && !m.tabs[m.active].pending {
					t := &m.tabs[m.active]
					t.pending = true
					id := t.task.ID
					return m, func() tea.Msg { m.runtime.Stop(id); return stoppedMsg{id} }
				}
			default:
				m.notify("Unknown shortcut; " + m.prefix + " ? for help")
			}
			return m, nil
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
		if !m.prefixed && m.dialog == nil && m.switcher == nil && m.history == nil && !m.help && m.scroll == 0 && len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
			m.tabs[m.active].pane.Key(uv.Key(msg), true)
		}
	case tea.PasteMsg:
		if m.switcher != nil || m.history != nil {
			return m, nil
		}
		if m.dialog != nil {
			return m, m.dialogPaste(msg)
		}
		if !m.help && m.scroll == 0 && len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
			m.tabs[m.active].pane.Paste(msg.Content)
		}
	case tea.MouseMsg:
		if m.dialog != nil || m.switcher != nil || m.history != nil || m.help {
			return m, nil
		}
		mouse := msg.Mouse()
		if mouse.Y == 1 {
			if _, ok := msg.(tea.MouseClickMsg); ok {
				for i, offset := range m.tabOffsets {
					if mouse.X >= offset {
						m.selectTab(i)
					}
				}
			}
			return m, nil
		}
		if mouse.Button == tea.MouseWheelUp {
			m.scroll += 3
			return m, nil
		}
		if mouse.Button == tea.MouseWheelDown && m.scroll > 0 {
			m.scroll = max(0, m.scroll-3)
			return m, nil
		}
		if mouse.Y >= 2 && mouse.Y < m.height-2 && len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil && m.scroll == 0 {
			mouse.Y -= 2
			_, release := msg.(tea.MouseReleaseMsg)
			_, motion := msg.(tea.MouseMotionMsg)
			m.tabs[m.active].pane.Mouse(uv.Mouse(mouse), release, motion)
		}
	}
	return m, nil
}

func (m *Model) selectTab(i int) {
	if len(m.tabs) > 0 {
		m.active = (i + len(m.tabs)) % len(m.tabs)
		m.scroll = 0
	}
}

func (m *Model) forwardKey(msg tea.KeyPressMsg, release bool) {
	delete(m.swallowed, msg.Code)
	if len(m.tabs) > 0 && !m.tabs[m.active].pending && m.tabs[m.active].pane != nil {
		m.tabs[m.active].pane.Key(uv.Key(msg), release)
	}
}

var (
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa"))
	muted  = lipgloss.NewStyle().Foreground(lipgloss.Color("#9399b2"))
)

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
	cols, rows := m.size()
	header := accent.Render(" ♪ maestro ") + muted.Render(m.service.Repo.Root)
	var tabs strings.Builder
	m.tabOffsets = nil
	// Keep the active tab visible; overflow is indicated instead of wrapping.
	start := 0
	labels := make([]string, len(m.tabs))
	total := 0
	for i, t := range m.tabs {
		labels[i] = fmt.Sprintf(" %s %s · %s ", icon(t.state, m.frame, m.cfg.Icons), t.task.Slug, t.task.Agent)
		total += ansi.StringWidth(labels[i])
		if i == m.active {
			for total > cols-4 && start < i {
				total -= ansi.StringWidth(labels[start])
				start++
			}
		}
	}
	if start > 0 {
		tabs.WriteString("‹ ")
	}
	for i := 0; i < len(m.tabs); i++ {
		if i < start {
			m.tabOffsets = append(m.tabOffsets, -100000)
			continue
		}
		offset := ansi.StringWidth(tabs.String())
		if offset+ansi.StringWidth(labels[i]) > cols-2 && i > m.active {
			tabs.WriteString(" ›")
			break
		}
		m.tabOffsets = append(m.tabOffsets, offset)
		label := labels[i]
		if i == m.active {
			label = accent.Bold(true).Underline(true).Render(label)
		} else {
			label = muted.Render(label)
		}
		tabs.WriteString(label)
	}
	body := "No tasks yet. " + m.prefix + " c creates a task."
	status := ""
	var cursor *tea.Cursor
	if len(m.tabs) > 0 {
		t := m.tabs[m.active]
		status = fmt.Sprintf(" %s · %s · +%d −%d · %d files · ↑%d ↓%d · %s", t.task.Agent, t.task.Branch, t.status.Added, t.status.Deleted, t.status.Dirty, t.status.Ahead, t.status.Behind, t.task.Lifecycle)
		switch {
		case t.pane != nil:
			s := t.pane.Snapshot(true)
			body = s.Screen
			if s.CursorVisible && s.State != term.Exited && s.State != term.Crashed {
				cursor = tea.NewCursor(s.X, s.Y+2)
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
			status = " Scroll: j/k or arrows · PgUp/PgDn · y copy history · q return"
			cursor = nil
		}
	}
	if m.help {
		body = "Maestro shortcuts\n\nalt+1…9  switch tab    alt+h/l  previous/next\n\n" + m.prefix + " c  new task\n" + m.prefix + " a  switch agent\n" + m.prefix + " h  task history\n" + m.prefix + " H  copy manual handoff instruction\n" + m.prefix + " d  archive tab (stop agent, preserve worktree)\n" + m.prefix + " x  stop agent\n" + m.prefix + " r  restart/resume\n" + m.prefix + " R  explicitly start a fresh session\n" + m.prefix + " [  scroll/copy history\n" + m.prefix + " q  quit and stop all agents\n" + m.prefix + " " + m.prefix + "  send prefix to agent\n\nAny key closes help. Ctrl+C is forwarded to the agent."
		cursor = nil
	}
	if m.dialog != nil {
		body = m.dialog.View(cols, rows)
		cursor = nil
	}
	if m.switcher != nil {
		body = m.switcher.View()
		cursor = nil
	}
	if m.history != nil {
		body = m.history.View(cols, rows)
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
	footer := m.prefix + " ? help · " + m.prefix + " c new · " + m.prefix + " q quit"
	if m.prefixed {
		footer = "Prefix: a agent · h history · c new · x stop · r resume · R fresh · [ scroll · q quit"
	}
	if time.Now().Before(m.toastUntil) {
		footer = m.toast
	}
	if m.quitting {
		footer = "Stopping agents and saving sessions…"
	}
	v := tea.NewView(strings.Join([]string{fit(header, cols), fit(tabs.String(), cols), strings.Join(lines, "\n"), fit(muted.Render(status), cols), fit(footer, cols)}, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = cursor
	v.WindowTitle = "Maestro"
	v.KeyboardEnhancements.ReportEventTypes = true
	v.KeyboardEnhancements.ReportAlternateKeys = true
	return v
}
