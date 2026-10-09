package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/adrg/xdg"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestCheckpointCommandAndBranchRename(t *testing.T) {
	root := testutil.Repo(t)
	t.Chdir(root)
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	if _, err := run(t, "new", "portable"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "checkpoint", "portable"); err != nil || !strings.Contains(out, "Review and commit") {
		t.Fatal(out, err)
	}
	s, err := app.Open(context.Background(), root, config.DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	task, err := s.Find(context.Background(), "portable")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "add", ".maestro/.gitignore", ".maestro/tasks")
	testutil.Git(t, task.Worktree, "commit", "-m", "checkpoint")
	if _, err = run(t, "notes", "portable", "--set", "local notes"); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, "restore", "portable", "--replace"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "notes", "portable"); err != nil || strings.TrimSpace(out) != "" {
		t.Fatal("same checkpoint replacement did not restore notes", out, err)
	}
	testutil.Git(t, task.Worktree, "branch", "-m", "renamed")
	if _, err = run(t, "checkpoint", "portable"); err == nil {
		t.Fatal("silently adopted different branch")
	}
	if _, err = run(t, "checkpoint", "portable", "--branch", "renamed"); err != nil {
		t.Fatal(err)
	}
	task, err = s.Find(context.Background(), "portable")
	if err != nil || task.Branch != "renamed" {
		t.Fatal(task, err)
	}
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = run(t, "checkpoint", "portable"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("checkpoint bypassed project lock", err)
	}
	if _, err = run(t, "restore", "renamed"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("restore bypassed project lock", err)
	}
}
