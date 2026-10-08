package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

type WorkflowOptions struct {
	Title, Body, Base, ExpectedHead string
	BodySet                         bool
	Force, StopAgent                bool
}

// Workflow is the CLI entry point. Runtime uses the same operations under its
// project lock and per-task operation lock.
func (s *TaskService) Workflow(ctx context.Context, slug, action string, opts WorkflowOptions) (store.Task, error) {
	var result store.Task
	err := s.mutateTask(ctx, slug, func(t store.Task) error {
		var e error
		result, e = s.workflow(ctx, t, action, opts)
		return e
	})
	return result, err
}

func (r *Runtime) Workflow(ctx context.Context, task store.Task, action string, opts WorkflowOptions) (store.Task, error) {
	done, err := r.operation(task.ID)
	if err != nil {
		return task, err
	}
	defer done()
	op, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	task, err = r.Service.Find(op, task.Slug)
	if err != nil {
		return task, err
	}
	if action == "cleanup" || action == "archive" {
		if old := r.entry(task.ID); old != nil {
			select {
			case <-old.finished:
			default:
				if !opts.StopAgent {
					return task, fmt.Errorf("agent is running; confirm stop before archiving")
				}
				old.pane.Stop()
				<-old.finished
			}
			if old.saveErr != nil {
				if err = r.retryHistory(old); err != nil {
					return task, err
				}
			}
		}
	}
	result, err := r.Service.workflow(op, task, action, opts)
	if err == nil && result.Lifecycle == "archived" {
		r.mu.Lock()
		delete(r.panes, task.ID)
		r.mu.Unlock()
	}
	return result, err
}

func (s *TaskService) workflow(ctx context.Context, task store.Task, action string, opts WorkflowOptions) (store.Task, error) {
	switch action {
	case "archive":
		return s.archive(ctx, task, false, false)
	case "cleanup":
		return s.archive(ctx, task, true, opts.Force)
	case "reopen":
		return s.reopen(ctx, task)
	}
	if task.Lifecycle == "archived" || task.CleanupPending {
		return task, fmt.Errorf("task is archived or cleanup is incomplete; reopen or finish cleanup first")
	}
	if action == "push" || action == "pr" || action == "merge" {
		if err := s.Repo.ValidateWorktree(ctx, task.Worktree, task.Branch); err != nil {
			return task, err
		}
	}
	if action == "push" {
		if task.Lifecycle == "merged" || task.Lifecycle == "closed" {
			return task, fmt.Errorf("archive and reopen this completed task before pushing new work")
		}
		if err := git.Push(ctx, task.Worktree, task.Branch); err != nil {
			return task, err
		}
		if task.PRNumber == 0 {
			task.Lifecycle = "pushed"
		}
		err := s.Store.SaveWorkflow(ctx, task, "pushed")
		return task, err
	}
	repo, err := forge.ParseRemote(s.Repo.Remote)
	if err != nil {
		return task, err
	}
	if s.Forge == nil {
		return task, fmt.Errorf("GitHub provider is unavailable")
	}
	var pr *forge.PR
	if task.PRNumber != 0 {
		if reader, ok := s.Forge.(forge.PRReader); ok {
			pr, err = reader.GetPR(ctx, repo, task.PRNumber)
		} else {
			pr, err = s.Forge.PRForBranch(ctx, repo, task.Branch)
		}
	} else {
		pr, err = s.Forge.PRForBranch(ctx, repo, task.Branch)
	}
	if err != nil {
		return task, err
	}
	switch action {
	case "refresh":
		if pr == nil {
			if task.Lifecycle == "pushed" {
				status, e := s.Status(ctx, task)
				if e != nil {
					return task, e
				}
				if status.Ahead > 0 {
					task.Lifecycle = "active"
					err = s.Store.SaveWorkflow(ctx, task, "local_commits")
				}
			}
			return task, err
		}
	case "pr":
		if pr == nil {
			in, e := s.PRDescription(ctx, task, opts.Base)
			if e != nil {
				return task, e
			}
			if opts.Title != "" {
				in.Title = opts.Title
			}
			if opts.BodySet || opts.Body != "" {
				in.Body = opts.Body
			}
			if err = git.Push(ctx, task.Worktree, task.Branch); err != nil {
				return task, err
			}
			task.Lifecycle = "pushed"
			if err = s.Store.SaveWorkflow(ctx, task, "pushed"); err != nil {
				return task, err
			}
			pr, err = s.Forge.CreatePR(ctx, repo, in)
			if err != nil {
				return task, err
			}
			task.PRBase = in.Base
		}
	case "merge":
		if pr == nil {
			return task, fmt.Errorf("task has no open PR")
		}
		if pr.State == "open" {
			head, e := git.Head(ctx, task.Worktree)
			if e != nil {
				return task, e
			}
			if opts.ExpectedHead != "" && opts.ExpectedHead != pr.HeadSHA {
				return task, fmt.Errorf("PR head changed after confirmation; review and retry")
			}
			if head != pr.HeadSHA {
				return task, fmt.Errorf("local HEAD differs from the PR; push or update the worktree before merging")
			}
			if merger, ok := s.Forge.(forge.HeadMerger); ok {
				pr, err = merger.MergeWithHead(ctx, repo, pr.Number, s.Config.Git.MergeMethod, pr.HeadSHA)
			} else {
				err = s.Forge.Merge(ctx, repo, pr.Number, s.Config.Git.MergeMethod)
				pr.State = "merged"
			}
			if err != nil {
				return task, err
			}
		} else if pr.State != "merged" {
			return task, fmt.Errorf("PR is closed without merging")
		}
	default:
		return task, fmt.Errorf("unknown workflow action %q", action)
	}
	// Persist discovery before a terminal transition to keep lifecycle ordering.
	if pr != nil && pr.State != "open" && (task.Lifecycle == "new" || task.Lifecycle == "active" || task.Lifecycle == "pushed") {
		task.PRNumber = pr.Number
		task.PRURL = pr.URL
		task.PRState = "open"
		task.Lifecycle = "pr_open"
		if err = s.Store.SaveWorkflow(ctx, task, "pr_opened"); err != nil {
			return task, err
		}
	}
	return s.savePR(ctx, task, pr)
}

