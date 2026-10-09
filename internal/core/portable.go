package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofrs/flock"
	"github.com/google/uuid"

	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/handoff"
	"github.com/mmoehabb/maestro/internal/portable"
	"github.com/mmoehabb/maestro/internal/store"
)

func fingerprint(h store.History) string {
	b, _ := json.Marshal(struct {
		Title, Goal, Notes, Agent, Branch string
		Archived                          bool
		Sessions                          []store.Session
		Turns                             []store.Turn
	}{h.Task.Title, h.Task.Goal, h.Task.Notes, h.Task.Agent, h.Task.Branch, h.Task.Lifecycle == "archived", h.Sessions, h.Turns})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (s *TaskService) Checkpoint(ctx context.Context, slug string) (string, error) {
	return s.CheckpointOnBranch(ctx, slug, "")
}

// CheckpointOnBranch explicitly adopts a branch renamed in the same checkout.
func (s *TaskService) CheckpointOnBranch(ctx context.Context, slug, branch string) (string, error) {
	var path string
	err := s.mutateTask(ctx, slug, func(t store.Task) error {
		if branch != "" && branch != t.Branch {
			if err := s.Repo.ValidateTaskCheckout(ctx, t.Worktree, branch); err != nil {
				return err
			}
			tasks, err := s.Store.Tasks(ctx, s.Project.ID, true)
			if err != nil {
				return err
			}
			for _, other := range tasks {
				if other.ID != t.ID && other.Branch == branch {
					return fmt.Errorf("branch already belongs to task %s", other.Slug)
				}
			}
			p, err := s.Store.Portable(ctx, t.ID)
			if err != nil {
				return err
			}
			t.Branch = branch
			if err = s.Store.SaveWorkflow(ctx, t, "branch_renamed"); err != nil {
				return err
			}
			if p.Checkpoint.ID != "" {
				p.SourceCommit, p.SourceRef = "", ""
				if err = s.Store.SavePortable(ctx, t.ID, p); err != nil {
					return err
				}
			}
		}
		var err error
		path, err = s.checkpoint(ctx, t)
		return err
	})
	return path, err
}

// Checkpoint stops and drains the agent so the checkpoint includes its final
// native history and terminal fallback. Restart/resume remains available.
func (r *Runtime) Checkpoint(ctx context.Context, task store.Task) (string, error) {
	done, err := r.operation(task.ID)
	if err != nil {
		return "", err
	}
	defer done()
	if old := r.entry(task.ID); old != nil {
		old.pane.Stop()
		<-old.finished
		if old.saveErr != nil {
			if err = r.retryHistory(old); err != nil {
				return "", err
			}
		}
	}
	task, err = r.Service.Find(ctx, task.Slug)
	if err != nil {
		return "", err
	}
	return r.Service.checkpoint(ctx, task)
}

func (s *TaskService) checkpoint(ctx context.Context, task store.Task) (string, error) {
	if task.CleanupPending {
		return "", fmt.Errorf("finish cleanup before checkpointing")
	}
	if err := s.ensurePortableWorktree(ctx, task); err != nil {
		return "", err
	}
	if err := s.Repo.ValidateTaskCheckout(ctx, task.Worktree, task.Branch); err != nil {
		return "", err
	}
	p, err := s.Store.Portable(ctx, task.ID)
	if err != nil {
		return "", err
	}
	history, err := s.Store.History(ctx, task)
	if err != nil {
		return "", err
	}
	base, err := s.Repo.Commit(ctx, task.BaseBranch)
	if err != nil {
		return "", fmt.Errorf("checkpoint base is unavailable; fetch %s: %w", task.BaseBranch, err)
	}
	head, err := git.Head(ctx, task.Worktree)
	if err != nil {
		return "", err
	}
	gitState, err := git.Context(ctx, task.Worktree, task.BaseBranch)
	if err != nil {
		return "", err
	}
	c := portable.Checkpoint{Manifest: portable.Manifest{
		Version: 1, ID: p.Checkpoint.ID, Parent: p.Checkpoint.Checkpoint,
		Slug: task.Slug, Title: task.Title, Goal: task.Goal, Notes: task.Notes,
		Agent: task.Agent, Branch: task.Branch, BaseBranch: task.BaseBranch,
		BaseCommit: base, Archived: task.Lifecycle == "archived",
	}}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if p.Checkpoint.BaseBranch != "" && task.BaseBranch == p.Checkpoint.BaseCommit {
		c.BaseBranch = p.Checkpoint.BaseBranch
	}
	if p.Fingerprint == fingerprint(history) && p.GitState == gitState && p.Checkpoint.Checkpoint != "" {
		c = p.Checkpoint
	} else {
		previous := p
		previous.Fingerprint = "" // Rebuild current Git state even without new dialogue.
		c.Handoff, err = s.continuation(ctx, history, previous)
		if err != nil {
			return "", err
		}
		c.Seal()
	}
	if err = s.Repo.EnablePortable(task.Worktree); err != nil {
		return "", err
	}
	if err = c.Validate(); err != nil {
		return "", err
	}
	p.Checkpoint, p.Fingerprint = c, fingerprint(history)
	p.GitState = gitState
	p.SourceRef, p.SourceCommit = "refs/heads/"+task.Branch, head
	if err = s.Store.SavePortable(ctx, task.ID, p); err != nil {
		return "", err
	}
	if err = portable.Write(task.Worktree, c); err != nil {
		return "", err
	}
	return filepath.Join(task.Worktree, c.Directory()), nil
}

func (s *TaskService) continuation(ctx context.Context, h store.History, p store.Portable) (string, error) {
	budget := min(s.Config.Handoff.TokenBudget, portable.MaxBytes)
	if p.Checkpoint.Handoff != "" && p.Fingerprint == fingerprint(h) {
		return handoff.Limit(p.Checkpoint.Handoff, budget), nil
	}
	state, err := git.Context(ctx, h.Task.Worktree, h.Task.BaseBranch)
	if err != nil {
		return "", err
	}
	if p.Checkpoint.Handoff == "" {
		return handoff.Build(h, state, h.Task.Agent, budget), nil
	}
	// Keep the prior checkpoint independently of local session history. In
	// particular, an empty new database must not erase imported conversation.
	return handoff.WithPrevious(h, state, h.Task.Agent, budget, p.Checkpoint.Handoff), nil
}

func (s *TaskService) ensurePortableWorktree(ctx context.Context, task store.Task) error {
	p, err := s.Store.Portable(ctx, task.ID)
	if err != nil || p.SourceCommit == "" {
		return err
	}
	if _, err = s.Repo.Commit(ctx, p.Checkpoint.BaseCommit); err != nil {
		return fmt.Errorf("task base commit is missing; fetch full branch history before opening %s", task.Slug)
	}
	commit, ref := p.SourceCommit, p.SourceRef
	// Explicit replacement selects metadata, while Git files remain on the local
	// branch. A divergent checkout is safe to use when its committed context is
	// identical to the chosen checkpoint.
	if p.PinnedRef != "" {
		local, e := s.Repo.TaskCheckpoints(ctx, "refs/heads/"+task.Branch)
		if e == nil {
			for _, candidate := range local {
				if candidate.ID == p.Checkpoint.ID && candidate.Checkpoint.Checkpoint == p.Checkpoint.Checkpoint {
					commit, ref = candidate.Commit, candidate.Ref
					break
				}
			}
		}
	}
	if err = s.Repo.RestoreWorktree(ctx, task.Worktree, task.Branch, commit, ref); err != nil {
		return err
	}
	return s.Repo.EnablePortable(task.Worktree)
}

// DiscoverTasks imports committed metadata, without creating worktrees or
// launching agents. Listing remains available while a TUI owns the lock.
func (s *TaskService) DiscoverTasks(ctx context.Context) error {
	lock := flock.New(s.LockPath)
	ok, err := lock.TryLock()
	if err != nil || !ok {
		return err
	}
	defer lock.Close()
	return s.restoreTasks(ctx, "", false)
}

func (s *TaskService) RestoreTasks(ctx context.Context, ref string, replace bool) error {
	lock := flock.New(s.LockPath)
	ok, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project is busy; quit the TUI before restoring tasks")
	}
	defer lock.Close()
	return s.restoreTasks(ctx, ref, replace)
}

