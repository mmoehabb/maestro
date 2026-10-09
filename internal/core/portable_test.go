package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func portableOpen(t *testing.T, root string, paths config.Paths) *core.TaskService {
	t.Helper()
	s, err := app.Open(context.Background(), root, paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Store.Close() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Agents["fake"] = config.Agent{Cmd: exe, New: []string{"-test.run=^TestAgentProcess$", "--", "new", "{{.SessionID}}", "{{.Prompt}}"}, Resume: []string{"-test.run=^TestAgentProcess$", "--", "resume", "{{.SessionID}}", "{{.Prompt}}"}, GenerateSessionID: true}
	s.Config.Activity.IdleAfter = "80ms"
	return s
}

func portablePaths(t *testing.T) config.Paths {
	t.Helper()
	data := t.TempDir()
	return config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data}
}

func sameDirectory(t *testing.T, a, b string) bool {
	t.Helper()
	infoA, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(infoA, infoB)
}

func checkpointCommit(t *testing.T, s *core.TaskService, task store.Task) {
	t.Helper()
	if _, err := s.Checkpoint(context.Background(), task.Slug); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "add", ".maestro/.gitignore", ".maestro/tasks")
	testutil.Git(t, task.Worktree, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "checkpoint")
}

func TestPortableTaskRoundTrip(t *testing.T) {
	t.Setenv("MAESTRO_TEST_HELPER", "1")
	ctx := context.Background()
	for _, checkoutTask := range []bool{false, true} {
		name := "remote-branch"
		if checkoutTask {
			name = "main-checkout"
		}
		t.Run(name, func(t *testing.T) {
			root, remote := testutil.Repo(t), t.TempDir()
			testutil.Git(t, remote, "init", "--bare", "-b", "main")
			testutil.Git(t, root, "remote", "add", "origin", remote)
			testutil.Git(t, root, "push", "origin", "main")
			s := portableOpen(t, root, portablePaths(t))
			task, err := s.Create(ctx, core.NewTask{Title: "Portable task", Agent: "fake", Prompt: "Repair login"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SetNotes(ctx, task.Slug, "TODO: preserve compatibility"); err != nil {
				t.Fatal(err)
			}
			session, err := s.Store.StartSession(ctx, task, "machine-a-native-session", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Store.ImportTurns(ctx, session.ID, []store.Turn{{SourceKey: "first", Role: "assistant", Content: "Decision: use a constant-time comparison.", TS: time.Now()}}); err != nil {
				t.Fatal(err)
			}
			if err = s.Store.EndSession(ctx, session.ID, 0); err != nil {
				t.Fatal(err)
			}
			checkpointCommit(t, s, task)
			if _, err = s.Workflow(ctx, task.Slug, "push", core.WorkflowOptions{}); err != nil {
				t.Fatal(err)
			}
			clone := filepath.Join(t.TempDir(), "clone")
			args := []string{"clone", remote, clone}
			if checkoutTask {
				args = []string{"clone", "--branch", task.Branch, remote, clone}
			}
			testutil.Git(t, root, args...)
			paths := portablePaths(t)
			b := portableOpen(t, clone, paths)
			list, err := b.List(ctx, false)
			if err != nil || len(list) != 1 {
				t.Fatal("discovery", list, err)
			}
			restored := list[0]
			if restored.Goal != "Repair login" || restored.Notes != "TODO: preserve compatibility" || restored.Worktree == task.Worktree {
				t.Fatal("lost portable metadata", restored)
			}
			if checkoutTask && !sameDirectory(t, restored.Worktree, clone) {
				t.Fatal("did not reuse main checkout", restored.Worktree)
			}
			h, err := b.Store.History(ctx, restored)
			if err != nil || len(h.Sessions) != 0 {
				t.Fatal("imported machine-specific sessions", h, err)
			}
			if err = b.DiscoverTasks(ctx); err != nil {
				t.Fatal(err)
			}
			list, _ = b.List(ctx, false)
			if len(list) != 1 {
				t.Fatal("duplicate restore")
			}
			r, err := b.OpenRuntime()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close() })
			preset := b.Config.Agents["fake"]
			unavailable := preset
			unavailable.Cmd = filepath.Join(t.TempDir(), "missing-agent")
			b.Config.Agents["fake"] = unavailable
			if _, err = r.Start(restored, 120, 32, false); err == nil {
				t.Fatal("missing agent started")
			}
			pending, err := b.Store.PendingHandoff(ctx, restored.ID)
			if err != nil || pending.ID == 0 {
				t.Fatal("missing agent lost pending handoff", err)
			}
			b.Config.Agents["fake"] = preset
			blocked := filepath.Join(restored.Worktree, ".maestro", "local", "handoff.md")
			if err = os.MkdirAll(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Start(restored, 120, 32, false); err == nil {
				t.Fatal("invalid handoff destination accepted")
			}
			if err = os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			pane, err := r.Start(restored, 120, 32, false)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, pane, term.Done, "ready new")
			brief, err := os.ReadFile(filepath.Join(restored.Worktree, ".maestro", "local", "handoff.md"))
			if err != nil || !strings.Contains(string(brief), "constant-time comparison") {
				t.Fatal("lost imported conversation", string(brief), err)
			}
			if string(brief) != pending.Content {
				t.Fatal("first launch changed the imported checkpoint")
			}
			launches, err := os.ReadFile(filepath.Join(restored.Worktree, ".maestro", "launches.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var argv []string
			if err = json.Unmarshal([]byte(strings.TrimSpace(string(launches))), &argv); err != nil {
				t.Fatal(err)
			}
			if argv[0] != "new" || argv[1] == session.NativeID || !strings.Contains(argv[2], "handoff.md") {
				t.Fatal("invalid continuation launch", argv)
			}
			if _, err = r.Checkpoint(ctx, restored); err != nil {
				t.Fatal("runtime checkpoint failed", err)
			}
			if err = r.Close(); err != nil {
				t.Fatal(err)
			}
			if err = b.SetNotes(ctx, restored.Slug, "Completed on machine B; TODO: final review"); err != nil {
				t.Fatal(err)
			}
			checkpointCommit(t, b, restored)
			testutil.Git(t, restored.Worktree, "push", "origin", "HEAD")
			testutil.Git(t, root, "fetch", "origin")
			if err = s.DiscoverTasks(ctx); err != nil {
				t.Fatal("return trip discovery", err)
			}
			back, err := s.Find(ctx, task.Slug)
			if err != nil || back.Notes != "Completed on machine B; TODO: final review" {
				t.Fatal("return trip lost context", back, err)
			}
			p, err := s.Store.Portable(ctx, back.ID)
			if err != nil || !p.Fresh {
				t.Fatal("return trip would resume stale native session", p, err)
			}
			testutil.Git(t, task.Worktree, "merge", "--ff-only", "origin/"+task.Branch)
			// The original database can checkpoint the returned task without
			// losing its local history or the newly imported continuation.
			if _, err = s.Checkpoint(ctx, back.Slug); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPortableDiscoveryTracksNewCodeWithoutNewContext(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	s := portableOpen(t, root, portablePaths(t))
	task, err := s.Create(ctx, core.NewTask{Title: "Code advance", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	checkpointCommit(t, s, task)
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, root, "clone", root, clone)
	b := portableOpen(t, clone, portablePaths(t))
	before, err := b.Find(ctx, task.Slug)
	if err != nil {
		t.Fatal(err)
	}
	old, err := b.Store.Portable(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "commit", "--allow-empty", "-m", "new code commit")
	testutil.Git(t, clone, "fetch", "origin")
	if err = b.DiscoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := b.Store.Portable(ctx, before.ID)
	if err != nil || old.SourceCommit == after.SourceCommit {
		t.Fatal("ignored latest branch tip", after, err)
	}
	if old.Checkpoint != after.Checkpoint {
		t.Fatal("changed unchanged context")
	}
}

func TestPortableCheckpointRefreshAndRetry(t *testing.T) {
	ctx := context.Background()
	s := portableOpen(t, testutil.Repo(t), portablePaths(t))
	task, err := s.Create(ctx, core.NewTask{Title: "Checkpoint retry", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	checkpointCommit(t, s, task)
	first, err := s.Store.Portable(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Checkpoint(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	same, err := s.Store.Portable(ctx, task.ID)
	if err != nil || same.Checkpoint != first.Checkpoint {
		t.Fatal("unchanged checkpoint churned", err)
	}
	if err = os.WriteFile(filepath.Join(task.Worktree, "new-code.txt"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Checkpoint(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Store.Portable(ctx, task.ID)
	if err != nil || changed.Checkpoint.Checkpoint == first.Checkpoint.Checkpoint || !strings.Contains(changed.Checkpoint.Handoff, "new-code.txt") {
		t.Fatal("checkpoint lost current Git state", changed, err)
	}
	if _, err = s.Workflow(ctx, task.Slug, "push", core.WorkflowOptions{}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatal("push accepted unpublished context", err)
	}
	path := filepath.Join(task.Worktree, changed.Checkpoint.Directory(), "handoff.md")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Checkpoint(ctx, task.Slug); err == nil {
		t.Fatal("accepted blocked checkpoint destination")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Checkpoint(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	retried, err := s.Store.Portable(ctx, task.ID)
	if err != nil || retried.Checkpoint != changed.Checkpoint {
		t.Fatal("retry changed checkpoint identity", err)
	}
}

func TestPortableMultipleTasksAndManualPrompt(t *testing.T) {
	t.Setenv("MAESTRO_TEST_HELPER", "1")
	ctx := context.Background()
	root := testutil.Repo(t)
	s := portableOpen(t, root, portablePaths(t))
	for _, name := range []string{"first", "second", "archived"} {
		task, err := s.Create(ctx, core.NewTask{Title: name, Agent: "fake", Prompt: "Continue " + name})
		if err != nil {
			t.Fatal(err)
		}
		if name == "archived" {
			if err = s.Archive(ctx, name); err != nil {
				t.Fatal(err)
			}
		}
		checkpointCommit(t, s, task)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, root, "clone", root, clone)
	b := portableOpen(t, clone, portablePaths(t))
	tasks, err := b.List(ctx, false)
	if err != nil || len(tasks) != 2 {
		t.Fatal("active task discovery", tasks, err)
	}
	all, err := b.List(ctx, true)
	if err != nil || len(all) != 3 {
		t.Fatal("archived task discovery", all, err)
	}
	preset := b.Config.Agents["fake"]
	preset.ManualPrompt = true
	preset.New = preset.New[:len(preset.New)-1]
	b.Config.Agents["fake"] = preset
	r, err := b.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	for _, task := range tasks {
		p, err := r.Start(task, 80, 24, false)
		if err != nil {
			t.Fatal(err)
		}
		awaitPane(t, p, term.Done, "ready new")
		launch, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "launches.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		var argv []string
		if err = json.Unmarshal([]byte(strings.TrimSpace(string(launch))), &argv); err != nil {
			t.Fatal(err)
		}
		if len(argv) != 2 {
			t.Fatal("injected prompt into manual agent", argv)
		}
		content, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "local", "handoff.md"))
		if err != nil || !strings.Contains(string(content), "Continue "+task.Slug) {
			t.Fatal("manual handoff unavailable", err)
		}
	}
}

func TestPortablePRBaseResolution(t *testing.T) {
	ctx := context.Background()
	for _, base := range []string{"HEAD", "refs/heads/main"} {
		t.Run(base, func(t *testing.T) {
			s := portableOpen(t, testutil.Repo(t), portablePaths(t))
			task, err := s.Create(ctx, core.NewTask{Title: "PR base", Agent: "fake", Base: base})
			if err != nil {
				t.Fatal(err)
			}
			checkpointCommit(t, s, task)
			draft, err := s.PRDescription(ctx, task, "")
			if base == "HEAD" {
				if err == nil || !strings.Contains(err.Error(), "specify the PR base") {
					t.Fatal("commit used as PR branch", draft, err)
				}
			} else if err != nil || draft.Base != "main" {
				t.Fatal("lost named base", draft, err)
			}
		})
	}
}

func TestPortableConflictsArchiveAndDeletion(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	s := portableOpen(t, root, portablePaths(t))
	task, err := s.Create(ctx, core.NewTask{Title: "Shared task", Agent: "fake", Prompt: "A portable goal"})
	if err != nil {
		t.Fatal(err)
	}
	checkpointCommit(t, s, task)
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, root, "clone", root, clone)
	b := portableOpen(t, clone, portablePaths(t))
	if err = b.SetNotes(ctx, task.Slug, "local work must survive"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetNotes(ctx, task.Slug, "new shared work"); err != nil {
		t.Fatal(err)
	}
	checkpointCommit(t, s, task)
	testutil.Git(t, clone, "fetch", "origin")
	if err = b.DiscoverTasks(ctx); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatal("silently overwrote local work", err)
	}
	local, _ := b.Find(ctx, task.Slug)
	if local.Notes != "local work must survive" {
		t.Fatal("local notes overwritten")
	}
	if err = b.RestoreTasks(ctx, "origin/"+task.Branch, true); err != nil {
		t.Fatal(err)
	}
	local, _ = b.Find(ctx, task.Slug)
	if local.Notes != "new shared work" {
		t.Fatal("explicit replacement failed")
	}
	if _, err = s.Workflow(ctx, task.Slug, "cleanup", core.WorkflowOptions{}); err == nil || !strings.Contains(err.Error(), "archive this portable task") {
		t.Fatal("cleanup discarded unpublished lifecycle", err)
	}
	if err = s.Archive(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	checkpointCommit(t, s, task)
	testutil.Git(t, clone, "fetch", "origin")
	if err = b.DiscoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	active, _ := b.List(ctx, false)
	if len(active) != 0 {
		t.Fatal("archived task restored as active")
	}
	if err = b.DeleteArchived(ctx, task.Slug); err != nil {
		t.Fatal(err)
	}
	if err = b.DiscoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := b.List(ctx, true)
	if len(all) != 0 {
		t.Fatal("deleted task resurrected")
	}
	if err = b.RestoreTasks(ctx, "origin/"+task.Branch, false); err != nil {
		t.Fatal(err)
	}
	if err = b.Reopen(ctx, task.Slug); err != nil {
		t.Fatal("reopen imported archived task", err)
	}
	active, _ = b.List(ctx, false)
	if len(active) != 1 {
		t.Fatal("reopen failed")
	}
}

func TestExplicitRestoreKeepsDivergentCheckoutUsable(t *testing.T) {
	t.Setenv("MAESTRO_TEST_HELPER", "1")
	ctx := context.Background()
	root := testutil.Repo(t)
	source := portableOpen(t, root, portablePaths(t))
	task, err := source.Create(ctx, core.NewTask{Title: "Diverged task", Agent: "fake", Prompt: "Continue work"})
	if err != nil {
		t.Fatal(err)
	}
	checkpointCommit(t, source, task)
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, root, "clone", root, clone)
	paths := portablePaths(t)
	dest := portableOpen(t, clone, paths)
	restored, err := dest.Find(ctx, task.Slug)
	if err != nil {
		t.Fatal(err)
	}
	p, err := dest.Store.Portable(ctx, restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = dest.Repo.RestoreWorktree(ctx, restored.Worktree, restored.Branch, p.SourceCommit, p.SourceRef); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, restored.Worktree, "-c", "user.name=Maestro Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "local change")
	testutil.Git(t, task.Worktree, "-c", "user.name=Maestro Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "remote change")
	testutil.Git(t, clone, "fetch", "origin")
	if err = dest.DiscoverTasks(ctx); err == nil || !strings.Contains(err.Error(), "conflicting checkpoints") {
		t.Fatal("divergent refs were silently chosen", err)
	}
	if err = dest.RestoreTasks(ctx, "origin/"+task.Branch, true); err != nil {
		t.Fatal(err)
	}
	// Opening a new service repeats discovery, as maestro ls/open would.
	reopened := portableOpen(t, clone, paths)
	listed, err := reopened.List(ctx, false)
	if err != nil || len(listed) != 1 {
		t.Fatal("explicit ref was not honored", listed, err)
	}
	saved, err := reopened.Store.Portable(ctx, listed[0].ID)
	if err != nil || saved.PinnedRef != "refs/remotes/origin/"+task.Branch {
		t.Fatal("selection not persisted", saved, err)
	}
	runtime, err := reopened.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	pane, err := runtime.Start(listed[0], 80, 24, false)
	if err != nil {
		_ = runtime.Close()
		t.Fatal("explicit selection did not restore access", err)
	}
	awaitPane(t, pane, term.Done, "ready new")
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, task.Worktree, "-c", "user.name=Maestro Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "next remote change")
	testutil.Git(t, clone, "fetch", "origin")
	if err = reopened.DiscoverTasks(ctx); err != nil {
		t.Fatal("pin did not follow selected ref", err)
	}
	updated, err := reopened.Store.Portable(ctx, listed[0].ID)
	if err != nil || updated.SourceCommit == saved.SourceCommit {
		t.Fatal("pin did not advance", updated, err)
	}
}

func TestCompletedPortableTaskReopensInMainCheckout(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{"merged", "closed"} {
		t.Run(state, func(t *testing.T) {
			root := testutil.Repo(t)
			source := portableOpen(t, root, portablePaths(t))
			task, err := source.Create(ctx, core.NewTask{Title: "Completed task", Agent: "fake"})
			if err != nil {
				t.Fatal(err)
			}
			checkpointCommit(t, source, task)
			clone := filepath.Join(t.TempDir(), "clone")
			testutil.Git(t, root, "clone", "--branch", task.Branch, root, clone)
			dest := portableOpen(t, clone, portablePaths(t))
			imported, err := dest.Find(ctx, task.Slug)
			if err != nil || !sameDirectory(t, imported.Worktree, clone) {
				t.Fatal("task was not in main checkout", imported, err)
			}
			imported.Lifecycle = "pr_open"
			imported.PRNumber = 42
			imported.PRURL = "https://example.invalid/pr/42"
			imported.PRState = "open"
			if err = dest.Store.SaveWorkflow(ctx, imported, "pr_opened"); err != nil {
				t.Fatal(err)
			}
			imported.Lifecycle = state
			imported.PRState = state
			if err = dest.Store.SaveWorkflow(ctx, imported, state); err != nil {
				t.Fatal(err)
			}
			if err = dest.Archive(ctx, task.Slug); err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(testutil.Git(t, clone, "rev-parse", "HEAD"))
			if err = dest.Reopen(ctx, task.Slug); err != nil {
				t.Fatal("main checkout reopen failed", err)
			}
			got, err := dest.Find(ctx, task.Slug)
			if err != nil || got.Lifecycle != "active" || got.PRNumber != 0 || got.PRState != "" || !sameDirectory(t, got.Worktree, clone) || got.Branch != task.Branch {
				t.Fatal("completed PR state not reset", got, err)
			}
			if now := strings.TrimSpace(testutil.Git(t, clone, "rev-parse", "HEAD")); now != head {
				t.Fatal("reopen changed main checkout")
			}
		})
	}
}
