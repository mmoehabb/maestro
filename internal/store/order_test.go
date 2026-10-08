package store

import (
	"context"
	"path/filepath"
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