func (s *TaskService) restoreTasks(ctx context.Context, ref string, replace bool) error {
	candidates, err := s.Repo.TaskCheckpoints(ctx, ref)
	if err != nil {
		return err
	}
	grouped := map[string][]git.TaskCheckpoint{}
	for _, c := range candidates {
		grouped[c.ID] = append(grouped[c.ID], c)
	}
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		deleted, err := s.Store.PortableDeleted(ctx, s.Project.ID, id)
		if err != nil {
			return err
		}
		if deleted && ref == "" {
			continue
		}
		pin, err := s.Store.PinnedPortableRef(ctx, s.Project.ID, id)
		if err != nil {
			return err
		}
		c, err := s.selectCheckpoint(ctx, grouped[id], pin)
		if err != nil {
			return err
		}
		tasks, err := s.Store.Tasks(ctx, s.Project.ID, true)
		if err != nil {
			return err
		}
		target := store.Task{ProjectID: s.Project.ID}
		var existing store.Portable
		for _, task := range tasks {
			p, err := s.Store.Portable(ctx, task.ID)
			if err != nil {
				return err
			}
			if p.Checkpoint.ID == id {
				target, existing = task, p
				break
			}
		}
		if target.ID != 0 {
			if existing.Checkpoint.Checkpoint == c.Checkpoint.Checkpoint && !replace {
				// Recover a crash between the atomic import and baseline save.
				if existing.Fingerprint == "" {
					h, err := s.Store.History(ctx, target)
					if err != nil {
						return err
					}
					existing.Fingerprint = fingerprint(h)
				}
				if existing.SourceCommit == "" || s.Repo.IsAncestor(ctx, existing.SourceCommit, c.Commit) {
					existing.SourceRef, existing.SourceCommit = c.Ref, c.Commit
				}
				if err = s.Store.SavePortable(ctx, target.ID, existing); err != nil {
					return err
				}
				continue
			}
			if !replace {
				if existing.Checkpoint.Parent == c.Checkpoint.Checkpoint || (existing.SourceCommit != "" && s.Repo.IsAncestor(ctx, c.Commit, existing.SourceCommit)) {
					continue
				}
				h, err := s.Store.History(ctx, target)
				if err != nil {
					return err
				}
				if existing.Fingerprint != fingerprint(h) || (existing.Checkpoint.Checkpoint != c.Parent && !s.Repo.HasCheckpoint(ctx, c.Commit, existing.Checkpoint)) {
					return fmt.Errorf("task %s has local changes or conflicting context; checkpoint local work, then reconcile or use maestro restore %s --replace", target.Slug, c.Ref)
				}
			}
		}
		for _, t := range tasks {
			if t.ID != target.ID && (t.Slug == c.Slug || t.Branch == c.Branch) {
				return fmt.Errorf("portable task %s conflicts with existing slug or branch; rename the conflicting task/branch before restoring", c.Slug)
			}
		}
		path, err := s.Repo.TaskWorktree(ctx, c.Branch)
		if err != nil {
			return err
		}
		if path == "" {
			path = filepath.Join(s.Config.Worktree.Root, s.Repo.Key(), c.Slug)
		}
		target.Worktree = path
		if _, err = s.Repo.Commit(ctx, c.BaseCommit); err != nil {
			return fmt.Errorf("base commit for %s is missing; fetch full history", c.Slug)
		}
		pinnedRef := ""
		if replace {
			pinnedRef = c.Ref
		}
		target, err = s.Store.ImportPortable(ctx, target, c.Checkpoint, c.Ref, c.Commit, pinnedRef)
		if err != nil {
			return err
		}
		target, err = s.Find(ctx, c.Slug)
		if err != nil {
			return err
		}
		h, err := s.Store.History(ctx, target)
		if err != nil {
			return err
		}
		p, err := s.Store.Portable(ctx, target.ID)
		if err != nil {
			return err
		}
		p.Fingerprint = fingerprint(h)
		if err = s.Store.SavePortable(ctx, target.ID, p); err != nil {
			return err
		}
	}
	return nil
}

