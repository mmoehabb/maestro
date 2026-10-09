package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestTitleEditPreservesTaskIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	project, err := s.EnsureProject(ctx, Project{Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.CreateTask(ctx, Task{ProjectID: project.ID, Slug: "stable", Title: "Old", Branch: "feature", Worktree: "/worktree", BaseBranch: "main", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{" ", "hello\nworld", "bad\x1btitle", strings.Repeat("x", 513), string([]byte{0xff})} {
		if err = s.SetTitle(ctx, original.ID, invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if err = s.SetTitle(ctx, original.ID, "  新しい名前  "); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTitle(ctx, original.ID, "新しい名前"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Tasks(ctx, project.ID, true)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	got := tasks[0]
	if got.Title != "新しい名前" || got.ID != original.ID || got.Slug != original.Slug || got.Branch != original.Branch || got.Worktree != original.Worktree || got.Agent != original.Agent {
		t.Fatal(got)
	}
	h, err := s.History(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range h.Events {
		if event.Kind == "title_updated" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("non-idempotent rename", count)
	}
}
