package tui

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/forge/github"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

type forgeDialog struct {
	task                 store.Task
	action               string
	title, base, confirm textinput.Model
	body                 textarea.Model
	focus                int
	force, busy          bool
	err                  string
	archived             []store.Task
	selected             int
}

type workflowMsg struct {
	task   store.Task
	action string
	err    error
	dialog *forgeDialog
}
type prDraftMsg struct {
	dialog *forgeDialog
	task   store.Task
	draft  forge.NewPR
	err    error
}
type reopenListMsg struct {
	dialog *forgeDialog
	tasks  []store.Task
	err    error
}
type browserMsg struct{ err error }

func (m *Model) workflow(task store.Task, action string, opts core.WorkflowOptions) tea.Cmd {
	for i := range m.tabs {
		if m.tabs[i].task.ID == task.ID {
			if m.tabs[i].pending {
				return nil
			}
			m.tabs[i].pending = true
		}
	}
	dialog := m.forgeUI
	if dialog != nil && (dialog.task.ID == task.ID || dialog.action == "reopen" && action == "reopen") &&
		(dialog.action == action || dialog.action == "cleanup" && action == "archive" || dialog.action == "merge" && action == "refresh") {
		dialog.task = task
		dialog.busy = true
		dialog.err = ""
	} else {
		dialog = nil
	}
	return func() tea.Msg {
		ctx := git.WithCredentialPrompt(context.Background(), m.promptCredentials)
		result, err := m.runtime.Workflow(ctx, task, action, opts)
		return workflowMsg{task: result, action: action, err: err, dialog: dialog}
	}
}

func (m *Model) openForge(action string) tea.Cmd {
	if action == "reopen" {
		m.forgeUI = &forgeDialog{action: "reopen", busy: true}
		dialog := m.forgeUI
		return func() tea.Msg {
			tasks, err := m.service.List(context.Background(), true)
			return reopenListMsg{dialog: dialog, tasks: tasks, err: err}
		}
	}
	if len(m.tabs) == 0 || m.tabs[m.active].pending {
		return nil
	}
	task := m.tabs[m.active].task
	if action == "push" {
		return m.workflow(task, action, core.WorkflowOptions{})
	}
	m.forgeUI = &forgeDialog{task: task, action: action}
	if action == "pr" {
		m.forgeUI = newPRDialog(task, forge.NewPR{Title: task.Title}, m.width, m.height)
	}
	switch action {
	case "pr":
		dialog := m.forgeUI
		m.forgeUI.busy = true
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			current, err := m.runtime.Workflow(ctx, task, "refresh", core.WorkflowOptions{})
			if err != nil {
				return prDraftMsg{dialog: dialog, task: task, err: err}
			}
			if current.PRNumber != 0 {
				return prDraftMsg{dialog: dialog, task: current}
			}
			draft, err := m.service.PRDescription(ctx, current, "")
			// Commit-only bases are editable in the dialog.
			if err != nil && strings.Contains(err.Error(), "specify the PR base") {
				draft, err = m.service.PRDescription(ctx, current, strings.TrimPrefix(m.service.Repo.DefaultBranch, "origin/"))
			}
			return prDraftMsg{dialog: dialog, task: current, draft: draft, err: err}
		}
	case "merge":
		m.forgeUI.busy = true
		return m.workflow(task, "refresh", core.WorkflowOptions{})
	}
	return nil
}

func newPRDialog(task store.Task, draft forge.NewPR, width, height int) *forgeDialog {
	d := &forgeDialog{task: task, action: "pr"}
	d.title = textinput.New()
	d.title.SetVirtualCursor(true)
	d.title.SetValue(draft.Title)
	d.base = textinput.New()
	d.base.SetVirtualCursor(true)
	d.base.SetValue(draft.Base)
	d.body = textarea.New()
	d.body.SetVirtualCursor(true)
	d.body.ShowLineNumbers = false
	d.body.Prompt = ""
	d.body.CharLimit = 60000
	d.body.MaxHeight = 0
	d.body.MaxWidth = 0
	d.body.SetValue(draft.Body)
	d.body.SetWidth(max(1, width-2))
	d.body.SetHeight(max(1, height-13))
	d.title.Focus()
	return d
}

