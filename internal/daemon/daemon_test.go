package daemon_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/daemon"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestDaemonAgentHelper(t *testing.T) {
	if os.Getenv("MAESTRO_DAEMON_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			fmt.Printf("pid=%d\r\n", os.Getpid())
			os.Exit(testutil.FakeAgent(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestDetachReattachAndCLIMutations(t *testing.T) {
	t.Setenv("MAESTRO_DAEMON_HELPER", "1")
	ctx := context.Background()
	dir := testutil.Repo(t)
	data := t.TempDir()
	paths := config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data}
	s, err := app.Open(ctx, dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Store.Close() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Agents["fake"] = config.Agent{Cmd: exe, New: []string{"-test.run=^TestDaemonAgentHelper$", "--", "new", "{{.SessionID}}"}, Resume: []string{"-test.run=^TestDaemonAgentHelper$", "--", "resume", "{{.SessionID}}"}, GenerateSessionID: true}
	var tasks []store.Task
	for _, title := range []string{"one", "two", "three"} {
		task, e := s.Create(ctx, core.NewTask{Title: title, Agent: "fake"})
		if e != nil {
			t.Fatal(e)
		}
		tasks = append(tasks, task)
	}
	runtime, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	address, err := daemon.Endpoint(data, s.Repo.Key())
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	server := daemon.Server{Runtime: runtime}
	go func() { done <- server.Serve(serverCtx, address) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	eventually(t, func() bool { _, e := daemon.Probe(ctx, address); return e == nil })
	client, err := daemon.Attach(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	if other, e := daemon.Attach(ctx, address); e == nil {
		_ = other.Close()
		t.Fatal("second TUI attached")
	}
	var panes []*term.Pane
	for _, task := range tasks {
		p, e := client.Start(task, 80, 24, false)
		if e != nil {
			t.Fatal(e)
		}
		panes = append(panes, p)
		eventually(t, func() bool { return strings.Contains(p.Snapshot(true).Screen, "ready new") })
	}
	before := runtime.Panes()
	// Start work, then disconnect while the agent is still producing output.
	panes[0].Paste("detached work")
	panes[0].Key(uv.Key{Code: uv.KeyEnter}, false)

	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	client = nil
	eventually(t, func() bool {
		for _, info := range runtime.Panes() {
			if info.TaskID == tasks[0].ID {
				screen := info.Pane.Snapshot(true).Screen
				return strings.Contains(screen, "detached work") && strings.Contains(screen, "done")
			}
		}
		return false
	})
	eventually(t, func() bool {
		status, e := daemon.Probe(ctx, address)
		return e == nil && !status.Attached && status.Agents == 3
	})
	other, err := app.Open(ctx, dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Store.Close()
	if other.Remote == nil {
		t.Fatal("CLI did not route to daemon")
	}
	if err = other.SetNotes(ctx, tasks[0].Slug, "written while detached"); err != nil {
		t.Fatal(err)
	}
	if err = other.Rename(ctx, tasks[0].Slug, "New display title"); err != nil {
		t.Fatal(err)
	}
	current, err := other.Find(ctx, tasks[0].Slug)
	if err != nil || current.Title != "New display title" || current.Branch != tasks[0].Branch || current.Worktree != tasks[0].Worktree {
		t.Fatal(current, err)
	}
	if _, err = other.Create(ctx, core.NewTask{Title: "four", Agent: "fake"}); err != nil {
		t.Fatal(err)
	}
	client, err = daemon.Attach(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		p, e := client.Start(task, 100, 30, false)
		if e != nil {
			t.Fatal(e)
		}
		eventually(t, func() bool { return strings.Contains(p.Snapshot(true).Screen, "ready new") })
		history, e := s.History(ctx, task.Slug)
		if e != nil || len(history.Sessions) != 1 {
			t.Fatalf("reattach relaunched agent: %+v %v", history.Sessions, e)
		}
		launches, e := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "launches.jsonl"))
		if e != nil || strings.Count(string(launches), "\n") != 1 {
			t.Fatalf("duplicate launch: %s %v", launches, e)
		}
		if task.ID == tasks[0].ID {
			eventually(t, func() bool { return strings.Contains(p.Snapshot(true).Screen, "done") })
		}
	}
	after := runtime.Panes()
	for _, old := range before {
		found := false
		for _, now := range after {
			if old.TaskID == now.TaskID && old.Generation == now.Generation && old.Pane == now.Pane {
				found = true
			}
		}
		if !found {
			t.Fatal("process replaced across detach")
		}
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	client = nil
	if err = (daemon.Remote{Address: address}).Call(ctx, "shutdown", core.Request{}, nil); err != nil {
		t.Fatal(err)
	}
}

// A provider-side challenge exercises the same context used by Git askpass.
type promptingForge struct{}

func (promptingForge) PRForBranch(ctx context.Context, _ forge.Repo, _ string) (*forge.PR, error) {
	value, err := git.PromptCredential(ctx, "Test key passphrase:")
	if err != nil {
		return nil, err
	}
	if value != " spaces and symbols $! " {
		return nil, fmt.Errorf("credential value changed")
	}
	return nil, nil
}

func (promptingForge) CreatePR(context.Context, forge.Repo, forge.NewPR) (*forge.PR, error) {
	return nil, fmt.Errorf("unused")
}

func (promptingForge) Merge(context.Context, forge.Repo, int, string) error {
	return fmt.Errorf("unused")
}
func (promptingForge) WebURL(*forge.PR) string { return "" }
func TestCredentialChallengeAndCancellation(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	paths := config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data}
	s, err := app.Open(ctx, testutil.Repo(t), paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Store.Close() })
	task, err := s.Create(ctx, core.NewTask{Title: "credentials"})
	if err != nil {
		t.Fatal(err)
	}
	s.Repo.Remote = "https://github.com/example/project.git"
	s.Forge = nil
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	s.Forge = promptingForge{}
	address, err := daemon.Endpoint(data, s.Repo.Key())
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	server := daemon.Server{Runtime: r}
	go func() { done <- server.Serve(serveCtx, address) }()
	t.Cleanup(func() { cancel(); <-done; _ = r.Close() })
	eventually(t, func() bool { _, err := daemon.Probe(ctx, address); return err == nil })
	remote := daemon.Remote{Address: address}
	called := false
	promptCtx := git.WithCredentialPrompt(ctx, func(_ context.Context, prompt string) (string, error) {
		called = true
		if prompt != "Test key passphrase:" {
			t.Error(prompt)
		}
		return " spaces and symbols $! ", nil
	})
	var result store.Task
	if err = remote.Call(promptCtx, "workflow", core.Request{Task: task, Action: "refresh"}, &result); err != nil || !called {
		t.Fatal(err, called)
	}
	canceled := git.WithCredentialPrompt(ctx, func(context.Context, string) (string, error) { return "", git.ErrCredentialCanceled })
	if err = remote.Call(canceled, "workflow", core.Request{Task: task, Action: "refresh"}, &result); err == nil {
		t.Fatal("cancellation accepted")
	}
	if err = remote.Call(ctx, "workflow", core.Request{Task: task, Action: "refresh"}, &result); err == nil {
		t.Fatal("detached credential request accepted")
	}
}
