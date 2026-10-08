package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

type diffView struct {
	lines              []string
	task               store.Task
	result             git.DiffResult
	loading            bool
	err                error
	offset, horizontal int
}
type diffMsg struct {
	view   *diffView
	result git.DiffResult
	err    error
}

func (m *Model) openDiff() tea.Cmd {
	if len(m.tabs) == 0 {
		return nil
	}
	return m.loadDiff(m.tabs[m.active].task)
}

func (m *Model) loadDiff(task store.Task) tea.Cmd {
	d := &diffView{task: task, loading: true}
	m.diff = d
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.service.Diff(ctx, task)
		return diffMsg{d, result, err}
	}
}

func (m *Model) diffKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.diff
	_, height := m.size()
	switch msg.String() {
	case "esc", "q":
		m.diff = nil
	case "r":
		return m.loadDiff(d.task)
	case "j", "down":
		d.offset++
	case "k", "up":
		d.offset = max(0, d.offset-1)
	case "pgdown":
		d.offset += max(1, height-3)
	case "pgup":
		d.offset = max(0, d.offset-height+3)
	case "home":
		d.offset = 0
		d.horizontal = 0
	case "end":
		d.offset = 1 << 30
	case "left", "h":
		d.horizontal = max(0, d.horizontal-8)
	case "right", "l":
		d.horizontal = min(MaxDiffColumns, d.horizontal+8)
	}
	return nil
}

const MaxDiffColumns = 4096

func (d *diffView) view(width, height int, p config.Palette) string {
	title := colored(p.Accent).Bold(true).Render("Diff · " + d.task.Slug + " · base " + d.task.BaseBranch)
	footer := "↑/↓ scroll · ←/→ pan · r refresh · Esc return"
	if d.loading {
		return title + "\n\nLoading diff…\n" + footer
	}
	if d.err != nil {
		return title + "\n\n" + d.err.Error() + "\n" + footer
	}
	if d.lines == nil {
		content := strings.TrimSuffix(ansi.Strip(d.result.Patch), "\n")
		if content == "" {
			content = "No tracked changes against base."
		}
		if d.result.Untracked != "" {
			content += "\n\nUntracked files (not included in patch):\n" + ansi.Strip(d.result.Untracked)
		}
		if d.result.Truncated {
			content += "\n[Diff truncated at 2 MiB; inspect the full diff in your editor.]"
		}
		d.lines = strings.Split(strings.ReplaceAll(content, "\t", "    "), "\n")
	}
	lines := d.lines
	rows := max(1, height-3)
	d.offset = min(d.offset, max(0, len(lines)-rows))
	visible := []string{title, ""}
	for _, line := range lines[d.offset:min(len(lines), d.offset+rows)] {
		color := p.Foreground
		switch {
		case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			color = p.Accent
		case strings.HasPrefix(line, "@@"):
			color = p.Merged
		case strings.HasPrefix(line, "+"):
			color = p.Success
		case strings.HasPrefix(line, "-"):
			color = p.Error
		case strings.HasPrefix(line, "Binary"), strings.HasPrefix(line, "[Diff truncated"):
			color = p.Warning
		}
		line = ansi.Cut(line, d.horizontal, d.horizontal+max(1, width))
		visible = append(visible, colored(color).Render(line))
	}
	visible = append(visible, footer)
	return strings.Join(visible, "\n")
}
