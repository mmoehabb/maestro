package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gofrs/flock"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
)

type TaskService struct {
	Config   config.Config
	Repo     git.Repo
	Project  store.Project
	Store    *store.Store
	LockPath string
	mu       sync.Mutex
	lock     *flock.Flock
}

type NewTask struct{ Title, Agent, Base, Prompt string }

func Slug(title string) string {
	var b strings.Builder
	separator := false
	for _, c := range strings.ToLower(strings.TrimSpace(title)) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(c)
			separator = false
		} else {
			separator = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	return b.String()
}

// Create serializes mutations with the future TUI's project lock. Listing does
// not acquire it, and remains available while a writer owns the project.
func (s *TaskService) Create(ctx context.Context, in NewTask) (store.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock != nil {
		return s.create(ctx, in)
	}
	lock := flock.New(s.LockPath)
	ok, err := lock.TryLock()
	if err != nil {
		return store.Task{}, err
	}
	if !ok {
		return store.Task{}, fmt.Errorf("this project is busy in another Maestro process")
	}
	defer lock.Close()
	return s.create(ctx, in)
}

func (s *TaskService) acquire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock != nil {
		return fmt.Errorf("this service already has an active runtime")
	}
	lock := flock.New(s.LockPath)
	ok, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("this project is busy in another Maestro process")
	}
	s.lock = lock
	return nil
}

func (s *TaskService) release() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

func (s *TaskService) Status(ctx context.Context, task store.Task) (git.Status, error) {
	return git.WorktreeStatus(ctx, task.Worktree, task.BaseBranch)
}

func (s *TaskService) create(ctx context.Context, in NewTask) (store.Task, error) {
	in.Title = strings.TrimSpace(in.Title)
	slug := Slug(in.Title)
	if slug == "" {
		return store.Task{}, fmt.Errorf("title must contain at least one ASCII letter or digit")
	}
	if in.Agent == "" {
		in.Agent = s.Config.DefaultAgent
	}
	if _, ok := s.Config.Agents[in.Agent]; !ok {
		return store.Task{}, fmt.Errorf("unknown agent %q", in.Agent)
	}
	if in.Base == "" {
		in.Base = s.Repo.DefaultBranch
	}
	tasks, err := s.List(ctx, true)
	if err != nil {
		return store.Task{}, err
	}
	for _, t := range tasks {
		if t.Slug == slug {
			return store.Task{}, fmt.Errorf("task %q already exists; choose a different title", slug)
		}
	}
	t := store.Task{
		ProjectID: s.Project.ID, Slug: slug, Title: in.Title, Goal: in.Prompt,
		Agent: in.Agent, Prompt: in.Prompt, BaseBranch: in.Base, Branch: s.Config.Worktree.BranchPrefix + slug,
		Worktree: filepath.Join(s.Config.Worktree.Root, s.Repo.Key(), slug),
	}
	if err := s.Repo.CreateWorktree(ctx, git.WorktreeSpec{
		Path: t.Worktree, Branch: t.Branch, Base: t.BaseBranch,
		Copy: s.Config.Worktree.Copy, Setup: s.Config.Worktree.Setup,
	}); err != nil {
		return store.Task{}, err
	}
	created, err := s.Store.CreateTask(ctx, t)
	if err != nil {
		return store.Task{}, fmt.Errorf("save task: %w; worktree retained at %s (branch %s)", err, t.Worktree, t.Branch)
	}
	return created, nil
}

func (s *TaskService) List(ctx context.Context, all bool) ([]store.Task, error) {
	return s.Store.Tasks(ctx, s.Project.ID, all)
}