func (s *TaskService) savePR(ctx context.Context, task store.Task, pr *forge.PR) (store.Task, error) {
	before := task
	task.PRNumber = pr.Number
	task.PRURL = pr.URL
	task.PRState = pr.State
	task.PRHeadSHA = pr.HeadSHA
	if pr.State == "open" {
		task.Lifecycle = "pr_open"
		task.CIState = pr.CI
		task.ReviewState = pr.Review
	} else {
		task.Lifecycle = pr.State
		if pr.State == "merged" {
			task.MergeSHA = pr.MergeSHA
		}
	}
	if before == task {
		return task, nil
	}
	kind := "pr_status"
	if before.PRNumber == 0 {
		kind = "pr_opened"
	}
	if before.Lifecycle != task.Lifecycle && task.Lifecycle == "merged" {
		kind = "merged"
	}
	if before.Lifecycle != task.Lifecycle && task.Lifecycle == "closed" {
		kind = "closed"
	}
	return task, s.Store.SaveWorkflow(ctx, task, kind)
}

func (s *TaskService) PRDescription(ctx context.Context, task store.Task, base string) (forge.NewPR, error) {
	if base == "" {
		base = task.PRBase
	}
	if base == "" {
		var err error
		base, err = s.Repo.PRBase(ctx, task.BaseBranch)
		if err != nil {
			return forge.NewPR{}, err
		}
	}
	commits, err := git.Commits(ctx, task.Worktree, task.BaseBranch)
	if err != nil {
		return forge.NewPR{}, err
	}
	history, err := s.Store.History(ctx, task)
	if err != nil {
		return forge.NewPR{}, err
	}
	var body strings.Builder
	body.WriteString("## Goal\n\n" + task.Goal + "\n")
	if task.Notes != "" {
		body.WriteString("\n## Notes\n\n" + task.Notes + "\n")
	}
	if len(history.Handoffs) > 0 {
		summary := []rune(history.Handoffs[len(history.Handoffs)-1].Content)
		if len(summary) > 4000 {
			summary = append(summary[:4000], []rune("\n… (trimmed)")...)
		}
		body.WriteString("\n## Handoff summary\n\n" + string(summary) + "\n")
	}
	body.WriteString("\n## Commits\n\n" + commits + "\n")
	return forge.NewPR{Title: task.Title, Body: body.String(), Head: task.Branch, Base: base}, nil
}
