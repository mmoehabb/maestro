package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Turn struct {
	ID         int64     `json:"id"`
	SessionID  int64     `json:"session_id"`
	SourceKey  string    `json:"source_key"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	TS         time.Time `json:"ts"`
}
type TimelineEvent struct {
	ID      int64           `json:"id"`
	TS      time.Time       `json:"ts"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}
type Handoff struct {
	ID          int64      `json:"id"`
	FromSession int64      `json:"from_session"`
	ToSession   int64      `json:"to_session"`
	Agent       string     `json:"agent"`
	Content     string     `json:"content"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}
type History struct {
	Task     Task            `json:"task"`
	Sessions []Session       `json:"sessions"`
	Turns    []Turn          `json:"turns"`
	Events   []TimelineEvent `json:"events"`
	Handoffs []Handoff       `json:"handoffs"`
}

func (s *Store) LatestAgentSession(ctx context.Context, taskID int64, agent string) (Session, error) {
	var x Session
	var ts int64
	err := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(native_id,''), started_at, agent FROM agent_sessions WHERE task_id=? AND agent=? ORDER BY id DESC LIMIT 1`, taskID, agent).Scan(&x.ID, &x.NativeID, &ts, &x.Agent)
	if errors.Is(err, sql.ErrNoRows) {
		return x, nil
	}
	x.StartedAt = time.UnixMilli(ts)
	return x, err
}

// ImportTurns identifies native records independently of the process that read them.
// Existing records keep their owner when streaming content is updated on resume.
func (s *Store) ImportTurns(ctx context.Context, sessionID int64, turns []Turn) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var taskID, conversationID int64
	err = tx.QueryRowContext(ctx, `SELECT task_id, COALESCE(conversation_id,0) FROM agent_sessions WHERE id=?`, sessionID).Scan(&taskID, &conversationID)
	if err != nil {
		return err
	}
	if conversationID == 0 {
		return errors.New("cannot import transcript without native identity")
	}
	for _, t := range turns {
		owner := sessionID
		if !t.TS.IsZero() {
			err = tx.QueryRowContext(ctx, `SELECT id FROM agent_sessions WHERE conversation_id=? ORDER BY CASE WHEN started_at<=? THEN 0 ELSE 1 END, CASE WHEN started_at<=? THEN -started_at ELSE started_at END, id LIMIT 1`, conversationID, t.TS.UnixMilli(), t.TS.UnixMilli()).Scan(&owner)
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO turns(session_id,seq,role,content,ts,conversation_id,source_key,tool_call_id)
   VALUES(?,(SELECT COALESCE(MAX(seq),0)+1 FROM turns WHERE session_id=?),?,?,?,?,?,?)
   ON CONFLICT(conversation_id,source_key) DO UPDATE SET content=excluded.content, role=excluded.role, tool_call_id=excluded.tool_call_id`, owner, owner, t.Role, t.Content, t.TS.UnixMilli(), conversationID, t.SourceKey, t.ToolCallID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddEvent(ctx context.Context, taskID int64, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,?,?)`, taskID, time.Now().UnixMilli(), kind, string(b))
	return err
}

func (s *Store) History(ctx context.Context, task Task) (History, error) {
	h := History{Task: task, Sessions: []Session{}, Turns: []Turn{}, Events: []TimelineEvent{}, Handoffs: []Handoff{}}
	// Read a consistent snapshot while other tasks import new records.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return h, err
	}
	defer tx.Rollback() //nolint:errcheck
	// Close every cursor before starting another query: the store has one connection.
	rows, err := tx.QueryContext(ctx, `SELECT id,agent,COALESCE(native_id,''),started_at,ended_at,exit_code,COALESCE(handoff_from,0),native_complete FROM agent_sessions WHERE task_id=? ORDER BY id`, task.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var x Session
		var ts int64
		var end, code sql.NullInt64
		if err = rows.Scan(&x.ID, &x.Agent, &x.NativeID, &ts, &end, &code, &x.HandoffFrom, &x.NativeComplete); err != nil {
			break
		}
		x.StartedAt = time.UnixMilli(ts)
		if end.Valid {
			v := time.UnixMilli(end.Int64)
			x.EndedAt = &v
		}
		if code.Valid {
			v := int(code.Int64)
			x.ExitCode = &v
		}
		h.Sessions = append(h.Sessions, x)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return h, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT t.id,t.session_id,COALESCE(t.source_key,''),t.role,t.content,t.ts,t.tool_call_id FROM turns t JOIN agent_sessions s ON s.id=t.session_id WHERE s.task_id=? ORDER BY t.ts,t.id`, task.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var x Turn
		var ts int64
		if err = rows.Scan(&x.ID, &x.SessionID, &x.SourceKey, &x.Role, &x.Content, &ts, &x.ToolCallID); err != nil {
			break
		}
		x.TS = time.UnixMilli(ts)
		h.Turns = append(h.Turns, x)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return h, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,ts,kind,payload FROM events WHERE task_id=? ORDER BY ts,id`, task.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var x TimelineEvent
		var ts int64
		var b string
		if err = rows.Scan(&x.ID, &ts, &x.Kind, &b); err != nil {
			break
		}
		x.TS = time.UnixMilli(ts)
		x.Payload = json.RawMessage(b)
		h.Events = append(h.Events, x)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return h, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,COALESCE(from_session,0),COALESCE(to_session,0),agent,content,created_at,delivered_at FROM handoffs WHERE task_id=? ORDER BY id`, task.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var x Handoff
		var ts int64
		var end sql.NullInt64
		if err = rows.Scan(&x.ID, &x.FromSession, &x.ToSession, &x.Agent, &x.Content, &ts, &end); err != nil {
			break
		}
		x.CreatedAt = time.UnixMilli(ts)
		if end.Valid {
			v := time.UnixMilli(end.Int64)
			x.DeliveredAt = &v
		}
		h.Handoffs = append(h.Handoffs, x)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return h, err
	}
	return h, tx.Commit()
}

func (s *Store) PrepareHandoff(ctx context.Context, taskID, from int64, agent, content string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	_, err = tx.ExecContext(ctx, `INSERT INTO handoffs(task_id,from_session,agent,content,created_at) VALUES(?,NULLIF(?,0),?,?,?) ON CONFLICT(task_id) WHERE delivered_at IS NULL DO UPDATE SET agent=excluded.agent,content=excluded.content,from_session=excluded.from_session,created_at=excluded.created_at`, taskID, from, agent, content, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET agent=?,updated_at=? WHERE id=?`, agent, time.Now().UnixMilli(), taskID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PendingHandoff(ctx context.Context, taskID int64) (Handoff, error) {
	var h Handoff
	err := s.db.QueryRowContext(ctx, `SELECT id,COALESCE(from_session,0),agent,content FROM handoffs WHERE task_id=? AND delivered_at IS NULL`, taskID).Scan(&h.ID, &h.FromSession, &h.Agent, &h.Content)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return h, err
}
