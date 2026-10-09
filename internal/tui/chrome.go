package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/term"
)

// Geometry is shared by rendering, pane sizing, cursor placement and hit tests.
// Short terminals use compact chrome; taller ones gain breathing room.
type shellGeometry struct {
	padding, header, tabs, tabLine, meta, rule, status, footer int
	pane                                                       bounds
}

func (m *Model) geometry() shellGeometry {
	w, h := max(1, m.width), max(1, m.height)
	pad := 1
	if w >= 80 {
		pad = 2
	}
	if w >= 120 {
		pad = 3
	}
	if w < 20 {
		pad = 0
	}
	g := shellGeometry{padding: pad, header: 0, tabs: 1, tabLine: -1, meta: -1, rule: -1, status: h - 2, footer: h - 1}
	top, bottom := 2, 2
	if h >= 24 && w >= 60 {
		g.tabs = 2
		g.tabLine = 3
		g.meta = 4
		top = 6
		bottom = 3
		g.rule = h - 3
	}
	if h >= 32 && w >= 80 {
		g.header = 1
		g.tabs = 3
		g.tabLine = 4
		g.meta = 6
		top = 8
		bottom = 5
		g.rule = h - 4
		g.status = h - 3
	}
	side := m.sidebarWidth()
	g.pane = bounds{side + pad, top, max(1, w-side-2*pad), max(1, h-top-bottom)}
	return g
}

type chromePalette struct{ bar, selected, line string }

func (m *Model) chromeColors() chromePalette {
	p := m.paletteColors()
	switch p.Background {
	case "#242b26":
		return chromePalette{"#202620", "#30392f", "#435045"}
	case "#f5f2e9":
		return chromePalette{"#e9ecdf", "#dce2cf", "#c6cbbd"}
	default:
		return chromePalette{mixColor(p.Background, p.Foreground, .025), mixColor(p.Background, p.Foreground, .10), mixColor(p.Background, p.Foreground, .22)}
	}
}

func mixColor(a, b string, f float64) string {
	av, _ := strconv.ParseUint(strings.TrimPrefix(a, "#"), 16, 32)
	bv, _ := strconv.ParseUint(strings.TrimPrefix(b, "#"), 16, 32)
	channels := [3]int{}
	for i, shift := range []uint{16, 8, 0} {
		x, y := float64((av>>shift)&255), float64((bv>>shift)&255)
		channels[i] = int(x + (y-x)*f)
	}
	return fmt.Sprintf("#%02x%02x%02x", channels[0], channels[1], channels[2])
}

func backgroundSequence(color string) string {
	value, _ := strconv.ParseUint(strings.TrimPrefix(color, "#"), 16, 32)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", value>>16, (value>>8)&255, value&255)
}

func (m *Model) separator() string {
	if m.cfg.Icons == "ascii" {
		return "|"
	}
	return "│"
}

func (m *Model) ruleChar() string {
	if m.cfg.Icons == "ascii" {
		return "-"
	}
	return "─"
}

func rowPair(left, right string, width int) string {
	width = max(1, width)
	if right == "" {
		return ansi.Truncate(left, width, "…")
	}
	right = ansi.Truncate(right, width, "…")
	room := width - ansi.StringWidth(right) - 2
	if room < 1 {
		return right
	}
	left = ansi.Truncate(left, room, "…")
	return left + strings.Repeat(" ", max(2, width-ansi.StringWidth(left)-ansi.StringWidth(right))) + right
}

func (m *Model) shellHeader(width int) string {
	p := m.paletteColors()
	mark := "♪"
	if m.cfg.Icons == "ascii" {
		mark = "*"
	}
	left := colored(p.Accent).Render(mark) + "  " + colored(p.Foreground).Bold(true).Render("maestro") + m.mutedStyle().Render(" / "+filepath.Base(m.service.Repo.Root))
	right := ""
	if width >= 65 {
		right = m.mutedStyle().Render(fmt.Sprintf("%d sessions", len(m.tabs)))
	}
	return rowPair(left, right, width)
}

func (m *Model) sessionMeta(width int) string {
	if len(m.tabs) == 0 {
		return m.mutedStyle().Render("YOUR WORKSPACE")
	}
	t := m.tabs[m.active]
	p := m.paletteColors()
	label, color := "Starting", p.Muted
	switch t.state {
	case term.Working:
		label, color = "Working", p.Accent
	case term.Done:
		label, color = "Done", p.Success
	case term.NeedsInput:
		label, color = "Needs input", p.Warning
	case term.Exited:
		label = "Exited"
	case term.Crashed:
		label, color = "Crashed", p.Error
	}
	left := colored(p.Foreground).Render(strings.ToUpper(t.task.Agent)) + m.mutedStyle().Render(" / "+taskTitle(t.task))
	return rowPair(left, colored(color).Render(icon(t.state, m.frame, m.cfg.Icons)+" "+label), width)
}

