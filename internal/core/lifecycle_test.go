package core_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestArchiveRunningTask(t *testing.T) {
	t.Setenv("MAESTRO_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	ctx := context.Background()
	s, err := app.Open(ctx, testutil.Repo(t), config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	s.Config.Agents["fake"] = config.Agent{Cmd: exe, New: []string{"-test.run=TestAgentProcess", "--", "new", "{{.SessionID}}"}, GenerateSessionID: true}
	task, err := s.Create(ctx, core.NewTask{Title: "archive", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Archive(ctx, task); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("agent still running")
	}
	tasks, err := s.List(ctx, false)
	if err != nil || len(tasks) != 0 {
		t.Fatal(tasks, err)
	}
	h, err := s.History(ctx, task.Slug)
	if err != nil || len(h.Sessions) != 1 || h.Sessions[0].EndedAt == nil || len(h.Turns) == 0 {
		t.Fatal(h, err)
	}
	if _, err = r.Start(task, 80, 24, false); err == nil {
		t.Fatal("stale task restarted archived agent")
	}
	if _, err = r.Switch(ctx, task, "fake", 80, 24, true); err == nil {
		t.Fatal("archived task switched")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Reopen(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.List(ctx, false)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
}
