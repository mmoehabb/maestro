package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Create with private permissions before SQLite opens the file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	// Windows drive letters belong in the path, not the URI authority.
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	// WAL setup can need a lock even before BEGIN IMMEDIATE. Run it explicitly
	// in migrate so contention is retried instead of failing connection setup.
	q := url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(1)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	// An immediate transaction serializes migrations across CLI processes.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Handle startup contention ourselves: SQLite's busy handler can sleep
	// through context cancellation. Normal queries keep the DSN's timeout.
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return err
	}
	if err := execBusyRetry(ctx, conn, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if err := execBusyRetry(ctx, conn, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		return err
	}
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	if version > len(files) {
		return fmt.Errorf("database version %d is newer than supported version %d", version, len(files))
	}
	for i := version; i < len(files); i++ {
		script, err := migrations.ReadFile("migrations/" + files[i].Name())
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

// SQLite can return BUSY without invoking its busy handler when upgrading a
// lock. Retry those operations within a bounded, cancellable startup window.
func execBusyRetry(ctx context.Context, conn *sql.Conn, query string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, query)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type Project struct {
	ID            int64  `json:"id"`
	Root          string `json:"root"`
	Remote        string `json:"remote"`
	DefaultBranch string `json:"default_branch"`
}

func (s *Store) EnsureProject(ctx context.Context, p Project) (Project, error) {
	err := s.db.QueryRowContext(ctx, `INSERT INTO projects(root, remote, default_branch, created_at)
		VALUES(?, ?, ?, ?) ON CONFLICT(root) DO UPDATE SET remote=excluded.remote,
		default_branch=excluded.default_branch RETURNING id`, p.Root, p.Remote, p.DefaultBranch, time.Now().UnixMilli()).Scan(&p.ID)
	return p, err
}

type Task struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"project_id"`
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Goal       string `json:"goal"`
	Notes      string `json:"notes"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"base_branch"`
	Worktree   string `json:"worktree"`
	Lifecycle  string `json:"lifecycle"`
	TabOrder   int    `json:"tab_order"`
	Agent      string `json:"agent"`
	Prompt     string `json:"prompt"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// CreateTask persists the task and its first timeline event atomically.
func (s *Store) CreateTask(ctx context.Context, t Task) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return t, err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	t.CreatedAt = time.Now().UnixMilli()
	t.UpdatedAt = t.CreatedAt
	t.Lifecycle = "new"
	err = tx.QueryRowContext(ctx, `INSERT INTO tasks(project_id, slug, title, goal, notes,
		branch, base_branch, worktree, lifecycle, tab_order, agent, prompt, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(tab_order)+1, 0) FROM tasks WHERE project_id=?), ?, ?, ?, ?)
		RETURNING id, tab_order`, t.ProjectID, t.Slug, t.Title, t.Goal, t.Notes, t.Branch, t.BaseBranch,
		t.Worktree, t.Lifecycle, t.ProjectID, t.Agent, t.Prompt, t.CreatedAt, t.UpdatedAt).Scan(&t.ID, &t.TabOrder)
	if err != nil {
		return t, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(task_id, ts, kind, payload) VALUES(?, ?, 'created', '{}')", t.ID, t.CreatedAt); err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func (s *Store) Tasks(ctx context.Context, projectID int64, all bool) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project_id, slug, title, goal, notes,
		branch, base_branch, worktree, lifecycle, tab_order, agent, prompt, created_at, updated_at
		FROM tasks WHERE project_id=? AND (? OR lifecycle != 'archived') ORDER BY tab_order, id`, projectID, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Slug, &t.Title, &t.Goal, &t.Notes, &t.Branch,
			&t.BaseBranch, &t.Worktree, &t.Lifecycle, &t.TabOrder, &t.Agent, &t.Prompt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
