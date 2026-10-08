package store

import (
	"context"
	"fmt"
)

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
