package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mmoehabb/maestro/internal/agent"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
)

type newTaskDialog struct {
	fields           [3]textinput.Model
	agents, detected []string
	field, selected  int
	err              string
	busy             bool
}

func newDialog(cfg config.Config, base string) *newTaskDialog {
	d := &newTaskDialog{}
	for i := range d.fields {
		d.fields[i] = textinput.New()
		d.fields[i].SetVirtualCursor(true)
		d.fields[i].CharLimit = 4096
		d.fields[i].SetWidth(52)
	}
	d.fields[0].Placeholder = "Fix login"
	d.fields[1].SetValue(base)
	d.fields[2].Placeholder = "Optional first prompt"
	d.fields[0].Focus()
	for name := range cfg.Agents {
		d.agents = append(d.agents, name)
	}
	sort.Strings(d.agents)
	for i, name := range d.agents {
		g := agent.Generic{Name: name, Config: cfg.Agents[name]}
		_, err := g.Detect()
		state := "installed"
		if err != nil {
			state = "not found"
		}
		d.detected = append(d.detected, state)
		if name == cfg.DefaultAgent {
			d.selected = i
		}
	}
	return d
}

func (d *newTaskDialog) inputIndex() int {
	if d.field == 3 {
		return 2
	}
	return d.field
}

func (d *newTaskDialog) focus(field int) {
	for i := range d.fields {
		d.fields[i].Blur()
	}
	d.field = (field + 4) % 4
	if d.field != 2 {
		d.fields[d.inputIndex()].Focus()
	}
}

func (m *Model) dialogPaste(msg tea.PasteMsg) tea.Cmd {
	d := m.dialog
	if d.busy || d.field == 2 {
		return nil
	}
	i := d.inputIndex()
	var cmd tea.Cmd
	d.fields[i], cmd = d.fields[i].Update(msg)
	return cmd
}

func (m *Model) dialogKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.dialog
	if d.busy {
		return nil
	}
	switch msg.String() {
	case "esc":
		m.dialog = nil
		return nil
	case "tab", "down":
		d.focus(d.field + 1)
		return nil
	case "shift+tab", "up":
		d.focus(d.field - 1)
		return nil
	case "left":
		if d.field == 2 {
			d.selected = (d.selected + len(d.agents) - 1) % len(d.agents)
			return nil
		}
	case "right":
		if d.field == 2 {
			d.selected = (d.selected + 1) % len(d.agents)
			return nil
		}
	case "enter":
		if d.field != 3 {
			d.focus(d.field + 1)
			return nil
		}
		return m.submitTask()
	case "ctrl+enter":
		return m.submitTask()
	}
	if d.field != 2 {
		i := d.inputIndex()
		var cmd tea.Cmd
		d.fields[i], cmd = d.fields[i].Update(msg)
		return cmd
	}
	return nil
}

func (m *Model) submitTask() tea.Cmd {
	d := m.dialog
	if strings.TrimSpace(d.fields[0].Value()) == "" {
		d.err = "Enter a title."
		d.focus(0)
		return nil
	}
	d.busy = true
	d.err = ""
	in := core.NewTask{Title: d.fields[0].Value(), Base: d.fields[1].Value(), Agent: d.agents[d.selected], Prompt: d.fields[2].Value()}
	return func() tea.Msg { task, err := m.runtime.Create(context.Background(), in); return createdMsg{task, err} }
}

func (d *newTaskDialog) View(width, height int) string {
	return d.view(width, height, resolvePalette(config.Config{Theme: "dark"}, false))
}

func (d *newTaskDialog) view(width, height int, p config.Palette) string {
	accent := colored(p.Accent)
	for i := range d.fields {
		d.fields[i].SetWidth(max(1, min(56, width-12)))
		d.fields[i].SetStyles(inputStyles(p))
	}
	picker := fmt.Sprintf("‹ %s ›  %s", d.agents[d.selected], d.detected[d.selected])
	if d.field == 2 {
		picker = accent.Render(picker)
	}
	text := accent.Bold(true).Render("New task") + "\n\nTitle\n" + d.fields[0].View() + "\nBase branch\n" + d.fields[1].View() + "\nAgent (←/→)\n" + picker + "\nFirst prompt\n" + d.fields[2].View() + "\n\nTab next · Enter continue/create · Esc cancel"
	if d.busy {
		text += "\nCreating worktree…"
	}
	if d.err != "" {
		text += "\n" + d.err
	}
	if width < 55 || height < 18 {
		return text
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.Accent)).Padding(1, 2).Render(text)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
