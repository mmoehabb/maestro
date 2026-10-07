package core_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/handoff"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
	"github.com/mmoehabb/maestro/internal/testutil"
)

// TestContextAgentProcess speaks real fixture formats, but never invokes a model.
func TestContextAgentProcess(t *testing.T) {
	if os.Getenv("MAESTRO_CONTEXT_HELPER") != "1" {
		return
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) < 3 {
		os.Exit(2)
	}
	kind, mode, id := args[0], args[1], args[2]
	if os.Getenv("MAESTRO_FAIL_STARTUP_AGENT") == kind {
		fmt.Println("startup failed before session initialization")
		delay, _ := time.ParseDuration(os.Getenv("MAESTRO_FAIL_STARTUP_DELAY"))
		time.Sleep(delay)
		os.Exit(7)
	}
	dir, _ := os.Getwd()
	var path string
	if kind == "codex" {
		path = filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "rollout-"+id+".jsonl")
	} else {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".gemini", "antigravity-cli", "brain", id, ".system_generated", "logs", "transcript.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		os.Exit(2)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	enc := json.NewEncoder(f)
	emit := func(event, content string) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if kind == "codex" {
			var payload any
			ty := "event_msg"
			switch event {
			case "meta":
				ty = "session_meta"
				payload = map[string]any{"id": id, "cwd": dir, "timestamp": now}
			case "start":
				payload = map[string]any{"type": "task_started"}
			case "done":
				payload = map[string]any{"type": "task_complete"}
			default:
				ty = "response_item"
				payload = map[string]any{"type": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": content}}}
			}
			_ = enc.Encode(map[string]any{"type": ty, "timestamp": now, "payload": payload})
		} else {
			ty := "PLANNER_RESPONSE"
			if event == "start" {
				ty = "USER_INPUT"
			}
			if event == "meta" {
				return
			}
			_ = enc.Encode(map[string]any{"type": ty, "created_at": now, "step_index": time.Now().UnixNano(), "status": "DONE", "content": content})
		}
		_ = f.Sync()
	}
	if mode == "new" {
		emit("meta", "")
	}
	// Record both native ID and argv prompt in output for end-to-end assertions.
	fmt.Printf("ready %s %s %s\n", kind, mode, id)
	if len(args) > 3 {
		fmt.Println("prompt:", args[3])
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() { <-signals; emit("message", "Final context saved on stop."); _ = f.Close(); os.Exit(0) }()
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		switch input.Text() {
		case "work":
			emit("start", "Continue the task.")
			fmt.Println("working")
		case "done":
			emit("message", "The login fix is complete.")
			emit("done", "The login fix is complete.")
			fmt.Println("complete")
		case "exit":
			emit("message", "Final context saved on exit.")
			_ = f.Close()
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func contextRuntime(t *testing.T) (*core.TaskService, *core.Runtime, store.Task) {
	t.Helper()
	t.Setenv("MAESTRO_CONTEXT_HELPER", "1")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	s, err := app.Open(context.Background(), testutil.Repo(t), config.Paths{ConfigFile: filepath.Join(data, "config.toml"), DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Store.Close() })
	for _, name := range []string{"codex", "agy"} {
		s.Config.Agents[name] = config.Agent{Cmd: exe, GenerateSessionID: true, New: []string{"-test.run=^TestContextAgentProcess$", "--", name, "new", "{{.SessionID}}", "{{.Prompt}}"}, Resume: []string{"-test.run=^TestContextAgentProcess$", "--", name, "resume", "{{.SessionID}}", "{{.Prompt}}"}}
	}
	s.Config.Activity.IdleAfter = "100ms"
	task, err := s.Create(context.Background(), core.NewTask{Title: "Login fix", Agent: "codex", Prompt: "Fix authentication"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return s, r, task
}

func awaitPane(t *testing.T, p *term.Pane, state term.State, text string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s := p.Snapshot(true)
		if s.State == state && strings.Contains(s.Screen, text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane did not reach %s/%q: %+v", state, text, p.Snapshot(true))
}
func sendLine(p *term.Pane, s string) { p.Paste(s); p.Key(uv.Key{Code: uv.KeyEnter}, false) }

func TestTaskBasePreservesGitContext(t *testing.T) {
	for _, base := range []string{"HEAD", "@", "HEAD~1", "main~1", "main", "origin/main", "detached default"} {
		t.Run(base, func(t *testing.T) {
			s, r, _ := contextRuntime(t)
			ctx := context.Background()
			testutil.Git(t, s.Repo.Root, "commit", "--allow-empty", "-m", "advance source")
			testutil.Git(t, s.Repo.Root, "update-ref", "refs/remotes/origin/main", "HEAD")
			requested := base
			if base == "detached default" {
				testutil.Git(t, s.Repo.Root, "checkout", "--detach")
				var err error
				s.Repo, err = git.Discover(ctx, s.Repo.Root)
				if err != nil {
					t.Fatal(err)
				}
				requested = ""
			}
			task, err := r.Create(ctx, core.NewTask{Title: "Base context", Agent: "codex", Base: requested})
			if err != nil {
				t.Fatal(err)
			}
			if (base == "main" || base == "origin/main") && task.BaseBranch != base {
				t.Fatal("named base changed", task.BaseBranch)
			}
			if err = os.WriteFile(filepath.Join(task.Worktree, "base-fix.txt"), []byte("committed task work\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, task.Worktree, "add", "base-fix.txt")
			testutil.Git(t, task.Worktree, "commit", "-m", "implement-base-context-task")
			status, err := s.Status(ctx, task)
			if err != nil || status.Commits != 1 {
				t.Fatal("base hid task commit", task.BaseBranch, status, err)
			}
			saved, err := s.Find(ctx, task.Slug)
			if err != nil || saved.BaseBranch != task.BaseBranch {
				t.Fatal("stable base was not persisted", saved, err)
			}
			p, err := r.Switch(ctx, saved, "agy", 80, 24, true)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready agy new")
			brief, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "handoff.md"))
			if err != nil || !strings.Contains(string(brief), "implement-base-context-task") || !strings.Contains(string(brief), "base-fix.txt") {
				t.Fatal("handoff lost committed Git context", string(brief), err)
			}
		})
	}
}

func TestContextSwitchResumeAndHistorySurvivesDeletion(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	original, err := s.Store.LatestAgentSession(ctx, task.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	sendLine(p, "work")
	// Wait for the native start to be observed, then exceed the quiet timeout.
	time.Sleep(1300 * time.Millisecond)
	awaitPane(t, p, term.Working, "working")
	if p.Snapshot(false).State != term.Working {
		t.Fatal("native work completed on silence", p.Snapshot(true))
	}
	if _, err = r.Switch(ctx, task, "agy", 80, 24, false); !errors.Is(err, core.ErrInterruptRequired) {
		t.Fatal("working switch lacked confirmation", err)
	}
	sendLine(p, "done")
	awaitPane(t, p, term.Done, "complete")
	p, err = r.Switch(ctx, task, "agy", 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
	b, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "handoff.md"))
	if err != nil || !strings.Contains(string(b), "login fix") {
		t.Fatal("context missing", string(b), err)
	}
	if !strings.Contains(strings.ReplaceAll(strings.Join(p.Scrollback(), ""), "\n", ""), "handoff.md first") {
		t.Fatalf("handoff instruction not delivered. Expected: %s\nGot: %s", handoff.Prompt(task.Worktree), strings.Join(p.Scrollback(), "\n"))
	}
	sendLine(p, "work")
	time.Sleep(1200 * time.Millisecond)
	sendLine(p, "done")
	awaitPane(t, p, term.Done, "complete")
	p, err = r.Switch(ctx, task, "codex", 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex resume")
	resumed, err := s.Store.LatestAgentSession(ctx, task.ID, "codex")
	if err != nil || resumed.NativeID != original.NativeID {
		t.Fatal("wrong resume identity", original, resumed, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(ctx, task.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Sessions) != 3 || len(h.Handoffs) != 2 {
		t.Fatal("missing switch history", h)
	}
	keys := map[string]bool{}
	for _, turn := range h.Turns {
		if turn.SourceKey == "" {
			continue
		}
		key := fmt.Sprintf("%s/%s", h.Sessions[turn.SessionID-1].Agent, turn.SourceKey)
		if keys[key] {
			t.Fatal("duplicated native turn", key)
		}
		keys[key] = true
	}
	before, _ := json.Marshal(h)
	if err = os.RemoveAll(task.Worktree); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(os.Getenv("CODEX_HOME")); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s.Store, err = store.Open(ctx, filepath.Join(filepath.Dir(s.Config.Worktree.Root), "maestro.db"))
	if err != nil {
		t.Fatal(err)
	}
	h, err = s.History(ctx, task.Slug)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(h)
	if string(before) != string(after) {
		t.Fatal("history depended on worktree/source files")
	}
}

func TestSwitchPreflightAndPendingRecovery(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	s.Config.Agents["missing"] = config.Agent{Cmd: filepath.Join(t.TempDir(), "absent")}
	if _, err = r.Switch(ctx, task, "missing", 80, 24, false); err == nil {
		t.Fatal("missing executable accepted")
	}
	select {
	case <-p.Done():
		t.Fatal("preflight stopped outgoing process")
	default:
	}
	// A filesystem failure occurs after the outgoing process stops. The persisted
	// pending handoff must survive and be consumed on retry without losing context.
	dir := filepath.Join(task.Worktree, ".maestro")
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "handoff.md")
	if err = os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Switch(ctx, task, "agy", 80, 24, false); err == nil {
		t.Fatal("invalid handoff destination accepted")
	}
	pending, err := s.Store.PendingHandoff(ctx, task.ID)
	if err != nil || pending.Agent != "agy" || pending.ID == 0 {
		t.Fatal(pending, err)
	}
	if err = os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	// A configuration change during recovery must not consume the pending handoff.
	original := s.Config.Agents["agy"]
	broken := original
	broken.New = broken.New[:len(broken.New)-1]
	s.Config.Agents["agy"] = broken
	if _, err = r.Start(task, 80, 24, false); err == nil || !strings.Contains(err.Error(), "cannot deliver the prompt") {
		t.Fatal(err)
	}
	if saved, e := s.Store.PendingHandoff(ctx, task.ID); e != nil || saved.ID != pending.ID {
		t.Fatal("pending handoff consumed without delivery", saved, e)
	}
	s.Config.Agents["agy"] = original
	p, err = r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
	if !strings.Contains(strings.ReplaceAll(strings.Join(p.Scrollback(), ""), "\n", ""), "handoff.md first") {
		t.Fatal("retry lost handoff prompt")
	}
	sendLine(p, "work") // Native targets acknowledge delivery with a new turn.
	pending, err = s.Store.PendingHandoff(ctx, task.ID)
	deadline := time.Now().Add(4 * time.Second)
	for err == nil && pending.ID != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		pending, err = s.Store.PendingHandoff(ctx, task.ID)
	}
	if err != nil || pending.ID != 0 {
		t.Fatal(pending, err)
	}
}

func TestConcurrentStopSwitchAndClose(t *testing.T) {
	_, r, task := contextRuntime(t)
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	var wg sync.WaitGroup
	wg.Go(func() { r.Stop(task.ID) })
	wg.Go(func() { _, _ = r.Switch(context.Background(), task, "agy", 80, 24, true) })
	wg.Go(func() { _ = r.Close() })
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(12 * time.Second):
		t.Fatal("concurrent lifecycle operations deadlocked")
	}
}

func TestManualPromptSwitchAndFreshRestart(t *testing.T) {
	s, r, task := contextRuntime(t)
	preset := s.Config.Agents["agy"]
	preset.ManualPrompt = true
	s.Config.Agents["agy"] = preset
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	p, err = r.Switch(context.Background(), task, "agy", 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
	if strings.Contains(strings.Join(p.Scrollback(), "\n"), "prompt:") {
		t.Fatal("manual agent received injected prompt")
	}
	p, err = r.Start(task, 80, 24, true)
	if err != nil {
		t.Fatal("fresh manual restart inherited original agent prompt", err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
}

func TestSwitchFreshRecoversSelectedTarget(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	target := task
	target.Agent = "agy"
	// A historical target launch for which identity discovery never succeeded.
	if _, err := s.Store.StartSession(ctx, target, "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	if _, err = r.Switch(ctx, task, "agy", 80, 24, false); !errors.Is(err, core.ErrFreshStartRequired) {
		t.Fatal(err)
	}
	current, err := s.Find(ctx, task.Slug)
	if err != nil || current.Agent != "codex" {
		t.Fatal(current, err)
	}
	if pending, err := s.Store.PendingHandoff(ctx, task.ID); err != nil || pending.ID != 0 {
		t.Fatal(pending, err)
	}
	sendLine(p, "work")
	time.Sleep(1200 * time.Millisecond)
	if _, err = r.SwitchFresh(ctx, task, "agy", 80, 24, false); !errors.Is(err, core.ErrInterruptRequired) {
		t.Fatal(err)
	}
	fresh, err := r.SwitchFresh(ctx, task, "agy", 80, 24, true)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, fresh, term.Done, "ready agy new")
	current, err = s.Find(ctx, task.Slug)
	if err != nil || current.Agent != "agy" {
		t.Fatal(current, err)
	}
	if !strings.Contains(strings.ReplaceAll(strings.Join(fresh.Scrollback(), ""), "\n", ""), "handoff.md first") {
		t.Fatalf("fresh target did not receive handoff. Got: %s", strings.Join(fresh.Scrollback(), "\n"))
	}
}

func TestFreshRestartPreservesPendingContext(t *testing.T) {
	for _, mode := range []string{"switch", "restart"} {
		t.Run(mode, func(t *testing.T) {
			s, r, task := contextRuntime(t)
			ctx := context.Background()
			p, err := r.Start(task, 80, 24, false)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready codex new")
			p, err = r.Switch(ctx, task, "agy", 80, 24, true)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready agy new")
			pending, err := s.Store.PendingHandoff(ctx, task.ID)
			if err != nil || pending.ID == 0 {
				t.Fatal("expected unacknowledged handoff", pending, err)
			}
			if mode == "switch" {
				p, err = r.SwitchFresh(ctx, task, "agy", 80, 24, true)
			} else {
				p, err = r.Start(task, 80, 24, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready agy new")
			if !strings.Contains(strings.ReplaceAll(strings.Join(p.Scrollback(), ""), "\n", ""), "handoff.md first") {
				t.Fatal("fresh agent did not receive handoff instruction")
			}
			content, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "handoff.md"))
			if err != nil || !strings.Contains(string(content), "Final context saved on stop.") {
				t.Fatal("fresh handoff lost final output", string(content), err)
			}
			history, err := s.History(ctx, task.Slug)
			if err != nil || len(history.Handoffs) != 2 || history.Handoffs[0].DeliveredAt == nil || history.Handoffs[1].DeliveredAt != nil {
				t.Fatal("fresh launch did not retain the old and new handoffs", history.Handoffs, err)
			}
		})
	}
}

func TestPendingHandoffRetryUsesCurrentContext(t *testing.T) {
	for _, mode := range []string{"switch", "resume", "fresh"} {
		t.Run(mode, func(t *testing.T) {
			s, r, task := contextRuntime(t)
			ctx := context.Background()
			p, err := r.Start(task, 80, 24, false)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready codex new")
			path := filepath.Join(task.Worktree, ".maestro", "handoff.md")
			if err = os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Switch(ctx, task, "agy", 80, 24, true); err == nil {
				t.Fatal("expected handoff write failure")
			}
			pending, err := s.Store.PendingHandoff(ctx, task.ID)
			if err != nil || pending.ID == 0 || pending.FromSession == 0 {
				t.Fatal("failed switch did not retain its source context", pending, err)
			}
			notes := "Updated requirement: preserve API compatibility."
			if err = r.SetNotes(ctx, task, notes); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(task.Worktree, "retry-context.txt"), []byte("new work after the failed switch"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if mode == "switch" {
				p, err = r.Switch(ctx, task, "agy", 80, 24, true)
			} else {
				p, err = r.Start(task, 80, 24, mode == "fresh")
			}
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready agy new")
			content, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(content), notes) || !strings.Contains(string(content), "retry-context.txt") {
				t.Fatal("retry handoff omitted current notes or Git state", string(content), err)
			}
			saved, err := s.Store.PendingHandoff(ctx, task.ID)
			if err != nil || saved.ID != pending.ID || saved.FromSession != pending.FromSession || saved.Content != string(content) {
				t.Fatal("retry did not refresh the existing handoff", saved, err)
			}
		})
	}
}

func TestSwitchSessionCreationFailureRetainsHandoff(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	original := s.Config.Agents["agy"]
	broken := original
	broken.GenerateSessionID = false
	broken.SessionCreate = []string{"-test.run=^TestContextAgentProcess$", "--", "agy", "new", "failed-session"}
	s.Config.Agents["agy"] = broken
	t.Setenv("MAESTRO_FAIL_STARTUP_AGENT", "agy")
	if _, err = r.Switch(ctx, task, "agy", 80, 24, true); err == nil {
		t.Fatal("expected session creation failure")
	}
	pending, err := s.Store.PendingHandoff(ctx, task.ID)
	if err != nil || pending.ID == 0 || pending.Agent != "agy" {
		t.Fatal("session creation failure lost pending context", pending, err)
	}
	current, err := s.Find(ctx, task.Slug)
	if err != nil || current.Agent != "agy" {
		t.Fatal("session creation failure lost selected target", current, err)
	}
	s.Config.Agents["agy"] = original
	t.Setenv("MAESTRO_FAIL_STARTUP_AGENT", "")
	p, err = r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
	if !strings.Contains(strings.ReplaceAll(strings.Join(p.Scrollback(), ""), "\n", ""), "handoff.md first") {
		t.Fatal("session creation retry lost handoff instruction")
	}
}

func TestNativeActivityRecoversSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows locking semantics prevent file replacement")
	}
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("readFailure=%t", failure), func(t *testing.T) {
			s, r, task := contextRuntime(t)
			p, err := r.Start(task, 80, 24, false)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready codex new")
			sendLine(p, "work")
			time.Sleep(1200 * time.Millisecond)
			awaitPane(t, p, term.Working, "working")
			session, err := s.Store.LatestAgentSession(context.Background(), task.ID, "codex")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "rollout-"+session.NativeID+".jsonl")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if failure {
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				deadline := time.After(5 * time.Second)
			waitError:
				for {
					select {
					case e := <-r.Events:
						if e.Err != nil && strings.Contains(e.Err.Error(), "native transcript unavailable") {
							break waitError
						}
					case <-deadline:
						t.Fatal("read failure not observed")
					}
				}
			}
			replacement := path + ".replacement"
			if err = os.WriteFile(replacement, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1500 * time.Millisecond)
			awaitPane(t, p, term.Working, "working")
		})
	}
}

func TestMalformedHistoryKeepsTerminalHandoff(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	sendLine(p, "done")
	awaitPane(t, p, term.Done, "complete")
	session, err := s.Store.LatestAgentSession(ctx, task.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "rollout-"+session.NativeID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("malformed\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	sendLine(p, "work")
	awaitPane(t, p, term.Done, "working")
	if _, err = r.Switch(ctx, task, "agy", 80, 24, true); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(ctx, task.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if h.Sessions[0].NativeComplete {
		t.Fatal("partial import marked complete")
	}
	b, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "handoff.md"))
	if err != nil || !strings.Contains(string(b), "[terminal") || !strings.Contains(string(b), "working") || !strings.Contains(string(b), "The login fix is complete.") {
		t.Fatal(string(b), err)
	}
}

func TestSwitchRejectsUndeliverableHandoff(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%t", resume), func(t *testing.T) {
			s, r, task := contextRuntime(t)
			ctx := context.Background()
			if resume {
				target := task
				target.Agent = "agy"
				if _, err := s.Store.StartSession(ctx, target, "saved-target", time.Now().Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			p, err := r.Start(task, 80, 24, false)
			if err != nil {
				t.Fatal(err)
			}
			awaitPane(t, p, term.Done, "ready codex new")
			original := s.Config.Agents["agy"]
			broken := original
			if resume {
				broken.Resume = broken.Resume[:len(broken.Resume)-1]
			} else {
				broken.New = broken.New[:len(broken.New)-1]
			}
			s.Config.Agents["agy"] = broken
			if _, err = r.Switch(ctx, task, "agy", 80, 24, true); err == nil || !strings.Contains(err.Error(), "cannot deliver the prompt") {
				t.Fatal(err)
			}
			select {
			case <-p.Done():
				t.Fatal("validation stopped outgoing agent")
			default:
			}
			current, err := s.Find(ctx, task.Slug)
			if err != nil || current.Agent != "codex" {
				t.Fatal(current, err)
			}
			h, err := s.History(ctx, task.Slug)
			if err != nil || len(h.Handoffs) != 0 {
				t.Fatal("undelivered handoff recorded", h.Handoffs, err)
			}
			s.Config.Agents["agy"] = original
			p, err = r.Switch(ctx, task, "agy", 80, 24, true)
			if err != nil {
				t.Fatal(err)
			}
			mode := "new"
			if resume {
				mode = "resume"
			}
			awaitPane(t, p, term.Done, "ready agy "+mode)
			if !strings.Contains(strings.ReplaceAll(strings.Join(p.Scrollback(), ""), "\n", ""), "handoff.md first") {
				t.Fatal("handoff instruction not delivered")
			}
		})
	}
}

func TestStartupCrashRetainsHandoffAcrossRestart(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	p, err := r.Start(task, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready codex new")
	t.Setenv("MAESTRO_FAIL_STARTUP_AGENT", "agy")
	t.Setenv("MAESTRO_FAIL_STARTUP_DELAY", "2500ms") // Outlast the generic startup fallback.
	p, err = r.Switch(ctx, task, "agy", 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Crashed, "startup failed")
	r.Stop(task.ID) // Join final persistence, not only the process.
	pending, err := s.Store.PendingHandoff(ctx, task.ID)
	if err != nil || pending.ID == 0 {
		t.Fatal("startup crash consumed handoff", pending, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s.Store, err = store.Open(ctx, filepath.Join(filepath.Dir(s.Config.Worktree.Root), "maestro.db"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Store.PendingHandoff(ctx, task.ID)
	if err != nil || saved.ID != pending.ID || saved.Content != pending.Content {
		t.Fatal("restart lost pending context", saved, err)
	}
	t.Setenv("MAESTRO_FAIL_STARTUP_AGENT", "")
	r, err = s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Recovery explicitly starts fresh because the failed target never created
	// the generated native ID. Even this path must retain the original handoff.
	p, err = r.Start(task, 80, 24, true)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
	if !strings.Contains(strings.ReplaceAll(strings.Join(p.Scrollback(), ""), "\n", ""), "handoff.md first") {
		t.Fatal("recovery lost handoff instruction")
	}
	sendLine(p, "work")
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		saved, err = s.Store.PendingHandoff(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.ID == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("initialized agent never acknowledged handoff")
}

func TestNotesPersistAndReachNextAgent(t *testing.T) {
	s, r, task := contextRuntime(t)
	ctx := context.Background()
	notes := "Keep API compatibility.\nTODO: cover session expiry."
	if err := s.SetNotes(ctx, task.Slug, "outside writer"); err == nil {
		t.Fatal("CLI notes bypassed runtime lock")
	}
	if err := r.SetNotes(ctx, task, notes); err != nil {
		t.Fatal(err)
	}
	task, err := s.Find(ctx, task.Slug)
	if err != nil || task.Notes != notes {
		t.Fatal(task, err)
	}
	p, err := r.Switch(ctx, task, "agy", 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready agy new")
	content, err := os.ReadFile(filepath.Join(task.Worktree, ".maestro", "handoff.md"))
	if err != nil || !strings.Contains(string(content), notes) {
		t.Fatal("notes missing from handoff", string(content), err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.SetNotes(ctx, task.Slug, ""); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(ctx, task.Slug)
	if err != nil || h.Task.Notes != "" {
		t.Fatal(h.Task, err)
	}
	count := 0
	for _, e := range h.Events {
		if e.Kind == "notes_updated" {
			count++
		}
	}
	if count != 2 {
		t.Fatal("missing notes timeline events", count)
	}
}

func TestGenericHandoffAcknowledgesLiveStartup(t *testing.T) {
	s, r, task := contextRuntime(t)
	t.Setenv("MAESTRO_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Agents["fake"] = config.Agent{Cmd: exe, GenerateSessionID: true, New: []string{"-test.run=^TestAgentProcess$", "--", "new", "{{.SessionID}}", "{{.Prompt}}"}, Resume: []string{"-test.run=^TestAgentProcess$", "--", "resume", "{{.SessionID}}", "{{.Prompt}}"}}
	p, err := r.Switch(context.Background(), task, "fake", 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	awaitPane(t, p, term.Done, "ready new")
	pending, err := s.Store.PendingHandoff(context.Background(), task.ID)
	if err != nil || pending.ID == 0 {
		t.Fatal("generic handoff consumed before startup window", pending, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		pending, err = s.Store.PendingHandoff(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pending.ID == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("generic handoff not acknowledged after live startup")
}
