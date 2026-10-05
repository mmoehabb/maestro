package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"

	"github.com/mmoehabb/maestro/internal/config"
)

type LaunchSpec struct {
	Dir, SessionID, Prompt string
	Env                    []string
	NewSession             bool
}

// Generic renders argv directly; prompts never pass through a shell.
type Generic struct {
	Name   string
	Config config.Agent
}

func (g Generic) ID() string { return g.Name }

func (g Generic) Detect() (string, error) { return exec.LookPath(g.Config.Cmd) }

func (g Generic) ValidatePrompt(prompt string) error {
	if prompt != "" && g.Config.ManualPrompt {
		return fmt.Errorf("agent %s requires manual prompt entry; omit the initial prompt and type it in the agent pane", g.Name)
	}
	return nil
}

func (g Generic) Command(ctx context.Context, spec LaunchSpec) (*exec.Cmd, error) {
	if err := g.ValidatePrompt(spec.Prompt); err != nil {
		return nil, err
	}
	args := g.Config.New
	mode := "new"
	if spec.SessionID != "" && !spec.NewSession {
		if len(g.Config.Resume) == 0 {
			return nil, fmt.Errorf("agent %s does not support resume", g.Name)
		}
		args = g.Config.Resume
		mode = "resume"
	}
	var rendered []string
	delivered := spec.Prompt == ""
	for _, arg := range args {
		t, err := template.New("arg").Option("missingkey=error").Parse(arg)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", g.Name, err)
		}
		var out bytes.Buffer
		if err := t.Execute(&out, spec); err != nil {
			return nil, fmt.Errorf("agent %s: %w", g.Name, err)
		}
		if out.Len() > 0 {
			rendered = append(rendered, out.String())
			delivered = delivered || strings.Contains(out.String(), spec.Prompt)
		}
	}
	// Check rendered arguments, since a conditional template can mention Prompt
	// without actually passing it for the selected launch mode.
	if !delivered {
		return nil, fmt.Errorf("agent %s %s template cannot deliver the prompt; include {{.Prompt}} or configure manual_prompt = true", g.Name, mode)
	}
	cmd := exec.CommandContext(ctx, g.Config.Cmd, rendered...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	return cmd, nil
}
