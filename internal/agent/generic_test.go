package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mmoehabb/maestro/internal/config"
)

func TestCommands(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(config.Paths{ConfigFile: filepath.Join(dir, "absent"), DataDir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	prompt := "spaces; $(touch nope) 'quoted'\nsecond line"
	for _, tc := range []struct {
		name, session, prompt string
		args                  []string
	}{
		{"codex", "", "", []string{"codex"}},
		{"codex", "abc", prompt, []string{"codex", "resume", "abc", prompt}},
		{"agy", "", prompt, []string{"agy", "-i", prompt}},
		{"agy", "abc", "", []string{"agy", "--conversation", "abc"}},
		{"opencode", "abc", prompt, []string{"opencode", "--session", "abc", "--prompt", prompt}},
		{"opencode", "", "", []string{"opencode"}},
	} {
		g := Generic{Name: tc.name, Config: cfg.Agents[tc.name]}
		cmd, err := g.Command(context.Background(), LaunchSpec{Dir: dir, SessionID: tc.session, Prompt: tc.prompt, Env: []string{"MAESTRO_TEST=1"}})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cmd.Args, tc.args) || cmd.Dir != dir {
			t.Fatalf("got %v in %s, want %v", cmd.Args, cmd.Dir, tc.args)
		}
		if cmd.Env[len(cmd.Env)-1] != "MAESTRO_TEST=1" {
			t.Fatal("missing environment")
		}
	}
}

func TestBadTemplateAndUnsupportedResume(t *testing.T) {
	for _, cfg := range []config.Agent{{Cmd: "fake", Resume: []string{"{{.Missing}}"}}, {Cmd: "fake"}} {
		g := Generic{Name: "fake", Config: cfg}
		if _, err := g.Command(context.Background(), LaunchSpec{SessionID: "abc"}); err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestAdditionalAgentCommands(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(config.Paths{ConfigFile: filepath.Join(dir, "absent"), DataDir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	const id = "cd830a00-8b2c-4a82-bb4b-56ab89ed5d01"
	for _, tc := range []struct {
		name                string
		newArgs, resumeArgs []string
		promptFlag          string
		generated           bool
	}{
		{"claude", []string{"--session-id", id}, []string{"--resume", id}, "--", true},
		{"qoder", []string{"--session-id", id}, []string{"--resume", id}, "--prompt-interactive", true},
		{"kimi", nil, []string{"--session", id}, "--prompt", false},
		{"cursor-agent", []string{"--resume", id}, []string{"--resume", id}, "--", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preset, ok := cfg.Agents[tc.name]
			if !ok || preset.GenerateSessionID != tc.generated {
				t.Fatalf("incorrect preset: %+v", preset)
			}
			for _, fresh := range []bool{true, false} {
				for _, prompt := range []string{"", "--help; $(touch nope) 'quoted'\nsecond line"} {
					g := Generic{Name: tc.name, Config: preset}
					cmd, err := g.Command(context.Background(), LaunchSpec{Dir: dir, SessionID: id, NewSession: fresh, Prompt: prompt})
					if tc.name == "kimi" && prompt != "" {
						if err == nil {
							t.Fatal("Kimi must reject startup prompts instead of enabling non-interactive automatic approvals")
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					args := tc.resumeArgs
					if fresh {
						args = tc.newArgs
					}
					want := append([]string{tc.name}, args...)
					if prompt != "" {
						want = append(want, tc.promptFlag, prompt)
					}
					if !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != dir {
						t.Fatalf("fresh=%v: got %q, want %q", fresh, cmd.Args, want)
					}
				}
			}
		})
	}
}

func TestPromptDeliveryUsesSelectedRenderedTemplate(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		for _, tc := range []struct {
			name, arg string
			wantError bool
		}{
			{"missing", "--interactive", true},
			{"inactive conditional", "{{if not .Prompt}}{{.Prompt}}{{end}}", true},
			{"truncated", "{{printf \"%.4s\" .Prompt}}", true},
			{"direct", "{{.Prompt}}", false},
			{"flag", "--prompt={{.Prompt}}", false},
		} {
			t.Run(fmt.Sprintf("new=%t/%s", fresh, tc.name), func(t *testing.T) {
				cfg := config.Agent{Cmd: "fake", New: []string{"{{.Prompt}}"}, Resume: []string{"{{.Prompt}}"}}
				if fresh {
					cfg.New = []string{tc.arg}
				} else {
					cfg.Resume = []string{tc.arg}
				}
				_, err := (Generic{Name: "custom", Config: cfg}).Command(context.Background(), LaunchSpec{NewSession: fresh, SessionID: "saved", Prompt: "Read .maestro/handoff.md first."})
				if (err != nil) != tc.wantError {
					t.Fatalf("unexpected delivery result: %v", err)
				}
			})
		}
	}
	// Ordinary resume without a new prompt remains valid.
	_, err := (Generic{Name: "custom", Config: config.Agent{Cmd: "fake", Resume: []string{"--resume", "{{.SessionID}}"}}}).Command(context.Background(), LaunchSpec{SessionID: "saved"})
	if err != nil {
		t.Fatal(err)
	}
}
