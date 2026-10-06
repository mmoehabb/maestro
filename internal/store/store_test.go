package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
	var timeout int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("busy timeout not restored: %d %v", timeout, err)
	}
}

func TestConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maestro.db")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			s, err := Open(context.Background(), path)
			if err != nil {
				t.Error(err)
				return
			}
			var version int
			if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Error(err)
			}
			files, err := migrations.ReadDir("migrations")
			if err != nil {
				t.Error(err)
			} else if version != len(files) {
				t.Errorf("incomplete migration: version %d, want %d", version, len(files))
			}
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
}

func TestOpenWALContention(t *testing.T) {
	for _, cancelOpen := range []bool{false, true} {
		name := "released"
		if cancelOpen {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "maestro.db")
			// A reader in rollback-journal mode prevents the switch to WAL.
			db, err := sql.Open("sqlite", filepath.ToSlash(path))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(context.Background(), "CREATE TABLE existing (id INTEGER); BEGIN; SELECT * FROM existing"); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				s, err := Open(ctx, path)
				if err == nil {
					err = s.Close()
				}
				result <- err
			}()
			select {
			case err := <-result:
				t.Fatalf("Open returned while WAL setup was blocked: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			if cancelOpen {
				cancel()
			} else if _, err = conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if cancelOpen && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
				if !cancelOpen && err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Open did not finish after releasing the lock or cancelling")
			}
		})
	}
}
