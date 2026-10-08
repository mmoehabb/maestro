package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type paletteView struct {
	input    textinput.Model
	selected int
}
type paletteEntry struct {
	label, key, disabled string
	taskID               int64
	score                int
}

func (m *Model) openPalette() tea.Cmd {
	input := textinput.New()
	input.Placeholder = "Search actions or tasks…"
	input.CharLimit = 256
	input.SetVirtualCursor(true)
	m.palette = &paletteView{input: input}
	return inputCommand(&m.palette.input, m.palette.input.Focus())
}

// fuzzyScore prefers contiguous matches and word starts; ties retain the
// documented action order followed by the current task order.
func fuzzyScore(query, value string) (int, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	v := []rune(strings.ToLower(value))
	score, pos, last := 0, 0, -2
	for _, want := range q {
		found := false
		for pos < len(v) {
			i := pos
			pos++
			if v[i] != want {
				continue
			}
			score += 10
			if i == last+1 {
				score += 8
			}
			if i == 0 || unicode.IsSpace(v[i-1]) || v[i-1] == '-' {
				score += 5
			}
			score -= i / 8
			last = i
			found = true
			break
		}
		if !found {
			return 0, false
		}
	}
	return score, true
}

func (m *Model) paletteEntries() []paletteEntry {
	entries := []paletteEntry{}
	for _, a := range actions {
		if a.key == ":" {
			continue
		}
		entries = append(entries, paletteEntry{label: a.label, key: a.key, disabled: m.disabled(a)})
	}
	for _, t := range m.tabs {
		entries = append(entries, paletteEntry{label: "Task: " + t.task.Title + " · " + t.task.Slug + " · " + t.task.Agent, taskID: t.task.ID})
	}
	matched := entries[:0]
	for _, e := range entries {
		if score, ok := fuzzyScore(m.palette.input.Value(), e.label); ok {
			e.score = score
			matched = append(matched, e)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].score > matched[j].score })
	return matched
}

func (m *Model) paletteKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.palette
	entries := m.paletteEntries()
	switch msg.String() {
	case "esc":
		m.palette = nil
		return nil
	case "up", "ctrl+p":
		p.selected = max(0, p.selected-1)
		return nil
	case "down", "ctrl+n":
		p.selected = min(max(0, len(entries)-1), p.selected+1)
		return nil
	case "enter":
		if len(entries) == 0 {
			return nil
		}
		e := entries[min(p.selected, len(entries)-1)]
		if e.disabled != "" {
			m.notify(e.disabled)
			return nil
		}
		m.palette = nil
		if e.taskID != 0 {
			for i, t := range m.tabs {
				if t.task.ID == e.taskID {
					m.selectTab(i)
					break
				}
			}
			return nil
		}
		return m.dispatch(e.key)
	}
	return m.paletteInput(msg)
}

func (m *Model) paletteInput(msg tea.Msg) tea.Cmd {
	p := m.palette
	old := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != old {
		p.selected = 0
	}
	return inputCommand(&p.input, cmd)
}

func (m *Model) paletteView(width, height int) string {
	p := m.palette
	p.input.SetWidth(max(1, width-4))
	p.input.SetStyles(inputStyles(m.paletteColors()))
	entries := m.paletteEntries()
	p.selected = min(p.selected, max(0, len(entries)-1))
	rows := max(1, height-5)
	start := max(0, p.selected-rows+1)
	lines := []string{m.accentStyle().Bold(true).Render("Command palette"), p.input.View(), ""}
	if len(entries) == 0 {
		lines = append(lines, "No matching actions or tasks.")
	}
	for i := start; i < min(len(entries), start+rows); i++ {
		e := entries[i]
		mark := "  "
		if i == p.selected {
			mark = "> "
		}
		text := e.label
		if e.key != "" {
			text += "  [" + m.prefix + " " + e.key + "]"
		}
		if e.disabled != "" {
			text += " — " + e.disabled
		}
		text = ansi.Truncate(mark+text, width, "…")
		if i == p.selected {
			text = m.accentStyle().Bold(true).Render(text)
		} else if e.disabled != "" {
			text = m.mutedStyle().Render(text)
		}
		lines = append(lines, text)
	}
	lines = append(lines, "", fmt.Sprintf("%d matches · ↑/↓ select · Enter run · Esc cancel", len(entries)))
	return strings.Join(lines, "\n")
}