func (m *Model) forgeKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.forgeUI
	if d.busy {
		return nil
	}
	key := msg.String()
	if key == "esc" {
		m.forgeUI = nil
		return nil
	}
	switch d.action {
	case "pr":
		if d.task.PRNumber != 0 {
			switch key {
			case "o":
				return openBrowser(d.task.PRURL)
			case "y":
				return tea.SetClipboard(d.task.PRURL)
			}
			return nil
		}
		switch key {
		case "tab", "shift+tab":
			d.title.Blur()
			d.base.Blur()
			d.body.Blur()
			delta := 1
			if key == "shift+tab" {
				delta = 2
			}
			d.focus = (d.focus + delta) % 3
			switch d.focus {
			case 0:
				return d.title.Focus()
			case 1:
				return d.base.Focus()
			default:
				return d.body.Focus()
			}
		case "ctrl+s":
			if strings.TrimSpace(d.title.Value()) == "" || strings.TrimSpace(d.base.Value()) == "" {
				d.err = "Title and base branch are required."
				return nil
			}
			return m.workflow(d.task, "pr", core.WorkflowOptions{Title: d.title.Value(), Base: d.base.Value(), Body: d.body.Value(), BodySet: true})
		}
		return m.forgeInput(msg)
	case "merge":
		if key == "y" && d.task.PRState == "open" {
			return m.workflow(d.task, "merge", core.WorkflowOptions{ExpectedHead: d.task.PRHeadSHA})
		}
		if key == "n" {
			m.forgeUI = nil
		}
	case "cleanup":
		if d.force {
			if key == "enter" && d.confirm.Value() == d.task.Slug {
				return m.workflow(d.task, "cleanup", core.WorkflowOptions{Force: true, StopAgent: true})
			}
			var cmd tea.Cmd
			d.confirm, cmd = d.confirm.Update(msg)
			return cmd
		}
		switch key {
		case "c":
			return m.workflow(d.task, "cleanup", core.WorkflowOptions{StopAgent: true})
		case "k":
			return m.workflow(d.task, "archive", core.WorkflowOptions{StopAgent: true})
		case "f":
			d.force = true
			d.confirm = textinput.New()
			d.confirm.SetVirtualCursor(true)
			return d.confirm.Focus()
		}
	case "reopen":
		switch key {
		case "j", "down":
			d.selected = min(len(d.archived)-1, d.selected+1)
		case "k", "up":
			d.selected = max(0, d.selected-1)
		case "enter":
			if len(d.archived) > 0 {
				return m.workflow(d.archived[d.selected], "reopen", core.WorkflowOptions{})
			}
		}
	}
	return nil
}

func (m *Model) forgeInput(msg tea.Msg) tea.Cmd {
	d := m.forgeUI
	if d == nil || d.busy || d.action != "pr" || d.task.PRNumber != 0 {
		return nil
	}
	var cmd tea.Cmd
	switch d.focus {
	case 0:
		d.title, cmd = d.title.Update(msg)
	case 1:
		d.base, cmd = d.base.Update(msg)
	default:
		d.body, cmd = d.body.Update(msg)
	}
	return cmd
}

func (d *forgeDialog) View(method string, rows int) string {
	if d.busy {
		return "Working on " + d.action + "…"
	}
	footer := "\n\n" + d.err + "\nEsc cancel"
	switch d.action {
	case "pr":
		if d.task.PRNumber != 0 {
			return fmt.Sprintf("PR #%d · %s\n\n%s\n\nCI: %s · Review: %s\n\no open in browser · y copy URL", d.task.PRNumber, d.task.PRState, d.task.PRURL, d.task.CIState, d.task.ReviewState) + footer
		}
		return "Create pull request · " + d.task.Slug + "\nTitle: " + d.title.View() + "\nBase:  " + d.base.View() + "\n\n" + d.body.View() + "\nTab change field · Ctrl+S push and create" + footer
	case "merge":
		if d.task.PRState != "open" {
			return "No open PR. Create one with prefix P." + footer
		}
		return fmt.Sprintf("Merge PR #%d using %s?\n\n%s\nHead: %s\nCI: %s · Review: %s\n\nOnly committed, pushed changes are included.\n\ny merge · n cancel", d.task.PRNumber, method, d.task.PRURL, d.task.PRHeadSHA, d.task.CIState, d.task.ReviewState) + footer
	case "cleanup":
		if d.force {
			return "Force cleanup · " + d.task.Slug + "\n\nStops the agent and deletes all uncommitted files in its worktree.\nLocal commits are retained in a recovery ref; history stays saved.\n\nType the task slug to confirm, then Enter:\n" + d.confirm.View() + footer
		}
		return "Archive · " + d.task.Slug + "\n\nStops the agent and saves its final history.\n\nc  Clean worktree and branch after safety checks\nk  Keep worktree and branch; hide the tab\nf  Force cleanup (requires another confirmation)" + footer
	case "reopen":
		lines := []string{"Reopen archived task", ""}
		start := max(0, d.selected-max(1, rows-7)+1)
		for i := start; i < len(d.archived) && len(lines) < rows-4; i++ {
			prefix := "  "
			if i == d.selected {
				prefix = "> "
			}
			lines = append(lines, prefix+d.archived[i].Slug)
		}
		if len(d.archived) == 0 {
			lines = append(lines, "No archived tasks.")
		}
		return strings.Join(lines, "\n") + "\nEnter reopen · ↑/↓ select" + footer
	}
	return footer
}

