package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/testutil"
)

type fakeForge struct {
	created   forge.NewPR
	pr        *forge.PR
	mergeSHA  string
	createErr error
}

func (f *fakeForge) PRForBranch(context.Context, forge.Repo, string) (*forge.PR, error) {
	if f.pr == nil {
		return nil, nil
	}
	p := *f.pr
	return &p, nil
}

func (f *fakeForge) GetPR(context.Context, forge.Repo, int) (*forge.PR, error) {
	p := *f.pr
	return &p, nil
}

func (f *fakeForge) CreatePR(_ context.Context, _ forge.Repo, in forge.NewPR) (*forge.PR, error) {
	f.created = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.pr = &forge.PR{Number: 7, URL: "https://github.com/o/r/pull/7", State: "open", CI: "success", Review: "approved"}
	return f.pr, nil
}

func (f *fakeForge) Merge(_ context.Context, _ forge.Repo, _ int, method string) error {
	_, err := f.MergeWithHead(context.Background(), forge.Repo{}, 0, method, f.pr.HeadSHA)
	return err
}

func (f *fakeForge) MergeWithHead(_ context.Context, _ forge.Repo, _ int, method, head string) (*forge.PR, error) {
	if method != "squash" || head != f.pr.HeadSHA {
		return nil, fmt.Errorf("unexpected merge")
	}
	f.pr.State = "merged"
	f.pr.MergeSHA = f.mergeSHA
	p := *f.pr
	return &p, nil
}
func (*fakeForge) WebURL(pr *forge.PR) string { return pr.URL }