func (m *Model) shellStatus(width int) string {
	if len(m.tabs) == 0 {
		return m.mutedStyle().Render("Each task gets its own branch and worktree.")
	}
	t := m.tabs[m.active]
	p := m.paletteColors()
	left := m.mutedStyle().Render(t.task.Branch)
	if t.task.PRNumber != 0 {
		left += "  " + lifecycleBadgeWithPalette(t.task, m.cfg.Icons, p)
		if width >= 100 {
			left += m.mutedStyle().Render("CI " + t.task.CIState + " · review " + t.task.ReviewState)
		}
	} else if t.task.Lifecycle != "active" {
		left += "  " + m.mutedStyle().Render(t.task.Lifecycle)
	}
	if t.status.Ahead > 0 || t.status.Behind > 0 {
		left += m.mutedStyle().Render(fmt.Sprintf("  ↑%d ↓%d", t.status.Ahead, t.status.Behind))
	}
	right := colored(p.Success).Render(fmt.Sprintf("+%d", t.status.Added)) + " " + colored(p.Accent).Render(fmt.Sprintf("−%d", t.status.Deleted)) + m.mutedStyle().Render(fmt.Sprintf(" · %d files", t.status.Dirty))
	return rowPair(left, right, width)
}

func (m *Model) shellFooter(width int) string {
	p := m.paletteColors()
	hint := func(key, label string) string {
		return colored(p.Foreground).Render(key) + " " + m.mutedStyle().Render(label)
	}
	if width < 65 {
		return hint(m.prefix+" :", "actions") + "   " + hint(m.prefix+" ?", "help")
	}
	text := hint("alt 1–9", "switch tabs") + "   " + hint(m.prefix+" a", "switch agent") + "   " + hint(m.prefix+" ?", "help")
	if width >= 105 {
		text += "   " + hint(m.prefix+" :", "actions")
	}
	return text
}

func (m *Model) composeShell(body []string, footer string) string {
	g := m.geometry()
	w, h := max(1, m.width), max(1, m.height)
	p := m.paletteColors()
	lines := make([]string, h)
	put := func(y, x int, text string) {
		if y >= 0 && y < h {
			lines[y] = strings.Repeat(" ", max(0, x)) + fit(text, max(1, w-x))
		}
	}
	put(g.header, g.padding, m.shellHeader(w-2*g.padding))
	if m.sidebarWidth() > 0 {
		put(g.tabs, g.padding, m.mutedStyle().Render("TASKS  /  drag to reorder"))
		if g.tabLine >= 0 {
			put(g.tabLine, g.padding, colored(m.chromeColors().line).Render(strings.Repeat(m.ruleChar(), max(1, w-2*g.padding))))
		}
	} else {
		put(g.tabs, 0, m.renderTabs())
		if g.tabLine >= 0 {
			put(g.tabLine, 0, m.tabRule())
		}
	}
	if g.meta >= 0 {
		put(g.meta, g.pane.x, m.sessionMeta(g.pane.w))
	}
	x := g.pane.x
	if m.sidebarWidth() > 0 {
		body = m.sidebarLines(body, g.pane.h)
		x = 0
	}
	for i, line := range body {
		put(g.pane.y+i, x, line)
	}
	if g.rule >= 0 {
		put(g.rule, g.pane.x, colored(m.chromeColors().line).Render(strings.Repeat(m.ruleChar(), g.pane.w)))
	}
	status := m.shellStatus(g.pane.w)
	if m.scroll > 0 {
		status = m.mutedStyle().Render("Scroll  ↑/↓ or j/k · PgUp/PgDn · y copy · q return")
	}
	put(g.status, g.pane.x, status)
	// Footer spans the terminal so the shortcut band remains a stable anchor.
	foot := strings.Repeat(" ", g.padding) + fit(footer, max(1, w-2*g.padding))
	foot += strings.Repeat(" ", max(0, w-ansi.StringWidth(foot)))
	foot = strings.ReplaceAll(foot, "\x1b[m", "\x1b[m"+backgroundSequence(m.chromeColors().bar))
	put(g.footer, 0, colored(p.Foreground).Background(lipgloss.Color(m.chromeColors().bar)).Render(foot))
	for i := range lines {
		lines[i] = colored(p.Foreground).Render(lines[i])
	}
	return strings.Join(lines, "\n")
}