func (m *Model) workflowResult(msg workflowMsg) tea.Cmd {
	owned := msg.dialog != nil && m.forgeUI == msg.dialog
	for i := range m.tabs {
		if m.tabs[i].task.ID == msg.task.ID {
			m.tabs[i].pending = false
		}
	}
	if msg.err != nil {
		if msg.action == "cleanup" && m.forgeUI == nil {
			m.forgeUI = &forgeDialog{task: msg.task, action: "cleanup"}
			owned = true
		}
		if owned {
			m.forgeUI.busy = false
			m.forgeUI.err = msg.err.Error()
		}
		m.notify(msg.err.Error())
		return nil
	}
	m.applyTask(msg.task)
	if owned && msg.action == "refresh" && m.forgeUI.action == "merge" {
		m.forgeUI.task = msg.task
		m.forgeUI.busy = false
		return nil
	}
	if owned {
		m.forgeUI = nil
	}
	if msg.action == "reopen" {
		m.tabs = append(m.tabs, tab{task: msg.task})
		m.observed[msg.task.ID] = activityObservation{state: term.Starting}
		m.active = len(m.tabs) - 1
		return m.launch(m.active, false)
	}
	m.notify(msg.task.Slug + ": " + msg.task.Lifecycle)
	if msg.action == "merge" && msg.task.Lifecycle == "merged" {
		switch m.cfg.Git.Cleanup {
		case "auto":
			return m.workflow(msg.task, "cleanup", core.WorkflowOptions{})
		case "ask":
			m.queueCleanup(msg.task)
		}
	}
	return nil
}

func (m *Model) applyTask(task store.Task) {
	for i := range m.tabs {
		if m.tabs[i].task.ID != task.ID {
			continue
		}
		if task.Lifecycle == "archived" {
			delete(m.observed, task.ID)
			delete(m.attention, task.ID)
			m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
			if i < m.active {
				m.active--
			}
			m.active = max(0, min(m.active, len(m.tabs)-1))
			m.scroll = 0
		} else {
			m.tabs[i].task = task
		}
		return
	}
}

func openBrowser(raw string) tea.Cmd {
	return func() tea.Msg {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() != "github.com" {
			return browserMsg{fmt.Errorf("invalid GitHub PR URL")}
		}
		name := "xdg-open"
		args := []string{raw}
		switch runtime.GOOS {
		case "darwin":
			name = "open"
		case "windows":
			name = "rundll32"
			args = []string{"url.dll,FileProtocolHandler", raw}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return browserMsg{exec.CommandContext(ctx, name, args...).Run()}
	}
}

func lifecycleBadgeWithPalette(task store.Task, mode string, p config.Palette) string {
	label, color := "", p.Muted
	switch task.Lifecycle {
	case "pushed":
		label = "↑"
		color = p.Accent
		if mode == "ascii" {
			label = "^"
		}
	case "pr_open":
		label = fmt.Sprintf("#%d", task.PRNumber)
		color = p.Accent
	case "merged":
		label = "⊕"
		color = p.Merged
		if mode == "ascii" {
			label = "M"
		}
	case "closed":
		label = "✕"
		color = p.Error
		if mode == "ascii" {
			label = "C"
		}
	}
	if task.ReviewState == "changes_requested" && task.PRState == "open" {
		label += "!"
		color = p.Warning
	}
	if label == "" {
		return ""
	}
	if task.PRState == "open" && task.CIState != "" && task.CIState != "none" {
		mark, ciColor := "·", p.Warning
		switch task.CIState {
		case "success":
			ciColor = p.Success
			mark = "+"
		case "failure":
			ciColor = p.Error
			mark = "!"
		}
		label = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label) + lipgloss.NewStyle().Foreground(lipgloss.Color(ciColor)).Render(mark)
		return label + " "
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label) + " "
}

func (m *Model) queueCleanup(task store.Task) {
	if m.forgeUI != nil && m.forgeUI.action == "cleanup" && m.forgeUI.task.ID == task.ID {
		return
	}
	for _, queued := range m.cleanupQueue {
		if queued.ID == task.ID {
			return
		}
	}
	m.cleanupQueue = append(m.cleanupQueue, task)
}

type githubLoginMsg struct{ err error }

func (m *Model) loginGitHub() tea.Cmd {
	cmd, err := github.LoginCommand(context.Background())
	if err != nil {
		m.notify(err.Error())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return githubLoginMsg{err} })
}
