package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type action struct {
	key, label string
	task       bool
}

var actions = []action{
	{":", "Command palette", false},
	{"c", "New task", false},
	{"a", "Switch agent", true},
	{"h", "Task history", true},
	{"n", "Edit task notes", true},
	{"H", "Copy manual handoff instruction", true},
	{"D", "View diff against base", true},
	{"s", "Toggle tabs / sidebar", false},
	{"T", "Choose color theme", false},
	{"?", "Help", false},
	{"g", "Sign in to GitHub", false},
	{"p", "Push branch", true},
	{"P", "Create / open pull request", true},
	{"m", "Merge pull request", true},
	{"&", "Archive / clean up worktree", true},
	{"u", "Reopen archived task", false},
	{"d", "Hide tab (preserve worktree)", true},
	{"x", "Stop agent", true},
	{"r", "Restart / resume agent", true},
	{"R", "Start a fresh session", true},
	{"[", "Scroll / copy terminal history", true},
	{"e", "Open editor", true},
	{"t", "Open shell in worktree", true},
	{"z", "Suspend Maestro", false},
	{"q", "Quit and save sessions", false},
}

func (m *Model) disabled(a action) string {
	if a.task && len(m.tabs) == 0 {
		return "No active task"
	}
	if a.task && m.tabs[m.active].pending {
		return "Task operation in progress"
	}
	if a.key == "H" && m.manualInstruction == "" {
		return "No manual handoff instruction"
	}
	return ""
}

func (m *Model) dispatch(key string) tea.Cmd {
	m.dragID = 0
	m.dragMoved = false
	for _, a := range actions {
		if a.key == key {
			if reason := m.disabled(a); reason != "" {
				m.notify(reason)
				return nil
			}
			break
		}
	}
	switch key {
	case ":":
		return m.openPalette()
	case "D":
		return m.openDiff()
	case "T":
		return m.openThemes()
	case "s":
		m.sidebar = !m.sidebar
		m.resizePanes()

	case "q":
		m.quitting = true
		return tea.Quit
	case "c":
		m.dialog = newDialog(m.cfg, m.service.Repo.DefaultBranch)
	case "a":
		return m.openSwitch()
	case "h":
		return m.openHistory()
	case "n":
		return m.openNotes()
	case "H":
		if m.manualInstruction != "" {
			return tea.SetClipboard(m.manualInstruction)
		}
	case "?":
		m.help = true
		m.helpOffset = 0
	case "[":
		m.scroll = 1
	case "r", "R":
		if len(m.tabs) > 0 {
			return m.launch(m.active, key == "R")
		}
	case "t":
		if len(m.tabs) > 0 {
			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = os.Getenv("COMSPEC")
			}
			if shell == "" {
				shell = "sh"
			}
			cmd := exec.Command(shell)
			cmd.Dir = m.tabs[m.active].task.Worktree
			return tea.ExecProcess(cmd, func(err error) tea.Msg { return shellMsg{err} })
		}
	case "z":
		return tea.Suspend
	case "g":
		return m.loginGitHub()
	case "p":
		return m.openForge("push")
	case "P":
		return m.openForge("pr")
	case "m":
		return m.openForge("merge")
	case "&":
		return m.openForge("cleanup")
	case "u":
		return m.openForge("reopen")
	case "d":
		if len(m.tabs) > 0 && !m.tabs[m.active].pending {
			t := &m.tabs[m.active]
			t.pending = true
			task := t.task
			return func() tea.Msg { return archivedMsg{task.ID, m.runtime.Archive(context.Background(), task)} }
		}
	case "x":
		if len(m.tabs) > 0 && !m.tabs[m.active].pending {
			t := &m.tabs[m.active]
			t.pending = true
			id := t.task.ID
			return func() tea.Msg { m.runtime.Stop(id); return stoppedMsg{id} }
		}
	case "e":
		if len(m.tabs) > 0 {
			return m.openEditor(m.tabs[m.active].task.Worktree)
		}
	default:
		m.notify("Unknown shortcut; " + m.prefix + " ? for help")
	}
	return nil
}

func (m *Model) helpText() string {
	var b strings.Builder
	b.WriteString("Maestro shortcuts\n\nalt+1…9 select · alt+h/l previous/next\n\n")
	for _, a := range actions {
		b.WriteString(m.prefix + " " + a.key + "  " + a.label + "\n")
	}
	b.WriteString("\n" + m.prefix + " " + m.prefix + "  Send prefix to agent\nDrag tasks to reorder · Mouse wheel scrolls\nCtrl+C and Enter reach the agent outside views.")
	return b.String()
}

func (m *Model) helpKey(msg tea.KeyPressMsg) {
	_, rows := m.size()
	switch msg.String() {
	case "esc", "q", "?":
		m.help = false
	case "j", "down":
		m.helpOffset++
	case "k", "up":
		m.helpOffset = max(0, m.helpOffset-1)
	case "pgdown":
		m.helpOffset += max(1, rows-1)
	case "pgup":
		m.helpOffset = max(0, m.helpOffset-rows+1)
	case "home":
		m.helpOffset = 0
	case "end":
		m.helpOffset = 10000
	}
}

func (m *Model) helpView(width, height int) string {
	lines := strings.Split(ansi.Wrap(m.helpText(), max(1, width), ""), "\n")
	rows := max(1, height-1)
	m.helpOffset = min(m.helpOffset, max(0, len(lines)-rows))
	return strings.Join(lines[m.helpOffset:min(len(lines), m.helpOffset+rows)], "\n") + "\n↑/↓ scroll · PgUp/PgDn · Esc return"
}
