package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/agent"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/handoff"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type switchDialog struct {
	task           store.Task
	agents, labels []string
	selected       int
	confirm, busy  bool
	fresh          bool
	err            string
	offset         int
	scrolled       bool
}
type switchedMsg struct {
	target string
	id     int64
	pane   *term.Pane
	task   store.Task
	err    error
}
type historyMsg struct {
	slug    string
	history store.History
	err     error
}
type historyView struct {
	slug             string
	data             store.History
	expanded         map[int64]bool
	tools            bool
	offset, selected int
	revealSelection  bool
	loading          bool
	err              string
}

func (m *Model) SetSwitchAgent(name string) { m.initialAgent = name }
func (m *Model) openSwitch() tea.Cmd {
	if len(m.tabs) == 0 || m.tabs[m.active].pending {
		return nil
	}
	t := m.tabs[m.active].task
	d := &switchDialog{task: t}
	for name := range m.cfg.Agents {
		d.agents = append(d.agents, name)
	}
	sort.Strings(d.agents)
	// Detection is a local PATH lookup; history loading remains asynchronous.
	for i, name := range d.agents {
		label := name
		if _, err := (agent.Generic{Name: name, Config: m.cfg.Agents[name]}).Detect(); err != nil {
			label += " · not installed"
		}
		if name == t.Agent {
			label += " · current"
			d.selected = i
		}
		d.labels = append(d.labels, label)
	}
	m.switcher = d
	return func() tea.Msg {
		h, err := m.service.History(context.Background(), t.Slug)
		return historyMsg{t.Slug, h, err}
	}
}

func (m *Model) switchTask(task store.Task, target string, confirmed bool) tea.Cmd {
	for i := range m.tabs {
		if m.tabs[i].task.ID == task.ID {
			m.tabs[i].pending = true
		}
	}
	if m.switcher != nil {
		m.switcher.busy = true
		m.switcher.err = ""
	}
	cols, rows := m.size()
	fresh := m.switcher != nil && m.switcher.fresh
	return func() tea.Msg {
		var p *term.Pane
		var err error
		if fresh {
			p, err = m.runtime.SwitchFresh(context.Background(), task, target, cols, rows, confirmed)
		} else {
			p, err = m.runtime.Switch(context.Background(), task, target, cols, rows, confirmed)
		}
		latest, e := m.service.Find(context.Background(), task.Slug)
		if e == nil {
			task = latest
		}
		return switchedMsg{target: target, id: task.ID, pane: p, task: task, err: err}
	}
}

func (m *Model) switchKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.switcher
	if d.busy {
		return nil
	}
	_, rows := m.size()
	switch msg.String() {
	case "pgdown":
		d.offset += max(1, rows-5)
		d.scrolled = true
		return nil
	case "pgup":
		d.offset = max(0, d.offset-max(1, rows-5))
		d.scrolled = true
		return nil
	}
	if d.confirm {
		switch msg.String() {
		case "y", "enter":
			return m.switchTask(d.task, d.agents[d.selected], true)
		case "n", "esc":
			d.confirm = false
			d.fresh = false
			d.err = ""
			d.offset = 0
			d.scrolled = false
		}
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		m.switcher = nil
	case "up", "k":
		d.selected = (d.selected + len(d.agents) - 1) % len(d.agents)
		d.scrolled = false
	case "down", "j":
		d.selected = (d.selected + 1) % len(d.agents)
		d.scrolled = false
	case "enter":
		return m.switchTask(d.task, d.agents[d.selected], false)
	}
	return nil
}

