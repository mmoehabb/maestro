package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestCleanupCyclesKeepUnpushedRecoveryCommits(t *testing.T) {
	s, f, task := forgeService(t)
	ctx := context.Background()
	var err error
	task, err = s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.pr.HeadSHA = strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD"))
	testutil.Git(t, s.Repo.Root, "merge", "--squash", task.Branch)
	testutil.Git(t, s.Repo.Root, "commit", "-m", "squash")
	f.pr.State = "merged"
	f.pr.MergeSHA = strings.TrimSpace(testutil.Git(t, s.Repo.Root, "rev-parse", "HEAD"))
	if _, err = s.Workflow(ctx, task.Slug, "refresh", core.WorkflowOptions{}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "commit", "--allow-empty", "-m", "unpushed after merge")
	protected := strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD"))
	if _, err = s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, s.Repo.Root, "reflog", "expire", "--expire=now", "--all")
	testutil.Git(t, s.Repo.Root, "gc", "--prune=now")
	testutil.Git(t, s.Repo.Root, "cat-file", "-e", protected+"^{commit}")
	refs := testutil.Git(t, s.Repo.Root, "for-each-ref", "--format=%(objectname)", "refs/maestro/recovery/")
	if !strings.Contains(refs, protected) {
		t.Fatal("previous cycle lost its recovery reference")
	}
}

func TestPRBodyOmissionAndExplicitEmpty(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		s, f, task := forgeService(t)
		ctx := context.Background()
		if err := s.SetNotes(ctx, task.Slug, "private notes deliberately removed from description"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{BodySet: explicit}); err != nil {
			t.Fatal(err)
		}
		if explicit && f.created.Body != "" {
			t.Fatal("empty body was replaced by generated notes")
		}
		if !explicit && !strings.Contains(f.created.Body, "private notes") {
			t.Fatal("omitted body was not generated")
		}
	}
}

func TestReopenProvisionsAndResumesFailedSetup(t *testing.T) {
	s, _, task := forgeService(t)
	ctx := context.Background()
	if _, err := s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", ".env.local"} {
		if err := os.WriteFile(filepath.Join(s.Repo.Root, name), []byte("env=value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.Config.Worktree.Setup = []string{"git rev-parse --show-toplevel >> setup-path", "exit 7"}
	if _, err := s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{}); err == nil {
		t.Fatal("failed setup activated the task")
	}
	saved, err := s.Find(ctx, task.Slug)
	if err != nil || saved.Lifecycle != "archived" || saved.ReopenStep == 0 {
		t.Fatal(saved, err)
	}
	for _, name := range []string{".env", ".env.local"} {
		b, e := os.ReadFile(filepath.Join(task.Worktree, name))
		if e != nil || string(b) != "env=value\n" {
			t.Fatal(name, string(b), e)
		}
	}
	// A retry must preserve copies and completed setup, including local edits.
	if err = os.WriteFile(filepath.Join(task.Worktree, ".env"), []byte("local edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Config.Worktree.Setup[1] = "git rev-parse --show-toplevel > recovered"
	if _, err = s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{}); err != nil {
		t.Fatal(err)
	}
	saved, err = s.Find(ctx, task.Slug)
	if err != nil || saved.Lifecycle != "active" || saved.ReopenStep != 0 {
		t.Fatal(saved, err)
	}
	b, err := os.ReadFile(filepath.Join(task.Worktree, ".env"))
	if err != nil || string(b) != "local edit" {
		t.Fatal("retry overwrote copied file", err)
	}
	if err = s.Archive(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	if err = s.Reopen(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(task.Worktree, "setup-path"))
	if err != nil || len(strings.Split(strings.TrimSpace(string(b)), "\n")) != 1 {
		t.Fatal("reopen repeated completed setup", string(b), err)
	}
	if _, err = os.Stat(filepath.Join(task.Worktree, "recovered")); err != nil {
		t.Fatal(err)
	}
}

func TestPollerArchiveSurvivesFullEventBuffer(t *testing.T) {
	s, f, task := forgeService(t)
	ctx := context.Background()
	var err error
	task, err = s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.pr.HeadSHA = strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD"))
	f.pr.State = "merged"
	f.pr.MergeSHA = f.pr.HeadSHA
	if _, err = s.Workflow(ctx, task.Slug, "refresh", core.WorkflowOptions{}); err != nil {
		t.Fatal(err)
	}
	s.Config.Git.Cleanup = "auto"
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for range cap(r.Events) {
		r.Events <- core.Event{}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		saved, e := s.Find(ctx, task.Slug)
		if e != nil {
			t.Fatal(e)
		}
		if saved.Lifecycle == "archived" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("poller did not archive the task")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = os.Stat(task.Worktree); !os.IsNotExist(err) {
		t.Fatal("cleanup did not remove worktree", err)
	}
	for {
		select {
		case event := <-r.Events:
			if event.Task != nil && event.Task.ID == task.ID && event.Task.Lifecycle == "archived" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("persisted archive notification was lost")
		}
	}
}
