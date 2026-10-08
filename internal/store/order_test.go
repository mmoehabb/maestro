package store

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func TestReorderAtomicAndPersistent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "order.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	p, err := s.EnsureProject(ctx, Project{Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, slug := range []string{"one", "two", "three"} {
		task, e := s.CreateTask(ctx, Task{ProjectID: p.ID, Slug: slug})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, task.ID)
	}
	check := func(want []int64) {
		t.Helper()
		tasks, e := s.Tasks(ctx, p.ID, false)
		if e != nil {
			t.Fatal(e)
		}
		if len(tasks) != len(want) {
			t.Fatal(tasks)
		}
		for i, id := range want {
			if tasks[i].ID != id {
				t.Fatalf("order: %+v", tasks)
			}
		}
	}
	reordered := []int64{ids[2], ids[0], ids[1]}
	if err = s.ReorderTasks(ctx, p.ID, reordered); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	check(reordered)
	for _, bad := range [][]int64{{ids[0], ids[0], ids[1]}, {ids[0], ids[1], 999}, {ids[0]}} {
		if err = s.ReorderTasks(ctx, p.ID, bad); err == nil {
			t.Fatal("invalid order accepted")
		}
		check(reordered)
	}
	if err = s.SetArchived(ctx, ids[0], true); err != nil {
		t.Fatal(err)
	}
	if err = s.ReorderTasks(ctx, p.ID, reordered); err == nil {
		t.Fatal("stale archive snapshot accepted")
	}
	if err = s.ReorderTasks(ctx, p.ID, []int64{ids[1], ids[2]}); err != nil {
		t.Fatal(err)
	}
	check([]int64{ids[1], ids[2]})
}

func TestReopenAppendsAfterReordering(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "order.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	p, err := s.EnsureProject(ctx, Project{Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	var tasks []Task
	for _, slug := range []string{"a", "b", "c"} {
		task, e := s.CreateTask(ctx, Task{ProjectID: p.ID, Slug: slug})
		if e != nil {
			t.Fatal(e)
		}
		tasks = append(tasks, task)
	}
	for cycle := range 3 {
		if err = s.SetArchived(ctx, tasks[0].ID, true); err != nil {
			t.Fatal(err)
		}
		if err = s.ReorderTasks(ctx, p.ID, []int64{tasks[2].ID, tasks[1].ID}); err != nil {
			t.Fatal(err)
		}
		tasks[0].Lifecycle = "active"
		switch cycle {
		case 0:
			tasks[0], err = s.SaveReopened(ctx, tasks[0])
			if err != nil || tasks[0].TabOrder != 2 {
				t.Fatal(tasks[0], err)
			}
		case 1:
			err = s.SaveWorkflow(ctx, tasks[0], "reopened")
		default:
			err = s.SetArchived(ctx, tasks[0].ID, false)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		saved, e := s.Tasks(ctx, p.ID, false)
		if e != nil {
			t.Fatal(e)
		}
		got := []int64{}
		for _, task := range saved {
			got = append(got, task.ID)
		}
		if !slices.Equal(got, []int64{tasks[2].ID, tasks[1].ID, tasks[0].ID}) {
			t.Fatal("reopen order changed after restart", got)
		}
	}
}

func TestConcurrentReopenRejectsStaleOrder(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject(ctx, Project{Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateTask(ctx, Task{ProjectID: p.ID, Slug: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTask(ctx, Task{ProjectID: p.ID, Slug: "b"})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if err = s.SetArchived(ctx, a.ID, true); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var reorderErr, reopenErr error
		wg.Add(2)
		go func() { defer wg.Done(); reorderErr = s.ReorderTasks(ctx, p.ID, []int64{b.ID}) }()
		go func() { defer wg.Done(); reopenErr = s.SetArchived(ctx, a.ID, false) }()
		wg.Wait()
		if reopenErr != nil {
			t.Fatal(reopenErr)
		}
		if reorderErr != nil && reorderErr.Error() != "task list changed; retry reordering" {
			t.Fatal(reorderErr)
		}
		saved, e := s.Tasks(ctx, p.ID, false)
		if e != nil || len(saved) != 2 || saved[0].ID != b.ID || saved[1].ID != a.ID || saved[0].TabOrder >= saved[1].TabOrder {
			t.Fatal("non-atomic order", saved, e)
		}
	}
}
