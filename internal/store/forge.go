package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SaveWorkflow changes only lifecycle/forge fields, never overwriting concurrent
// transcript, notes, or agent updates. The event and state commit together.
func (s *Store) SaveWorkflow(ctx context.Context, task Task, kind string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var current string
	if err = tx.QueryRowContext(ctx, "SELECT lifecycle FROM tasks WHERE id=?", task.ID).Scan(&current); err != nil {
		return err
	}
	allowed := map[string][]string{
		"new":     {"active", "pushed", "pr_open", "archived"},
		"active":  {"pushed", "pr_open", "archived"},
		"pushed":  {"active", "pr_open", "archived"},
		"pr_open": {"merged", "closed", "archived"},
		"merged":  {"archived"}, "closed": {"archived"}, "archived": {"active", "pr_open"},
	}
	valid := current == task.Lifecycle
	for _, next := range allowed[current] {
		valid = valid || next == task.Lifecycle
	}
	if !valid {
		return fmt.Errorf("invalid lifecycle transition %s → %s", current, task.Lifecycle)
	}
	now := time.Now().UnixMilli()
	var archivedAt any
	if task.Lifecycle == "archived" {
		archivedAt = now
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET branch=?,lifecycle=?,pr_number=NULLIF(?,0),pr_url=?,pr_state=?,ci_state=?,review_state=?,pr_head_sha=?,merge_sha=?,reopen_sha=?,pr_base=?,cleanup_pending=?,reopen_step=?,archived_at=?,updated_at=? WHERE id=?`, task.Branch, task.Lifecycle, task.PRNumber, task.PRURL, task.PRState, task.CIState, task.ReviewState, task.PRHeadSHA, task.MergeSHA, task.ReopenSHA, task.PRBase, task.CleanupPending, task.ReopenStep, archivedAt, now, task.ID)
	if err != nil {
		return err
	}
	if kind != "" {
		payload, e := json.Marshal(map[string]any{"from": current, "to": task.Lifecycle, "branch": task.Branch, "pr_number": task.PRNumber, "pr_url": task.PRURL, "ci": task.CIState, "review": task.ReviewState, "head_sha": task.PRHeadSHA, "merge_sha": task.MergeSHA})
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,?,?)`, task.ID, now, kind, string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
