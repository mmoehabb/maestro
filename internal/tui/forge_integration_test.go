package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/testutil"
)

type workflowForge struct {
	t    *testing.T
	repo git.Repo
	task store.Task
	pr   *forge.PR
	body string
}

func (f *workflowForge) PRForBranch(context.Context, forge.Repo, string) (*forge.PR, error) {
	return f.pr, nil
}
func (f *workflowForge) GetPR(context.Context, forge.Repo, int) (*forge.PR, error) { return f.pr, nil }
func (f *workflowForge) CreatePR(_ context.Context, _ forge.Repo, in forge.NewPR) (*forge.PR, error) {
	f.body = in.Body
	head := strings.TrimSpace(testutil.Git(f.t, f.task.Worktree, "rev-parse", "HEAD"))
	f.pr = &forge.PR{Number: 42, URL: "https://github.com/owner/repo/pull/42", State: "open", CI: "success", Review: "approved", HeadSHA: head}
	return f.pr, nil
}

func (f *workflowForge) Merge(ctx context.Context, repo forge.Repo, number int, method string) error {
	_, err := f.MergeWithHead(ctx, repo, number, method, f.pr.HeadSHA)
	return err
}

func (f *workflowForge) MergeWithHead(_ context.Context, _ forge.Repo, _ int, method, head string) (*forge.PR, error) {
	if method != "squash" || head != f.pr.HeadSHA {
		f.t.Fatal("merge confirmation did not preserve method/head")
	}
	testutil.Git(f.t, f.repo.Root, "merge", "--squash", f.task.Branch)
	testutil.Git(f.t, f.repo.Root, "commit", "-m", "squash")
	f.pr.State = "merged"
	f.pr.MergeSHA = strings.TrimSpace(testutil.Git(f.t, f.repo.Root, "rev-parse", "HEAD"))
	return f.pr, nil
}
func (*workflowForge) WebURL(pr *forge.PR) string { return pr.URL }

func TestTUIForgeLifecycleAndEmptyDescription(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	remote := t.TempDir()
	testutil.Git(t, remote, "init", "--bare")
	testutil.Git(t, root, "remote", "add", "origin", remote)
	testutil.Git(t, root, "push", "-u", "origin", "main")
	repo, err := git.Discover(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	repo.Remote = "https://github.com/owner/repo.git"
	data := t.TempDir()
	cfg, err := config.Load(config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data}, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(data, "maestro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project, err := db.EnsureProject(ctx, store.Project{Root: root, Remote: repo.Remote, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	s := &core.TaskService{Config: cfg, Repo: repo, Store: db, Project: project, DataDir: data, LockPath: filepath.Join(data, "project.lock")}
	// Inject the provider after opening the runtime so this deterministic action
	// test has no background poller. Poller delivery has separate integration coverage.
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	task, err := r.Create(ctx, core.NewTask{Title: "UI workflow"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Lifecycle != "new" {
		t.Fatal(task)
	}
	testutil.Git(t, task.Worktree, "config", "--file", "feature", "test.value", "new")
	testutil.Git(t, task.Worktree, "add", "feature")
	testutil.Git(t, task.Worktree, "commit", "-m", "feature content")
	session, err := db.StartSession(ctx, task, "native", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveScrollback(ctx, session.ID, "retained UI workflow history"); err != nil {
		t.Fatal(err)
	}
	f := &workflowForge{t: t, repo: repo, task: task}
	s.Forge = f
	m := New(s, r, "")
	m.loaded = true
	m.tabs = []tab{{task: task}}
	apply := func(cmd tea.Cmd) {
		t.Helper()
		if cmd == nil {
			t.Fatal("missing action command")
		}
		m.Update(cmd())
	}
	apply(m.openForge("push"))
	if m.tabs[0].task.Lifecycle != "pushed" {
		t.Fatal(m.tabs[0].task)
	}
	apply(m.openForge("pr"))
	if m.forgeUI == nil || m.forgeUI.busy {
		t.Fatal("PR editor did not open")
	}
	m.forgeUI.body.SetValue("")
	apply(m.forgeKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}))
	if f.body != "" {
		t.Fatal("cleared editor content was replaced")
	}
	if !strings.Contains(m.View().Content, "#42") {
		t.Fatal("PR badge was not updated")
	}
	apply(m.openForge("merge"))
	apply(m.forgeKey(tea.KeyPressMsg{Code: 'y'}))
	if m.tabs[0].task.Lifecycle != "merged" {
		t.Fatal(m.tabs[0].task)
	}
	m.Update(tickMsg(time.Now()))
	if m.forgeUI == nil || m.forgeUI.action != "cleanup" {
		t.Fatal("cleanup was not offered")
	}
	apply(m.forgeKey(tea.KeyPressMsg{Code: 'c'}))
	if len(m.tabs) != 0 {
		t.Fatal("archived tab remains visible")
	}
	saved, err := s.Find(ctx, task.Slug)
	if err != nil || saved.Lifecycle != "archived" {
		t.Fatal(saved, err)
	}
	history, err := s.History(ctx, task.Slug)
	if err != nil || len(history.Turns) != 1 {
		t.Fatal(history, err)
	}
}
