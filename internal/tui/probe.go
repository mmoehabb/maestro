package tui

import (
	"context"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
)

type (
	probe     struct{ enhanced bool }
	probeDone struct{}
)

func (p *probe) Init() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(time.Time) tea.Msg { return probeDone{} })
}

func (p *probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyboardEnhancementsMsg:
		p.enhanced = msg.SupportsKeyDisambiguation()
		return p, tea.Quit
	case probeDone:
		return p, tea.Quit
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return p, tea.Quit
		}
	}
	return p, nil
}

func (p *probe) View() tea.View {
	v := tea.NewView("Checking keyboard support…")
	v.KeyboardEnhancements.ReportEventTypes = true
	return v
}

func ProbeKeyboard(ctx context.Context, input io.Reader, output io.Writer) (bool, error) {
	p := &probe{}
	_, err := tea.NewProgram(p, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output)).Run()
	return p.enhanced, err
}
