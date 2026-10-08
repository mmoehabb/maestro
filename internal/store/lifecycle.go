package store

import (
	"context"
	"fmt"
	"time"
)

// SetArchived changes visibility and records the transition atomically.
func (s *Store) SetArchived(ctx context.Context, id int64, archived bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var current string
	if err = tx.QueryRowContext(ctx, "SELECT lifecycle FROM tasks WHERE id=?", id).Scan(&current); err != nil {
		return err
	}
	if (current == "archived") == archived {
		return nil
	}
	state, event := "active", "reopened"
	var archivedAt any
	now := time.Now().UnixMilli()
	if archived {
		state, event, archivedAt = "archived", "archived", now
	} else if _, err = appendTaskOrder(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET lifecycle=?, archived_at=?, updated_at=? WHERE id=?", state, archivedAt, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,?,'{}')", id, now, event); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteArchived removes all Maestro-owned records in one transaction.
func (s *Store) DeleteArchived(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var state string
	if err = tx.QueryRowContext(ctx, "SELECT lifecycle FROM tasks WHERE id=?", id).Scan(&state); err != nil {
		return err
	}
	if state != "archived" {
		return fmt.Errorf("task must be archived before deletion")
	}
	for _, query := range []string{
		"DELETE FROM handoffs WHERE task_id=?",
		"DELETE FROM turns WHERE session_id IN (SELECT id FROM agent_sessions WHERE task_id=?)",
		"UPDATE agent_sessions SET handoff_from=NULL WHERE task_id=?",
		"DELETE FROM agent_sessions WHERE task_id=?",
		"DELETE FROM native_conversations WHERE task_id=?",
		"DELETE FROM events WHERE task_id=?",
		"DELETE FROM tasks WHERE id=?",
	} {
		if _, err = tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
