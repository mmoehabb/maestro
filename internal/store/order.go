package store

import (
	"context"
	"database/sql"
	"fmt"
)

// The caller commits visibility and position in the same transaction. Archived
// positions are intentionally ignored: only the visible order is meaningful.
func appendTaskOrder(ctx context.Context, tx *sql.Tx, id int64) (int, error) {
	var position int
	err := tx.QueryRowContext(ctx, `UPDATE tasks SET tab_order=(
		SELECT COALESCE(MAX(tab_order)+1,0) FROM tasks
		WHERE project_id=(SELECT project_id FROM tasks WHERE id=?)
		AND lifecycle!='archived' AND id!=?
	) WHERE id=? RETURNING tab_order`, id, id, id).Scan(&position)
	return position, err
}

// ReorderTasks changes the complete visible order atomically. Stale requests
// (for example after a task is archived) cannot silently drop or duplicate tasks.
func (s *Store) ReorderTasks(ctx context.Context, projectID int64, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE project_id=? AND lifecycle!='archived'", projectID).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return fmt.Errorf("task list changed; retry reordering")
	}
	seen := make(map[int64]bool, len(ids))
	for i, id := range ids {
		if seen[id] {
			return fmt.Errorf("duplicate task in order")
		}
		seen[id] = true
		result, e := tx.ExecContext(ctx, "UPDATE tasks SET tab_order=? WHERE id=? AND project_id=? AND lifecycle!='archived'", i, id, projectID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("task list changed; retry reordering")
		}
	}
	return tx.Commit()
}
