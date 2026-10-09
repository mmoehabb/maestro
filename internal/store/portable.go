package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mmoehabb/maestro/internal/portable"
)

type Portable struct {
	Checkpoint              portable.Checkpoint
	Fingerprint             string
	GitState                string
	Fresh                   bool
	SourceRef, SourceCommit string
	PinnedRef               string
}

func (s *Store) Portable(ctx context.Context, id int64) (Portable, error) {
	var p Portable
	var manifest string
	err := s.db.QueryRowContext(ctx, "SELECT manifest,context,fingerprint,git_state,fresh,source_ref,source_commit,pinned_ref FROM portable_tasks WHERE task_id=?", id).Scan(&manifest, &p.Checkpoint.Handoff, &p.Fingerprint, &p.GitState, &p.Fresh, &p.SourceRef, &p.SourceCommit, &p.PinnedRef)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(manifest), &p.Checkpoint.Manifest)
	return p, err
}

func (s *Store) SavePortable(ctx context.Context, id int64, p Portable) error {
	b, err := json.Marshal(p.Checkpoint.Manifest)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO portable_tasks(task_id,uuid,manifest,context,fingerprint,git_state,fresh,source_ref,source_commit,pinned_ref) VALUES(?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(task_id) DO UPDATE SET uuid=excluded.uuid,manifest=excluded.manifest,context=excluded.context,fingerprint=excluded.fingerprint,git_state=excluded.git_state,fresh=excluded.fresh,source_ref=excluded.source_ref,source_commit=excluded.source_commit,pinned_ref=excluded.pinned_ref`, id, p.Checkpoint.ID, string(b), p.Checkpoint.Handoff, p.Fingerprint, p.GitState, p.Fresh, p.SourceRef, p.SourceCommit, p.PinnedRef)
	return err
}

func (s *Store) PinnedPortableRef(ctx context.Context, projectID int64, uuid string) (string, error) {
	var ref string
	err := s.db.QueryRowContext(ctx, `SELECT p.pinned_ref FROM portable_tasks p JOIN tasks t ON t.id=p.task_id
	WHERE t.project_id=? AND p.uuid=?`, projectID, uuid).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ref, err
}

// ImportPortable makes the task and its first pending handoff visible together.
func (s *Store) ImportPortable(ctx context.Context, t Task, c portable.Checkpoint, ref, commit, pinnedRef string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return t, err
	}
	defer tx.Rollback() //nolint:errcheck
	now := time.Now().UnixMilli()
	state := "active"
	var archivedAt any
	if c.Archived {
		state, archivedAt = "archived", now
	}
	if t.ID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO tasks(project_id,slug,title,goal,notes,branch,base_branch,worktree,lifecycle,tab_order,agent,prompt,created_at,updated_at,archived_at)
		VALUES(?,?,?,?,?,?,?,?,?,(SELECT COALESCE(MAX(tab_order)+1,0) FROM tasks WHERE project_id=?),?,'',?,?,?) RETURNING id`, t.ProjectID, c.Slug, c.Title, c.Goal, c.Notes, c.Branch, c.BaseCommit, t.Worktree, state, t.ProjectID, c.Agent, now, now, archivedAt).Scan(&t.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET slug=?,title=?,goal=?,notes=?,branch=?,base_branch=?,worktree=?,lifecycle=?,agent=?,prompt='',updated_at=?,archived_at=?,pr_number=NULL,pr_url=NULL,pr_state=NULL,ci_state=NULL,review_state=NULL,pr_head_sha='',merge_sha='',reopen_sha='',pr_base='',cleanup_pending=0,reopen_step=0 WHERE id=?`, c.Slug, c.Title, c.Goal, c.Notes, c.Branch, c.BaseCommit, t.Worktree, state, c.Agent, now, archivedAt, t.ID)
	}
	if err != nil {
		return t, err
	}
	b, err := json.Marshal(c.Manifest)
	if err != nil {
		return t, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO portable_tasks(task_id,uuid,manifest,context,fresh,source_ref,source_commit,pinned_ref) VALUES(?,?,?,?,1,?,?,?)
	ON CONFLICT(task_id) DO UPDATE SET uuid=excluded.uuid,manifest=excluded.manifest,context=excluded.context,fingerprint='',git_state='',fresh=1,source_ref=excluded.source_ref,source_commit=excluded.source_commit,pinned_ref=CASE WHEN excluded.pinned_ref!='' THEN excluded.pinned_ref ELSE portable_tasks.pinned_ref END`, t.ID, c.ID, string(b), c.Handoff, ref, commit, pinnedRef)
	if err != nil {
		return t, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO handoffs(task_id,agent,content,created_at) VALUES(?,?,?,?)
	ON CONFLICT(task_id) WHERE delivered_at IS NULL DO UPDATE SET agent=excluded.agent,content=excluded.content,from_session=NULL,to_session=NULL,created_at=excluded.created_at`, t.ID, c.Agent, c.Handoff, now)
	if err != nil {
		return t, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(task_id,ts,kind,payload) VALUES(?,?,'restored','{}')", t.ID, now)
	if err != nil {
		return t, err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM portable_deleted WHERE project_id=? AND uuid=?", t.ProjectID, c.ID)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func (s *Store) PortableDeleted(ctx context.Context, project int64, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM portable_deleted WHERE project_id=? AND uuid=?)", project, id).Scan(&exists)
	return exists, err
}
