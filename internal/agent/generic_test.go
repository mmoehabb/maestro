package agent

import (
	"context"
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
