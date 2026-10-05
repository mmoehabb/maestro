package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Session struct {
	ID        int64
	NativeID  string
	StartedAt time.Time
}

func (s *Store) LatestSession(ctx context.Context, taskID int64) (Session, error) {
	var session Session
	var started int64
	err := s.db.QueryRowContext(ctx, "SELECT id, COALESCE(native_id,''), started_at FROM agent_sessions WHERE task_id=? ORDER BY id DESC LIMIT 1", taskID).Scan(&session.ID, &session.NativeID, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return session, nil
	}
	session.StartedAt = time.UnixMilli(started)
	return session, err
}

func (s *Store) StartSession(ctx context.Context, task Task, nativeID string, started time.Time) (Session, error) {
	session := Session{NativeID: nativeID, StartedAt: started}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return session, err
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	if _, err := tx.ExecContext(ctx, "UPDATE agent_sessions SET ended_at=? WHERE task_id=? AND ended_at IS NULL", session.StartedAt.UnixMilli(), task.ID); err != nil {
		return session, err
	}
	err = tx.QueryRowContext(ctx, "INSERT INTO agent_sessions(task_id, agent, native_id, started_at) VALUES(?,?,?,?) RETURNING id", task.ID, task.Agent, nativeID, session.StartedAt.UnixMilli()).Scan(&session.ID)
	if err != nil {
		return session, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET lifecycle='active', updated_at=? WHERE id=?", session.StartedAt.UnixMilli(), task.ID); err != nil {
		return session, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(task_id, ts, kind, payload) VALUES(?,?,'agent_started','{}')", task.ID, session.StartedAt.UnixMilli()); err != nil {
		return session, err
	}
	return session, tx.Commit()
}

func (s *Store) SetNativeID(ctx context.Context, id int64, nativeID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET native_id=? WHERE id=?", nativeID, id)
	return err
}

func (s *Store) EndSession(ctx context.Context, id int64, code int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET ended_at=?, exit_code=? WHERE id=?", time.Now().UnixMilli(), code, id)
	return err
}

// SaveScrollback keeps the terminal fallback after process/worktree removal.
func (s *Store) SaveScrollback(ctx context.Context, id int64, text string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO turns(session_id, seq, role, content, ts) VALUES(?,0,'terminal',?,?)
		ON CONFLICT(session_id,seq) DO UPDATE SET content=excluded.content, ts=excluded.ts`, id, text, time.Now().UnixMilli())
	return err
}