func (d *switchDialog) View(width, height int) string {
	width, height = max(1, width), max(1, height)
	header := ansi.Truncate("Switch agent · "+d.task.Slug, width, "…")
	footer := "↑/↓ select · Enter switch · Esc cancel"
	var lines []string
	if d.confirm {
		prompt := "The agent is active. Interrupt it and switch?"
		if d.fresh {
			prompt = "No saved session ID. Stop the current agent and start a fresh session with a handoff?"
		}
		lines = strings.Split(ansi.Wrap(prompt+"\n\nTarget: "+d.agents[d.selected], width, ""), "\n")
		footer = "y/Enter confirm · n/Esc cancel"
	} else {
		for i, label := range d.labels {
			mark := "  "
			if i == d.selected {
				mark = "› "
			}
			lines = append(lines, ansi.Truncate(mark+label, width, "…"))
		}
	}
	if d.busy {
		footer = "Saving history and preparing handoff…"
	}
	if d.err != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(ansi.Wrap(d.err, width, ""), "\n")...)
	}
	footerLines := strings.Split(ansi.Wrap(footer, width, ""), "\n")
	rows := max(1, height-3-len(footerLines))
	if len(lines) > rows {
		footerLines = append(footerLines, "PgUp/PgDn scroll")
		rows = max(1, rows-1)
	}
	if !d.confirm && !d.scrolled && len(d.labels) > 0 {
		if d.selected < d.offset {
			d.offset = d.selected
		} else if d.selected >= d.offset+rows {
			d.offset = d.selected - rows + 1
		}
	}
	d.offset = min(d.offset, max(0, len(lines)-rows))
	body := append([]string{header, ""}, lines[d.offset:min(len(lines), d.offset+rows)]...)
	body = append(body, "")
	body = append(body, footerLines...)
	return strings.Join(body, "\n")
}

func (m *Model) openHistory() tea.Cmd {
	if len(m.tabs) == 0 {
		return nil
	}
	slug := m.tabs[m.active].task.Slug
	m.history = &historyView{slug: slug, expanded: map[int64]bool{}, loading: true}
	return func() tea.Msg {
		h, err := m.service.History(context.Background(), slug)
		return historyMsg{slug, h, err}
	}
}

func (m *Model) historyKey(msg tea.KeyPressMsg) tea.Cmd {
	h := m.history
	switch msg.String() {
	case "esc", "q":
		m.history = nil
	case "j", "down":
		h.offset++
	case "k", "up":
		h.offset = max(0, h.offset-1)
	case "pgdown":
		h.offset += max(1, m.height-8)
	case "pgup":
		h.offset = max(0, h.offset-m.height+8)
	case "tab":
		if len(h.data.Sessions) > 0 {
			h.selected = (h.selected + 1) % len(h.data.Sessions)
			h.revealSelection = true
		}
	case "shift+tab":
		if len(h.data.Sessions) > 0 {
			h.selected = (h.selected + len(h.data.Sessions) - 1) % len(h.data.Sessions)
			h.revealSelection = true
		}
	case "enter", "space":
		if len(h.data.Sessions) > 0 {
			id := h.data.Sessions[h.selected].ID
			h.expanded[id] = !h.expanded[id]
			h.revealSelection = true
		}
	case "t":
		h.tools = !h.tools
	case "r":
		return m.openHistory()
	}
	return nil
}

