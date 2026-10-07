package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

func (s *TaskService) archiveHandoffs(ctx context.Context, task store.Task) error {
	history, err := s.Store.History(ctx, task)
	if err != nil {
		return err
	}
	root := s.DataDir
	if root == "" {
		root = filepath.Dir(filepath.Dir(s.LockPath))
	}
	dir := filepath.Join("archive", s.Repo.Key(), task.Slug)
	// Confine all archive path components to the configured data directory.
	archive, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer archive.Close()
	if err = archive.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, h := range history.Handoffs {
		name := filepath.Join(dir, "handoff-"+strconv.FormatInt(h.ID, 10)+".md")
		if info, e := archive.Lstat(name); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("archive destination is not a regular file")
		}
		f, e := archive.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(h.Content)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *TaskService) archive(ctx context.Context, task store.Task, cleanup, force bool) (store.Task, error) {
	if task.Lifecycle == "archived" && !cleanup {
		return task, nil
	}
	if err := s.archiveHandoffs(ctx, task); err != nil {
		return task, err
	}
	if cleanup {
		_, statErr := os.Lstat(task.Worktree)
		if statErr != nil && !os.IsNotExist(statErr) {
			return task, statErr
		}
		if statErr == nil {
			if err := s.Repo.ValidateWorktree(ctx, task.Worktree, task.Branch); err != nil {
				return task, err
			}
			mergedHead := ""
			if task.PRState == "merged" {
				mergedHead = task.PRHeadSHA
			}
			if !force {
				if err := s.Repo.CleanupCheck(ctx, task.Worktree, task.Branch, task.BaseBranch, mergedHead); err != nil {
					return task, err
				}
			}
			head, err := s.Repo.PreserveHead(ctx, task.Worktree, task.ID)
			if err != nil {
				return task, err
			}
			task.ReopenSHA = head
			if task.MergeSHA != "" {
				task.ReopenSHA = task.MergeSHA
			}
			task.CleanupPending = true
			if err = s.Store.SaveWorkflow(ctx, task, "cleanup_started"); err != nil {
				return task, err
			}
			if err = s.Repo.RemoveWorktree(ctx, task.Worktree, task.Branch, head, force, mergedHead == head); err != nil {
				return task, err
			}
		} else {
			if task.ReopenSHA == "" {
				return task, fmt.Errorf("worktree is missing and no recovery commit was saved; retain the task until its branch is recovered")
			}
			if task.CleanupPending {
				mergedHead := ""
				if task.PRState == "merged" {
					mergedHead = task.PRHeadSHA
				}
				if err := s.Repo.FinishCleanup(ctx, task.Branch, task.ID, force, mergedHead); err != nil {
					return task, err
				}
			}
		}
	}
	task.Lifecycle = "archived"
	task.CleanupPending = false
	kind := "archived"
	if cleanup {
		kind = "cleaned"
	}
	return task, s.Store.SaveWorkflow(ctx, task, kind)
}

func (s *TaskService) reopen(ctx context.Context, task store.Task) (store.Task, error) {
	if task.Lifecycle != "archived" && !task.CleanupPending {
		return task, nil
	}
	// A completed PR belongs to the previous task cycle. Its URL remains in the
	// timeline; a cleaned worktree starts its new cycle on a fresh branch.
	if task.Lifecycle != "archived" {
		task.Lifecycle = "archived"
		if err := s.Store.SaveWorkflow(ctx, task, "cleanup_recovered"); err != nil {
			return task, err
		}
	}
	if task.PRState == "merged" || task.PRState == "closed" {
		if _, err := os.Lstat(task.Worktree); os.IsNotExist(err) {
			// A squash commit need not descend from the surviving remote branch.
			// Allocate a new branch for the next cycle without rewriting that remote.
			task.Branch += "-reopen-" + uuid.NewString()
		} else if err != nil {
			return task, err
		}
		task.PRNumber = 0
		task.PRURL = ""
		task.PRState = ""
		task.PRHeadSHA = ""
		task.MergeSHA = ""
		task.CIState = ""
		task.ReviewState = ""
		// Save the selected branch before Git creation so a failed reopen retries
		// the same branch and commit rather than allocating another cycle.
		if err := s.Store.SaveWorkflow(ctx, task, "reopen_prepared"); err != nil {
			return task, err
		}
	}
	if _, err := os.Lstat(task.Worktree); os.IsNotExist(err) {
		task.ReopenStep = 1
		if err = s.Store.SaveWorkflow(ctx, task, "reopen_provisioning"); err != nil {
			return task, err
		}
	} else if err != nil {
		return task, err
	}
	if err := s.Repo.RecreateWorktree(ctx, task.Worktree, task.Branch, task.ReopenSHA); err != nil {
		return task, err
	}
	if task.ReopenStep > 0 {
		spec := git.WorktreeSpec{Path: task.Worktree, Branch: task.Branch, Copy: s.Config.Worktree.Copy, Setup: s.Config.Worktree.Setup}
		if err := s.Repo.ProvisionWorktree(ctx, spec, task.ReopenStep-1, func(next int) error {
			task.ReopenStep = next + 1
			return s.Store.SaveWorkflow(ctx, task, "")
		}); err != nil {
			return task, err
		}
	}
	task.ReopenStep = 0
	task.Lifecycle = "active"
	if task.PRNumber != 0 {
		task.Lifecycle = "pr_open"
	}
	task.CleanupPending = false
	return task, s.Store.SaveWorkflow(ctx, task, "reopened")
}
