package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mmoehabb/maestro/internal/store"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/term"
)

type bounds struct{ x, y, w, h int }

func (b bounds) contains(x, y int) bool { return x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h }
func (m *Model) sidebarWidth() int {
	if m.sidebar && m.width >= 72 {
		return min(32, m.width/3)
	}
	return 0
}

func (m *Model) paneBounds() bounds { return m.geometry().pane }

func (m *Model) resizePanes() {
	cols, rows := m.size()
	for i := range m.tabs {
		if m.tabs[i].pane != nil {
			if err := m.tabs[i].pane.Resize(cols, rows); err != nil {
				m.notify(err.Error())
			}
		}
	}
}

func (m *Model) modal() bool {
	return m.themes != nil || m.palette != nil || m.diff != nil || m.forgeUI != nil || m.notes != nil || m.dialog != nil || m.switcher != nil || m.history != nil || m.help
}
func (m *Model) sidebarStart() int { _, rows := m.size(); return max(0, m.active-rows+1) }
func (m *Model) taskLabel(i int) string {
	t := m.tabs[i]
	mark := icon(t.state, m.frame, m.cfg.Icons)
	p := m.paletteColors()
	color := p.Muted
	switch t.state {
	case term.Working:
		color = p.Accent
	case term.Done:
		color = p.Success
	case term.NeedsInput:
		color = p.Warning
	case term.Crashed:
		color = p.Error
	}
	mark = colored(color).Render(mark)
	label := fmt.Sprintf(" %s %s%s  %s ", mark, lifecycleBadgeWithPalette(t.task, m.cfg.Icons, m.paletteColors()), taskTitle(t.task), m.mutedStyle().Render(t.task.Agent))
	return label
}

func (m *Model) tabLayout() ([]bounds, []string) {
	g := m.geometry()
	available := max(1, m.width-2*g.padding)
	regions := make([]bounds, len(m.tabs))
	labels := make([]string, len(m.tabs))
	widths := make([]int, len(m.tabs))
	total := 0
	for i := range m.tabs {
		labels[i] = m.taskLabel(i)
		widths[i] = min(ansi.StringWidth(labels[i])+4, max(1, available-4))
		total += widths[i]
	}
	if len(m.tabs) > 0 && total <= available {
		extra := (available - total) / len(m.tabs)
		for i := range widths {
			widths[i] += extra
		}
		widths[len(widths)-1] += available - total - extra*len(widths)
	}
	start, used := 0, 0
	for i := 0; i <= m.active && i < len(widths); i++ {
		used += widths[i]
		for used > available-4 && start < i && total > available {
			used -= widths[start]
			start++
		}
	}
	x := g.padding
	if start > 0 {
		x += 2
	}
	for i := start; i < len(m.tabs); i++ {
		if x+widths[i] > m.width-g.padding && i > m.active {
			break
		}
		regions[i] = bounds{x, g.tabs, widths[i], 1}
		x += widths[i]
	}
	return regions, labels
}

func (m *Model) renderTabs() string {
	regions, labels := m.tabLayout()
	var b strings.Builder
	p := m.paletteColors()
	c := m.chromeColors()
	x := 0
	for i, r := range regions {
		if r.w == 0 {
			continue
		}
		gap := r.x - x
		if gap > 0 {
			marker := strings.Repeat(" ", gap)
			if x == 0 && i > 0 && gap >= 2 {
				marker = strings.Repeat(" ", gap-2) + "‹ "
			}
			b.WriteString(m.mutedStyle().Render(marker))
		}
		background := c.bar
		if i == m.active {
			background = c.selected
		}
		label := ansi.Truncate(strings.TrimSpace(labels[i]), max(1, r.w-3), "…")
		space := max(0, r.w-1-ansi.StringWidth(label))
		left := space / 2
		label = strings.Repeat(" ", left) + label + strings.Repeat(" ", space-left)
		// Reapply the surface after an inner badge's reset sequence.
		label = strings.ReplaceAll(label, "\x1b[m", "\x1b[m"+backgroundSequence(background))
		style := colored(p.Foreground).Background(lipgloss.Color(background))
		if i == m.active {
			style = style.Bold(true)
		}
		if time.Now().Before(m.attention[m.tabs[i].task.ID]) {
			style = style.Foreground(lipgloss.Color(p.Warning))
		}
		b.WriteString(style.Render(label))
		b.WriteString(colored(c.line).Render(m.separator()))
		x = r.x + r.w
		if i+1 < len(regions) && regions[i+1].w == 0 {
			b.WriteString("›")
			x++
		}
	}
	return fit(b.String(), m.width)
}

func (m *Model) tabRule() string {
	g := m.geometry()
	regions, _ := m.tabLayout()
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", g.padding))
	x := g.padding
	for i, r := range regions {
		if r.w == 0 {
			continue
		}
		if r.x > x {
			b.WriteString(colored(m.chromeColors().line).Render(strings.Repeat(m.ruleChar(), r.x-x)))
		}
		color := m.chromeColors().line
		if i == m.active {
			color = m.paletteColors().Accent
		}
		b.WriteString(colored(color).Render(strings.Repeat(m.ruleChar(), r.w)))
		x = r.x + r.w
	}
	if x < m.width-g.padding {
		b.WriteString(colored(m.chromeColors().line).Render(strings.Repeat(m.ruleChar(), m.width-g.padding-x)))
	}
	return fit(b.String(), m.width)
}

