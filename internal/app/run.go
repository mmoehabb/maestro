package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	xterm "github.com/charmbracelet/x/term"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/tui"
)

func Run(ctx context.Context, dir, focus string, output io.Writer) error {
	return RunSwitch(ctx, dir, focus, "", output)
}

func RunSwitch(ctx context.Context, dir, focus, target string, output io.Writer) (err error) {
	if !xterm.IsTerminal(os.Stdin.Fd()) {
		return errors.New("the TUI needs an interactive terminal; use maestro ls --json for scripts")
	}
	s, err := Open(ctx, dir, config.DefaultPaths())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Store.Close()) }()
	if focus != "" {
		tasks, e := s.List(ctx, false)
		if e != nil {
			return e
		}
		found := false
		for _, t := range tasks {
			if t.Slug == focus {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("task %q not found", focus)
		}
	}
	r, err := s.OpenRuntime()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	model := tui.New(s, r, focus)
	model.SetSwitchAgent(target)
	_, err = tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(output)).Run()
	return err
}
