package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestTaskLifecycleCommands(t *testing.T) {
	t.Chdir(testutil.Repo(t))
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	for _, title := range []string{"one", "two"} {
		if _, err := run(t, "new", title); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	s, err := app.Open(ctx, ".", config.DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	task, err := s.Find(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Store.StartSession(ctx, task, "native", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.SaveScrollback(ctx, session.ID, "saved history"); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.PrepareHandoff(ctx, task.ID, session.ID, "agy", "handoff"); err != nil {
		t.Fatal(err)
	}
	task.Agent = "agy"
	if _, err = s.Store.StartSession(ctx, task, "native", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, "rm", "one"); err == nil {
		t.Fatal("deleted unarchived task")
	}
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"archive", "reopen", "rm"} {
		if _, err = run(t, command, "one"); err == nil || !strings.Contains(err.Error(), "busy") {
			t.Fatalf("lock: %s: %v", command, err)
		}
	}
	if _, err = run(t, "tabs", "--all"); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = run(t, "archive", "one"); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, "tabs")
	if err != nil || strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Fatal(out, err)
	}
	out, err = run(t, "tabs", "--archived")
	if err != nil || !strings.Contains(out, "one") || strings.Contains(out, "two") {
		t.Fatal(out, err)
	}
	out, err = run(t, "history", "one")
	if err != nil || !strings.Contains(out, "saved history") {
		t.Fatal(out, err)
	}
	if _, err = run(t, "reopen", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, "delete", "one"); err == nil {
		t.Fatal("deleted reopened task")
	}
	if _, err = run(t, "archive", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, "delete", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Find(ctx, "one"); err == nil {
		t.Fatal("deleted task remains")
	}
	if _, err = os.Stat(task.Worktree); err != nil {
		t.Fatal("worktree not retained", err)
	}
	out, err = run(t, "tabs", "--all")
	if err != nil || strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Fatal(out, err)
	}
	out, err = run(t, "tabs", "--archived", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatal(out, err)
	}
	if _, err = run(t, "tabs", "--all", "--archived"); err == nil {
		t.Fatal("conflicting filters accepted")
	}
}
