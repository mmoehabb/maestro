package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxNotesBytes = 64 << 10

func (s *Store) SetNotes(ctx context.Context, taskID int64, notes string) error {
	if !utf8.ValidString(notes) || len(notes) > MaxNotesBytes {
		return fmt.Errorf("notes must be valid UTF-8 and at most 64 KiB")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var previous string
	if err = tx.QueryRowContext(ctx, "SELECT notes FROM tasks WHERE id=?", taskID).Scan(&previous); err != nil {
		return err
	}
	if previous == notes {
		return nil
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET notes=?,updated_at=? WHERE id=?", notes, now, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,'notes_updated','{}')", taskID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTitle edits presentation only; task identity and Git paths stay stable.
func (s *Store) SetTitle(ctx context.Context, taskID int64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || !utf8.ValidString(title) || len(title) > 512 || strings.ContainsFunc(title, unicode.IsControl) {
		return fmt.Errorf("title must be nonempty UTF-8, at most 512 bytes, without control characters")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var previous string
	if err = tx.QueryRowContext(ctx, "SELECT title FROM tasks WHERE id=?", taskID).Scan(&previous); err != nil {
		return err
	}
	if previous == title {
		return nil
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET title=?,updated_at=? WHERE id=?", title, now, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,'title_updated','{}')", taskID, now); err != nil {
		return err
	}
	return tx.Commit()
}