func forgeService(t *testing.T) (*core.TaskService, *fakeForge, store.Task) {
	t.Helper()
	ctx := context.Background()
	repo := testutil.Repo(t)
	data := t.TempDir()
	remote := t.TempDir()
	testutil.Git(t, remote, "init", "--bare")
	testutil.Git(t, repo, "remote", "add", "origin", remote)
	testutil.Git(t, repo, "push", "-u", "origin", "main")
	s, err := app.Open(ctx, repo, config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Store.Close() })
	f := &fakeForge{}
	s.Forge = f
	s.Repo.Remote = "https://github.com/o/r.git"
	task, err := s.Create(ctx, core.NewTask{Title: "Forge task"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(task.Worktree, "feature"), []byte("feature"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "add", "feature")
	testutil.Git(t, task.Worktree, "commit", "-m", "feature")
	return s, f, task
}

func TestForgeLifecycleCleanupAndReopen(t *testing.T) {
	s, f, task := forgeService(t)
	ctx := context.Background()
	run := func(action string, opts core.WorkflowOptions) {
		t.Helper()
		var err error
		task, err = s.Workflow(ctx, task.Slug, action, opts)
		if err != nil {
			t.Fatal(action, err)
		}
	}
	run("push", core.WorkflowOptions{})
	if task.Lifecycle != "pushed" {
		t.Fatal(task)
	}
	run("pr", core.WorkflowOptions{Body: "reviewed"})
	if task.Lifecycle != "pr_open" {
		t.Fatal(task)
	}
	head := strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD"))
	f.pr.HeadSHA = head
	// A process restart must preserve the PR lifecycle.
	session, err := s.Store.StartSession(ctx, task, "native", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.SaveScrollback(ctx, session.ID, "retained conversation"); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.PrepareHandoff(ctx, task.ID, session.ID, "codex", "retained handoff"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Find(ctx, task.Slug)
	if err != nil || saved.Lifecycle != "pr_open" {
		t.Fatal(saved, err)
	}
	testutil.Git(t, s.Repo.Root, "merge", "--squash", task.Branch)
	testutil.Git(t, s.Repo.Root, "commit", "-m", "squash")
	f.mergeSHA = strings.TrimSpace(testutil.Git(t, s.Repo.Root, "rev-parse", "HEAD"))
	if _, err = s.Workflow(ctx, task.Slug, "merge", core.WorkflowOptions{ExpectedHead: "stale"}); err == nil {
		t.Fatal("merged changed head")
	}
	run("merge", core.WorkflowOptions{ExpectedHead: head})
	if task.Lifecycle != "merged" {
		t.Fatal(task)
	}
	dirty := filepath.Join(task.Worktree, "uncommitted")
	if err = os.WriteFile(dirty, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{}); err == nil {
		t.Fatal("deleted dirty worktree")
	}
	if _, err = os.Stat(dirty); err != nil {
		t.Fatal("lost local file")
	}
	if err = os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	run("cleanup", core.WorkflowOptions{})
	if task.Lifecycle != "archived" {
		t.Fatal(task)
	}
	if _, err = os.Stat(task.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree remains", err)
	}
	h, err := s.History(ctx, task.Slug)
	if err != nil || len(h.Turns) != 1 || len(h.Handoffs) != 1 {
		t.Fatal(h, err)
	}
	archive, err := os.ReadFile(filepath.Join(s.DataDir, "archive", s.Repo.Key(), task.Slug, fmt.Sprintf("handoff-%d.md", h.Handoffs[0].ID)))
	if err != nil || string(archive) != "retained handoff" {
		t.Fatal(string(archive), err)
	}
	previousBranch := task.Branch
	run("reopen", core.WorkflowOptions{})
	if task.Lifecycle != "active" || task.PRNumber != 0 {
		t.Fatal(task)
	}
	reopened := strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD"))
	if reopened != f.mergeSHA {
		t.Fatalf("reopened %s, wanted merge %s", reopened, f.mergeSHA)
	}
	if task.Branch == previousBranch {
		t.Fatal("completed task reused the squash-merged source branch")
	}
	// GitHub can retain the old source branch, whose tip is not an ancestor
	// of the squash commit. Both push and PR creation must work for the new cycle.
	run("push", core.WorkflowOptions{})
	if err = os.WriteFile(filepath.Join(task.Worktree, "next-cycle"), []byte("next"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "add", "next-cycle")
	testutil.Git(t, task.Worktree, "commit", "-m", "next cycle")
	f.pr = nil
	run("pr", core.WorkflowOptions{})
	if task.Lifecycle != "pr_open" {
		t.Fatal(task)
	}
	remote := strings.Fields(testutil.Git(t, task.Worktree, "ls-remote", "origin", "refs/heads/"+previousBranch))
	if len(remote) != 2 || remote[0] != head {
		t.Fatal("previous remote branch changed", remote)
	}
}

func TestReopenRetainedPRChangedWhileArchived(t *testing.T) {
	for _, state := range []string{"open", "merged", "closed"} {
		t.Run(state, func(t *testing.T) {
			s, f, task := forgeService(t)
			ctx := context.Background()
			var err error
			task, err = s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Archive(ctx, task.Slug); err != nil {
				t.Fatal(err)
			}
			f.pr.State = state
			task, err = s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{})
			if err != nil || task.Lifecycle != "pr_open" || task.PRNumber != f.pr.Number {
				t.Fatal(task, err)
			}
			task, err = s.Workflow(ctx, task.Slug, "refresh", core.WorkflowOptions{})
			want := state
			if state == "open" {
				want = "pr_open"
			}
			if err != nil || task.Lifecycle != want {
				t.Fatal(task, err)
			}
			if err = s.Repo.ValidateWorktree(ctx, task.Worktree, task.Branch); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRefreshRepairsPreviouslyReopenedPRLifecycle(t *testing.T) {
	s, f, task := forgeService(t)
	ctx := context.Background()
	var err error
	task, err = s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Archive(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	// Emulate the old reopen behavior, which left the retained PR on active.
	task.Lifecycle = "active"
	if err = s.Store.SaveWorkflow(ctx, task, "reopened"); err != nil {
		t.Fatal(err)
	}
	f.pr.State = "merged"
	task, err = s.Workflow(ctx, task.Slug, "refresh", core.WorkflowOptions{})
	if err != nil || task.Lifecycle != "merged" {
		t.Fatal(task, err)
	}
}

func TestFailedReopenRetriesPreparedBranch(t *testing.T) {
	s, f, task := forgeService(t)
	ctx := context.Background()
	var err error
	task, err = s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD"))
	f.pr.HeadSHA = head
	f.pr.State = "merged"
	f.pr.MergeSHA = head
	task, err = s.Workflow(ctx, task.Slug, "refresh", core.WorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldBranch := task.Branch
	task.ReopenSHA = "missing-commit"
	if err = s.Store.SaveWorkflow(ctx, task, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{}); err == nil {
		t.Fatal("reopen unexpectedly resolved the missing commit")
	}
	task, err = s.Find(ctx, task.Slug)
	if err != nil || task.Lifecycle != "archived" || task.Branch == oldBranch || task.PRNumber != 0 {
		t.Fatal(task, err)
	}
	preparedBranch := task.Branch
	task.ReopenSHA = head
	if err = s.Store.SaveWorkflow(ctx, task, ""); err != nil {
		t.Fatal(err)
	}
	task, err = s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{})
	if err != nil || task.Branch != preparedBranch || task.Lifecycle != "active" {
		t.Fatal(task, err)
	}
}

func TestCleanupRefusesUnpushedAndCanRecoverMissingWorktree(t *testing.T) {
	s, _, task := forgeService(t)
	ctx := context.Background()
	if _, err := s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{}); err == nil {
		t.Fatal("removed unpushed commits")
	}
	// Emulate interruption after writing the durable cleanup intent and deleting
	// the worktree, before the final archive transaction.
	head, err := s.Repo.PreserveHead(ctx, task.Worktree, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.CleanupPending = true
	task.ReopenSHA = head
	if err = s.Store.SaveWorkflow(ctx, task, "cleanup_started"); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, s.Repo.Root, "worktree", "remove", task.Worktree)
	task, err = s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{Force: true})
	if err != nil || task.Lifecycle != "archived" {
		t.Fatal(task, err)
	}
	if _, err = s.Workflow(ctx, task.Slug, "reopen", core.WorkflowOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testutil.Git(t, task.Worktree, "rev-parse", "HEAD")); got != head {
		t.Fatal(got, head)
	}
}

func TestForgeFailureAndProjectLock(t *testing.T) {
	s, f, task := forgeService(t)
	ctx := context.Background()
	f.createErr = fmt.Errorf("permission denied")
	if _, err := s.Workflow(ctx, task.Slug, "pr", core.WorkflowOptions{}); err == nil {
		t.Fatal("missing error")
	}
	saved, err := s.Find(ctx, task.Slug)
	if err != nil || saved.Lifecycle != "pushed" || saved.PRNumber != 0 {
		t.Fatal(saved, err)
	}
	// Disable polling so this test is exclusively about ownership of the lock.
	s.Repo.Remote = ""
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, action := range []string{"push", "pr", "merge", "cleanup", "reopen"} {
		if _, err = s.Workflow(ctx, task.Slug, action, core.WorkflowOptions{}); err == nil || !strings.Contains(err.Error(), "busy") {
			t.Fatal(action, err)
		}
	}
}