// selectCheckpoint picks the newest reachable tip. An explicit replacement
// resolves forks by choosing the ref the user named.
func (s *TaskService) selectCheckpoint(ctx context.Context, candidates []git.TaskCheckpoint, pin string) (git.TaskCheckpoint, error) {
	var heads []git.TaskCheckpoint
	for _, candidate := range candidates {
		older := false
		for _, other := range candidates {
			if candidate.Commit == other.Commit {
				if candidate.Ref != other.Ref && (other.Ref == pin || candidate.Ref != pin && other.Ref < candidate.Ref) {
					older = true
					break
				}
			} else if s.Repo.IsAncestor(ctx, candidate.Commit, other.Commit) {
				older = true
				break
			}
		}
		if !older {
			heads = append(heads, candidate)
		}
	}
	if len(heads) == 1 {
		return heads[0], nil
	}
	for _, c := range heads {
		if pin != "" && c.Ref == pin {
			return c, nil
		}
	}
	return git.TaskCheckpoint{}, fmt.Errorf("task %s has conflicting checkpoints; reconcile branches or use maestro restore <ref> --replace", candidates[0].Slug)
}

func (s *TaskService) checkpointReady(ctx context.Context, task store.Task) error {
	p, err := s.Store.Portable(ctx, task.ID)
	if err != nil || p.Checkpoint.ID == "" {
		return err
	}
	h, err := s.Store.History(ctx, task)
	if err != nil {
		return err
	}
	if fingerprint(h) != p.Fingerprint {
		return fmt.Errorf("portable context is stale; stop the agent and run maestro checkpoint %s, then commit the checkpoint", task.Slug)
	}
	return s.Repo.CheckpointCommitted(ctx, task.Worktree, p.Checkpoint)
}

func (s *TaskService) portablePRBase(ctx context.Context, task store.Task) string {
	p, err := s.Store.Portable(ctx, task.ID)
	if err != nil {
		return ""
	}
	base := p.Checkpoint.BaseBranch
	if base == "" {
		return ""
	}
	if strings.HasPrefix(base, "refs/heads/") {
		return strings.TrimPrefix(base, "refs/heads/")
	}
	if strings.HasPrefix(base, "refs/remotes/") {
		_, branch, _ := strings.Cut(strings.TrimPrefix(base, "refs/remotes/"), "/")
		return branch
	}
	if resolved, err := s.Repo.PRBase(ctx, base); err == nil {
		return resolved
	}
	if _, err := s.Repo.Commit(ctx, "refs/remotes/origin/"+base); err == nil {
		return base
	}
	// A commit-only base still requires an explicit PR target, as before.
	return ""
}
