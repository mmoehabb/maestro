package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestPersistenceConstraintsAndHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested # data", "maestro.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p, err := s.EnsureProject(ctx, Project{Root: "/test/repo", DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.EnsureProject(ctx, Project{Root: p.Root, DefaultBranch: "main", Remote: "updated"})
	if err != nil || p.ID != p2.ID {
		t.Fatalf("project upsert: %+v %v", p2, err)
	}
	input := Task{ProjectID: p.ID, Slug: "one", Title: "One", Agent: "fake", Branch: "maestro/one", BaseBranch: "main", Worktree: "/test/worktree"}
	first, err := s.CreateTask(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, input); err == nil {
		t.Fatal("duplicate slug accepted")
	}
	input.Slug = "two"
	second, err := s.CreateTask(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.TabOrder != 0 || second.TabOrder != 1 {
		t.Fatal("tab order not assigned")
	}
	input.ProjectID = -1
	if _, err := s.CreateTask(ctx, input); err == nil {
		t.Fatal("foreign keys not enforced")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE tasks SET lifecycle='archived' WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("expected two retained creation events, got %d", events)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Tasks(ctx, p.ID, false)
	if err != nil || len(tasks) != 1 || tasks[0].ID != second.ID {
		t.Fatalf("active tasks: %+v %v", tasks, err)
	}
	tasks, err = s.Tasks(ctx, p.ID, true)
	if err != nil || len(tasks) != 2 || tasks[0].Title != "One" {
		t.Fatalf("restored tasks: %+v %v", tasks, err)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL not enabled: %s %v", mode, err)
	}
}

func TestConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maestro.db")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			s, err := Open(context.Background(), path)
			if err != nil {
				t.Error(err)
				return
			}
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