func (m *Model) sidebarLines(body []string, rows int) []string {
	width, start := m.sidebarWidth(), m.sidebarStart()
	for row := 0; row < rows; row++ {
		label := ""
		i := start + row
		if i < len(m.tabs) {
			marker := " "
			if i == m.active {
				marker = ">"
			}
			if row == 0 && start > 0 {
				marker = "↑"
			}
			if row == rows-1 && i < len(m.tabs)-1 {
				marker = "↓"
			}
			if m.cfg.Icons == "ascii" {
				if marker == "↑" {
					marker = "^"
				}
				if marker == "↓" {
					marker = "v"
				}
			}
			label = marker + m.taskLabel(i)
		}
		label = ansi.Truncate(label, width-1, "…")
		label += strings.Repeat(" ", max(0, width-1-ansi.StringWidth(label)))
		switch {
		case i == m.active:
			label = m.accentStyle().Bold(true).Render(label)
		case i < len(m.tabs) && time.Now().Before(m.attention[m.tabs[i].task.ID]):
			label = colored(m.paletteColors().Warning).Render(label)
		default:
			label = m.mutedStyle().Render(label)
		}
		separator := "│"
		if m.cfg.Icons == "ascii" {
			separator = "|"
		}
		body[row] = label + m.mutedStyle().Render(separator) + strings.Repeat(" ", m.geometry().padding) + body[row]
	}
	return body
}

func (m *Model) taskAt(x, y int) int {
	if side := m.sidebarWidth(); side > 0 {
		b := m.paneBounds()
		if x >= 0 && x < side && y >= b.y && y < b.y+b.h {
			i := m.sidebarStart() + y - b.y
			if i < len(m.tabs) {
				return i
			}
		}
		return -1
	}
	regions, _ := m.tabLayout()
	for i, b := range regions {
		if b.w > 0 && b.contains(x, y) {
			return i
		}
	}
	return -1
}

type orderedMsg struct {
	ids []int64
	err error
}

func (m *Model) reorder(from, to int) tea.Cmd {
	if m.orderPending || from == to || from < 0 || to < 0 || from >= len(m.tabs) || to >= len(m.tabs) {
		return nil
	}
	ids := make([]int64, len(m.tabs))
	for i, t := range m.tabs {
		ids[i] = t.task.ID
	}
	id := ids[from]
	if from < to {
		copy(ids[from:to], ids[from+1:to+1])
	} else {
		copy(ids[to+1:from+1], ids[to:from])
	}
	ids[to] = id
	m.orderPending = true
	return func() tea.Msg { return orderedMsg{ids, m.runtime.ReorderTasks(context.Background(), ids)} }
}

func (m *Model) applyOrder(ids []int64) {
	if len(m.tabs) == 0 {
		return
	}
	activeID := m.tabs[m.active].task.ID
	// Ignore removed tasks and append tasks created while the write was pending.
	ordered := make([]tab, 0, len(m.tabs))
	seen := map[int64]bool{}
	for _, id := range ids {
		for _, t := range m.tabs {
			if t.task.ID == id {
				ordered = append(ordered, t)
				seen[id] = true
				break
			}
		}
	}
	for _, t := range m.tabs {
		if !seen[t.task.ID] {
			ordered = append(ordered, t)
		}
	}
	m.tabs = ordered
	for i := range m.tabs {
		m.tabs[i].task.TabOrder = i
		if m.tabs[i].task.ID == activeID {
			m.active = i
		}
	}
}

func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	if m.modal() {
		m.dragID = 0
		m.dragMoved = false
		if m.switcher != nil && (mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown) {
			if mouse.Button == tea.MouseWheelUp {
				m.switcher.offset = max(0, m.switcher.offset-3)
			} else {
				m.switcher.offset += 3
			}
			m.switcher.scrolled = true
		}
		if m.diff != nil && (mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown) {
			if mouse.Button == tea.MouseWheelUp {
				m.diff.offset = max(0, m.diff.offset-3)
			} else {
				m.diff.offset += 3
			}
		}
		if m.help && (mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown) {
			if mouse.Button == tea.MouseWheelUp {
				m.helpOffset = max(0, m.helpOffset-3)
			} else {
				m.helpOffset += 3
			}
		}
		return nil
	}
	target := m.taskAt(mouse.X, mouse.Y)
	if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft && target >= 0 {
		m.selectTab(target)
		m.dragID = m.tabs[target].task.ID
		m.dragMoved = false
		return nil
	}
	if m.dragID != 0 {
		if _, ok := msg.(tea.MouseMotionMsg); ok {
			m.dragMoved = true
		}
		if _, ok := msg.(tea.MouseReleaseMsg); ok {
			id, moved := m.dragID, m.dragMoved
			m.dragID = 0
			m.dragMoved = false
			if moved && target >= 0 {
				for i, t := range m.tabs {
					if t.task.ID == id {
						return m.reorder(i, target)
					}
				}
			}
		}
		return nil
	}
	if target >= 0 {
		return nil
	}
	b := m.paneBounds()
	if !b.contains(mouse.X, mouse.Y) || len(m.tabs) == 0 || m.tabs[m.active].pending || m.tabs[m.active].pane == nil {
		return nil
	}
	pane := m.tabs[m.active].pane
	if mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown {
		state := pane.Snapshot(false)
		if m.scroll > 0 || !state.MouseReporting || state.State == "exited" || state.State == "crashed" {
			if mouse.Button == tea.MouseWheelUp {
				m.scroll += 3
			} else {
				m.scroll = max(0, m.scroll-3)
			}
			return nil
		}
	}
	if m.scroll == 0 {
		mouse.X -= b.x
		mouse.Y -= b.y
		_, release := msg.(tea.MouseReleaseMsg)
		_, motion := msg.(tea.MouseMotionMsg)
		pane.Mouse(uv.Mouse(mouse), release, motion)
	}
	return nil
}

func taskTitle(t store.Task) string {
	if t.Title != "" {
		return t.Title
	}
	return t.Slug
}
