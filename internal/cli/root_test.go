package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"

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
	for _, c := range []string{"new", "ls", "open", "switch", "history", "push", "pr", "merge", "archive", "reopen", "rm", "doctor", "config", "completion"} {
		if !strings.Contains(out, "  "+c+" ") {
			t.Errorf("help output missing command %q", c)
		}
	}
}

func TestStubsReturnNotImplemented(t *testing.T) {
	cases := map[string][]string{
		"P2": {"switch", "x"},
		"P3": {"merge", "x"},
	}
	for phase, args := range cases {
		_, err := run(t, args...)
		if !errors.Is(err, ErrNotImplemented) {
			t.Fatalf("%v: got %v, want ErrNotImplemented", args, err)
		}
		if !strings.Contains(err.Error(), phase) {
			t.Errorf("%v: error %q does not name phase %s", args, err, phase)
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