func (h *historyView) View(width, height int) string {
	if h.loading {
		return "Loading task history…"
	}
	if h.err != "" {
		return "History: " + h.err + "\nEsc return"
	}
	var b strings.Builder
	b.WriteString("History · " + h.data.Task.Title + "\n")
	b.WriteString("Tab session · Enter expand · t tools · j/k scroll · r refresh · Esc return\n\n")
	for _, e := range h.data.Events {
		fmt.Fprintf(&b, "%s  %s\n", e.TS.Local().Format("Jan 02 15:04:05"), e.Kind)
	}
	if len(h.data.Sessions) == 0 {
		b.WriteString("No agent sessions yet.\n")
	}
	selectedOffset := -1
	for i, s := range h.data.Sessions {
		mark := " "
		if h.selected == i {
			mark = "›"
			selectedOffset = b.Len() + 1 // The session header follows a blank line.
		}
		expand := "+"
		if h.expanded[s.ID] {
			expand = "−"
		}
		fmt.Fprintf(&b, "\n%s %s %s · session %d · %s\n", mark, expand, s.Agent, s.ID, s.StartedAt.Local().Format("Jan 02 15:04"))
		if !h.expanded[s.ID] {
			continue
		}
		for _, t := range h.data.Turns {
			if t.SessionID != s.ID {
				continue
			}
			if strings.HasPrefix(t.Role, "tool_") && !h.tools {
				fmt.Fprintf(&b, "  [%s hidden; t to expand]\n", t.Role)
				continue
			}
			fmt.Fprintf(&b, "\n[%s]\n%s\n", t.Role, ansi.Strip(t.Content))
		}
	}
	for _, x := range h.data.Handoffs {
		fmt.Fprintf(&b, "\nHandoff → %s", x.Agent)
		if x.DeliveredAt == nil {
			b.WriteString(" · pending launch")
		}
		b.WriteString("\n")
	}
	width, height = max(1, width), max(1, height)
	content := b.String()
	lines := strings.Split(ansi.Wrap(content, width, ""), "\n")
	if h.revealSelection {
		if selectedOffset >= 0 {
			// Count rendered rows, including wrapped events and expanded turns.
			row := strings.Count(ansi.Wrap(content[:selectedOffset], width, ""), "\n")
			if row < h.offset || row >= h.offset+height {
				h.offset = row
			}
		}
		h.revealSelection = false
	}
	h.offset = min(h.offset, max(0, len(lines)-height))
	return strings.Join(lines[h.offset:min(len(lines), h.offset+height)], "\n")
}

func (m *Model) contextMessage(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case historyMsg:
		if m.switcher != nil && msg.history.Task.ID == m.switcher.task.ID && msg.err == nil {
			used := map[string]bool{}
			for _, s := range msg.history.Sessions {
				used[s.Agent] = true
			}
			for i, name := range m.switcher.agents {
				if used[name] && !strings.Contains(m.switcher.labels[i], "previously used") {
					m.switcher.labels[i] += " · previously used"
				}
			}
		}
		if m.history != nil && m.history.slug == msg.slug {
			m.history.loading = false
			m.history.data = msg.history
			if msg.err != nil {
				m.history.err = msg.err.Error()
			}
		}
		return true
	case switchedMsg:
		for i := range m.tabs {
			t := &m.tabs[i]
			if t.task.ID != msg.id {
				continue
			}
			t.pending = false
			t.task = msg.task
			if msg.err == nil {
				t.attachPane(msg.pane)
				t.err = nil
				if msg.pane != nil {
					c, r := m.size()
					_ = msg.pane.Resize(c, r)
				}
			} else if !errors.Is(msg.err, core.ErrInterruptRequired) && !errors.Is(msg.err, core.ErrFreshStartRequired) {
				t.err = msg.err
			}
		}
		if errors.Is(msg.err, core.ErrInterruptRequired) || errors.Is(msg.err, core.ErrFreshStartRequired) {
			if m.switcher == nil {
				m.switcher = &switchDialog{task: msg.task, agents: []string{msg.target}, labels: []string{msg.target}}
			}
			m.switcher.confirm = true
			m.switcher.fresh = errors.Is(msg.err, core.ErrFreshStartRequired)
			m.switcher.busy = false
			m.switcher.offset = 0
			m.switcher.scrolled = false
			return true
		}
		if msg.err != nil {
			if m.switcher != nil {
				m.switcher.busy = false
				m.switcher.err = msg.err.Error()
			}
			m.notify(msg.err.Error())
			return true
		}
		m.switcher = nil
		if m.cfg.Agents[msg.task.Agent].ManualPrompt {
			m.manualInstruction = handoff.Prompt(msg.task.Worktree)
			m.notify("Press prefix H to copy the handoff instruction, then paste it into the agent.")
		}
		return true
	}
	return false
}
