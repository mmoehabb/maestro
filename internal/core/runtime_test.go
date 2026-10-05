package core_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestAgentProcess(t *testing.T) {
	if os.Getenv("MAESTRO_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if i+1 < len(os.Args) && os.Args[i+1] == "create-session" {
				id := uuid.NewString()
				f, err := os.OpenFile("session-creations", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					os.Exit(2)
				}
				_, _ = fmt.Fprintln(f, id)
				_ = f.Close()
				fmt.Println(id)
				os.Exit(0)
			}
			os.Exit(testutil.FakeAgent(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

func TestThreeTasksRestoreNativeSessions(t *testing.T) {
	for _, createCommand := range []bool{false, true} {
		t.Run(fmt.Sprintf("create-command=%v", createCommand), func(t *testing.T) {
			testThreeTasksRestoreNativeSessions(t, createCommand)
		})
	}
}

func testThreeTasksRestoreNativeSessions(t *testing.T, createCommand bool) {
	t.Setenv("MAESTRO_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := testutil.Repo(t)
	data := t.TempDir()
	paths := config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data}
	ctx := context.Background()
	open := func() *core.TaskService {
		t.Helper()
		s, err := app.Open(ctx, dir, paths)
		if err != nil {
			t.Fatal(err)
		}
		s.Config.Agents["fake"] = config.Agent{Cmd: exe, New: []string{"-test.run=TestAgentProcess", "--", "new", "{{.SessionID}}", "{{.Prompt}}"}, Resume: []string{"-test.run=TestAgentProcess", "--", "resume", "{{.SessionID}}"}, GenerateSessionID: true}
		if createCommand {
			preset := s.Config.Agents["fake"]
			preset.GenerateSessionID = false
			preset.SessionCreate = []string{"-test.run=^TestAgentProcess$", "--", "create-session"}
			s.Config.Agents["fake"] = preset
		}
		s.Config.Activity.IdleAfter = "80ms"
		return s
	}
	s := open()
	t.Cleanup(func() { _ = s.Store.Close() })
	var tasks []store.Task
	for _, title := range []string{"one", "two", "three"} {
		task, err := s.Create(ctx, core.NewTask{Title: title, Agent: "fake", Prompt: "first prompt"})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	startAll := func(r *core.Runtime, mode string) {
		t.Helper()
		panes := []*term.Pane{}
		for _, task := range tasks {
			p, err := r.Start(task, 80, 24, false)
			if err != nil {
				t.Fatal(err)
			}
			panes = append(panes, p)
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			ready := 0
			for _, p := range panes {
				s := p.Snapshot(true)
				if s.State == term.Done && strings.Contains(s.Screen, "ready "+mode) {
					ready++
				}
			}
			if ready == 3 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		for _, p := range panes {
			t.Logf("pane: %+v", p.Snapshot(true))
		}
		t.Fatal("three panes did not become done")
	}
	startAll(r, "new")
	other := open()
	if _, err := other.OpenRuntime(); err == nil {
		t.Fatal("second runtime acquired lock")
	}
	if rows, err := other.List(ctx, false); err != nil || len(rows) != 3 {
		t.Fatal("read-only listing blocked", err)
	}
	_ = other.Store.Close()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	r, err = s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	startAll(r, "resume")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		f, err := os.Open(filepath.Join(task.Worktree, ".maestro", "launches.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		var launches [][]string
		for scanner.Scan() {
			var args []string
			if err := json.Unmarshal(scanner.Bytes(), &args); err != nil {
				t.Fatal(err)
			}
			launches = append(launches, args)
		}
		_ = f.Close()
		if len(launches) != 2 || launches[0][0] != "new" || launches[1][0] != "resume" || launches[0][1] != launches[1][1] || len(launches[1]) != 2 {
			t.Fatalf("incorrect resume: %#v", launches)
		}
		if createCommand {
			b, err := os.ReadFile(filepath.Join(task.Worktree, "session-creations"))
			if err != nil || string(b) != launches[0][1]+"\n" {
				t.Fatalf("session must be created once, before first launch: %q, %v", b, err)
			}
		}
	}
}
