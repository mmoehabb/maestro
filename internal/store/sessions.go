package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Session struct {
	ID             int64      `json:"id"`
	Agent          string     `json:"agent"`
	NativeID       string     `json:"native_id"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at"`
	ExitCode       *int       `json:"exit_code"`
	HandoffFrom    int64      `json:"handoff_from,omitempty"`
	NativeComplete bool       `json:"native_complete"`
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
	session := Session{NativeID: nativeID, StartedAt: started, Agent: task.Agent}
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
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET lifecycle=CASE WHEN lifecycle='new' THEN 'active' ELSE lifecycle END, updated_at=? WHERE id=?", session.StartedAt.UnixMilli(), task.ID); err != nil {
		return session, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(task_id, ts, kind, payload) VALUES(?,?,'agent_started','{}')", task.ID, session.StartedAt.UnixMilli()); err != nil {
		return session, err
	}
	if nativeID != "" {
		if err := bindIdentity(ctx, tx, session.ID, nativeID); err != nil {
			return session, err
		}
	}
	var handoffID int64
	err = tx.QueryRowContext(ctx, `SELECT id,COALESCE(from_session,0) FROM handoffs WHERE task_id=? AND agent=? AND delivered_at IS NULL`, task.ID, task.Agent).Scan(&handoffID, &session.HandoffFrom)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return session, err
	}
	if handoffID != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET handoff_from=NULLIF(?,0) WHERE id=?`, session.HandoffFrom, session.ID); err != nil {
			return session, err
		}
		// Process creation is not delivery: an agent can still fail during startup.
		if _, err := tx.ExecContext(ctx, `UPDATE handoffs SET to_session=? WHERE id=?`, session.ID, handoffID); err != nil {
			return session, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,'switched',json_object('session_id',?,'handoff_id',?,'agent',?))`, task.ID, started.UnixMilli(), session.ID, handoffID, task.Agent); err != nil {
			return session, err
		}
	}
	return session, tx.Commit()
}

// ConfirmHandoff acknowledges only the launch currently associated with the
// pending handoff. A stale watcher cannot consume a newer switch's context.
func (s *Store) ConfirmHandoff(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE handoffs SET delivered_at=? WHERE to_session=? AND delivered_at IS NULL`, time.Now().UnixMilli(), sessionID)
	return err
}

func bindIdentity(ctx context.Context, tx *sql.Tx, id int64, nativeID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO native_conversations(task_id,agent,native_id) SELECT task_id,agent,? FROM agent_sessions WHERE id=? ON CONFLICT DO NOTHING`, nativeID, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_sessions SET native_id=?,conversation_id=(SELECT n.id FROM native_conversations n WHERE n.task_id=agent_sessions.task_id AND n.agent=agent_sessions.agent AND n.native_id=?) WHERE id=?`, nativeID, nativeID, id)
	return err
}

func (s *Store) SetNativeID(ctx context.Context, id int64, nativeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err = bindIdentity(ctx, tx, id, nativeID); err != nil {
		return err
	}
	return tx.Commit()
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

// SetNativeComplete records whether the latest native snapshot was fully imported.
// Unknown and failed imports retain terminal fallback in handoffs.
func (s *Store) SetNativeComplete(ctx context.Context, sessionID int64, complete bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET native_complete=? WHERE id=?", complete, sessionID)
	return err
}
