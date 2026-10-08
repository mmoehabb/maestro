package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	root := NewRootCmd(&out, &errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String() + errb.String(), err
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.HasPrefix(out, "maestro ") {
			t.Errorf("%v: got %q, want prefix %q", args, out, "maestro ")
		}
	}
}

func TestHelpListsPlannedCommands(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"new", "ls", "open", "switch", "history", "push", "pr", "merge", "archive", "reopen", "rm", "doctor", "auth", "config", "theme", "completion"} {
		if !strings.Contains(out, "  "+c+" ") {
			t.Errorf("help output missing command %q", c)
		}
	}
}

func TestForgeCommandsRequireExistingTask(t *testing.T) {
	t.Chdir(testutil.Repo(t))
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	for _, action := range []string{"push", "pr", "merge"} {
		if _, err := run(t, action, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("%s: %v", action, err)
		}
	}
}

func TestArgValidation(t *testing.T) {
	if _, err := run(t, "new"); err == nil {
		t.Error("`new` without a title should fail")
	}
}

func TestCompletion(t *testing.T) {
	out, err := run(t, "completion", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "maestro") {
		t.Error("bash completion script does not mention maestro")
	}
}

func TestNewAndList(t *testing.T) {
	dir := testutil.Repo(t)
	t.Chdir(dir)
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	out, err := run(t, "ls", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list: %q %v", out, err)
	}
	out, err = run(t, "new", "Fix login", "-a", "agy", "-b", "main", "-p", "prompt with spaces")
	if err != nil || !strings.Contains(out, "Created fix-login") {
		t.Fatalf("new: %q %v", out, err)
	}
	out, err = run(t, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []store.Task
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Agent != "agy" || tasks[0].Prompt != "prompt with spaces" {
		t.Fatalf("bad tasks: %+v", tasks)
	}
	out, err = run(t, "config", "path")
	if err != nil || strings.TrimSpace(out) != filepath.Join(xdg.ConfigHome, "maestro", "config.toml") {
		t.Fatalf("config path: %q %v", out, err)
	}
}

func TestHistoryJSONWhileLockedAndAfterWorktreeDeletion(t *testing.T) {
	dir := testutil.Repo(t)
	t.Chdir(dir)
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	if _, err := run(t, "new", "History task"); err != nil {
		t.Fatal(err)
	}
	s, err := app.Open(context.Background(), dir, config.DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	task, err := s.Find(context.Background(), "history-task")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Store.StartSession(context.Background(), task, "native", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.SaveScrollback(context.Background(), session.ID, "persistent context"); err != nil {
		t.Fatal(err)
	}
	runtime, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err = os.RemoveAll(task.Worktree); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "history", "history-task", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var h store.History
	if err = json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatal(err)
	}
	if h.Task.ID != task.ID || len(h.Turns) != 1 || h.Turns[0].Content != "persistent context" || h.Events == nil || h.Handoffs == nil {
		t.Fatal(out)
	}
	if _, err = run(t, "history", "missing"); err == nil {
		t.Fatal("missing task accepted")
	}
	if _, err = run(t, "switch", "history-task"); err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatal("missing agent accepted", err)
	}
}
