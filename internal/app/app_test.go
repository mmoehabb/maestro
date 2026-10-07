package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestTasksSurviveRestartAndShareProjectLock(t *testing.T) {
	ctx := context.Background()
	dir := testutil.Repo(t)
	data := t.TempDir()
	paths := config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data}
	s, err := Open(ctx, dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Store.Close() })
	first, err := s.Create(ctx, core.NewTask{Title: "Fix auth!", Prompt: "repair login"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Slug != "fix-auth" || first.Branch != "fix-auth" || first.Agent != "codex" || first.Prompt != "repair login" {
		t.Fatalf("bad task: %+v", first)
	}
	if _, err := s.Create(ctx, core.NewTask{Title: "Fix auth"}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := s.Create(ctx, core.NewTask{Title: "Unknown", Agent: "missing"}); err == nil {
		t.Fatal("unknown agent accepted")
	}
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	// Opening from a managed worktree must restore the main project's tasks.
	s, err = Open(ctx, first.Worktree, paths)
	if err != nil {
		t.Fatal(err)
	}
	lock := flock.New(s.LockPath)
	ok, err := lock.TryLock()
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	tasks, err := s.List(ctx, false)
	if err != nil || len(tasks) != 1 || tasks[0].ID != first.ID {
		t.Fatalf("restore under lock: %+v %v", tasks, err)
	}
	if _, err := s.Create(ctx, core.NewTask{Title: "Locked"}); err == nil {
		t.Fatal("mutation ignored lock")
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, core.NewTask{Title: "Second"})
	if err != nil || second.TabOrder != 1 {
		t.Fatalf("second task: %+v %v", second, err)
	}
}
