package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

func TestPaletteRoutingAndDisabledActions(t *testing.T) {
	m := testModel(t)
	m.dispatch(":")
	if m.palette == nil {
		t.Fatal("palette not opened")
	}
	m.palette.input.SetValue("refactor")
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.active != 2 || m.palette != nil {
		t.Fatal("task selection failed")
	}
	m.dispatch(":")
	m.palette.input.SetValue("toggle sidebar")
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.sidebar {
		t.Fatal("palette did not dispatch action")
	}
	m.dispatch(":")
	m.palette.input.SetValue("not a matching command")
	if len(m.paletteEntries()) != 0 {
		t.Fatal("unexpected matches")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.palette == nil {
		t.Fatal("empty result closed palette")
	}
	m.palette = nil
	m.tabs = nil
	m.dispatch(":")
	m.palette.input.SetValue("push")
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.palette == nil || !strings.Contains(m.toast, "No active task") {
		t.Fatal("disabled action ran")
	}
	score, ok := fuzzyScore("swag", "Switch agent")
	if !ok || score <= 0 {
		t.Fatal("fuzzy subsequence not matched")
	}
}

func TestDiffRoutingAndStaleReplies(t *testing.T) {
	m := testModel(t)
	m.tabs[0].task.BaseBranch = "main"
	m.dispatch("D")
	first := m.diff
	if first == nil {
		t.Fatal("diff not opened")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if m.active != 0 {
		t.Fatal("shortcut escaped diff")
	}
	_, _ = m.Update(tea.PasteMsg{Content: "q"})
	if m.diff == nil {
		t.Fatal("paste escaped diff")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'r'})
	if first == m.diff {
		t.Fatal("refresh did not replace request")
	}
	_, _ = m.Update(diffMsg{view: first, result: git.DiffResult{Patch: "stale"}})
	if !m.diff.loading {
		t.Fatal("stale result accepted")
	}
	_, _ = m.Update(diffMsg{view: m.diff, result: git.DiffResult{Patch: "@@ -1 +1 @@\n-old\n+new\n", Untracked: "new.txt", Truncated: true}})
	rendered := ansi.Strip(m.diff.view(80, 20, m.paletteColors()))
	for _, want := range []string{"-old", "+new", "new.txt", "truncated"} {
		if !strings.Contains(rendered, want) {
			t.Fatal(rendered)
		}
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.diff != nil {
		t.Fatal("diff did not close")
	}
	// The existing archive shortcut still takes its original path.
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_ = cmd
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'd'})
	if cmd == nil || !m.tabs[0].pending {
		t.Fatal("archive binding changed")
	}
}

func TestLayoutsHitTestingAndFocus(t *testing.T) {
	m := testModel(t)
	m.width = 40
	m.active = 2
	regions, _ := m.tabLayout()
	for i, b := range regions {
		if b.w > 0 && m.taskAt(b.x, b.y) != i {
			t.Fatal("tab hit mismatch")
		}
	}
	if m.taskAt(39, 1) != -1 {
		t.Fatal("overflow indicator selects task")
	}
	m.width = 100
	m.sidebar = true
	b := m.paneBounds()
	if b.x != 34 || b.w != 64 {
		t.Fatalf("bad bounds %+v", b)
	}
	if m.taskAt(2, b.y+1) != 1 {
		t.Fatal("sidebar row mismatch")
	}
	m.applyOrder([]int64{3, 1, 2})
	if m.tabs[m.active].task.ID != 3 {
		t.Fatal("reordering lost focused task")
	}
	m.width = 60
	if m.sidebarWidth() != 0 {
		t.Fatal("narrow fallback missing")
	}
	for _, size := range [][2]int{{1, 1}, {20, 8}, {40, 12}, {80, 24}, {160, 48}} {
		m.width, m.height = size[0], size[1]
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(lines) > size[1] {
			t.Fatalf("too many rows at %v", size)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("overflow at %v: %q", size, line)
			}
		}
	}
}

type recordedNotifier struct{ calls []string }

func (n *recordedNotifier) Send(_ context.Context, title, body string) error {
	n.calls = append(n.calls, title+": "+body)
	return nil
}

func executeNotification(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			executeNotification(t, c)
		}
	}
}

func TestNotificationsTransitionsAndStalePane(t *testing.T) {
	m := testModel(t)
	n := &recordedNotifier{}
	m.SetNotifier(n)
	executeNotification(t, m.observeActivity()) // establish baseline
	m.tabs[1].state = term.Working
	executeNotification(t, m.observeActivity())
	m.tabs[1].state = term.Done
	executeNotification(t, m.observeActivity())
	executeNotification(t, m.observeActivity())
	if len(n.calls) != 1 {
		t.Fatalf("duplicate or missing notification: %v", n.calls)
	}
	m.tabs[0].state = term.NeedsInput
	executeNotification(t, m.observeActivity())
	if len(n.calls) != 1 {
		t.Fatal("focused task notified")
	}
	m.blurred = true
	m.tabs[0].state = term.Working
	executeNotification(t, m.observeActivity())
	m.tabs[0].state = term.Done
	executeNotification(t, m.observeActivity())
	if len(n.calls) != 2 {
		t.Fatal("unfocused terminal not notified")
	}
	m.cfg.Activity.NotifyOn = nil
	m.tabs[1].state = term.NeedsInput
	executeNotification(t, m.observeActivity())
	if len(n.calls) != 2 {
		t.Fatal("disabled notifications delivered")
	}
	m.cfg.Activity.NotifyOn = []string{"done", "needs_input"}
	_, _ = m.Update(core.Event{TaskID: m.tabs[1].task.ID, State: term.Done, Pane: new(term.Pane)})
	if m.tabs[1].state != term.NeedsInput {
		t.Fatal("stale pane changed activity")
	}
}

func TestThemeAndHelp(t *testing.T) {
	m := testModel(t)
	dark := m.paletteColors()
	if m.View().BackgroundColor != nil {
		t.Fatal("auto theme overwrote the terminal before probing")
	}
	_, _ = m.Update(tea.BackgroundColorMsg{Color: color.White})
	if m.View().BackgroundColor == nil {
		t.Fatal("auto theme did not apply after probing")
	}
	if m.paletteColors().Background == dark.Background {
		t.Fatal("auto theme ignored background")
	}
	m.cfg.Theme = "tokyo-night"
	tokyo := m.paletteColors()
	_, _ = m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if m.paletteColors() != tokyo {
		t.Fatal("explicit theme changed automatically")
	}
	m.cfg.Themes = map[string]config.Palette{"mine": dark}
	m.cfg.Theme = "mine"
	if m.paletteColors() != dark {
		t.Fatal("custom theme ignored")
	}
	m.dispatch("?")
	m.helpKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !strings.Contains(m.helpView(80, 20), "Ctrl+C") {
		t.Fatal("help cannot reach final bindings")
	}
}

// Freeze background work while exercising real Bubble Tea message routing and
// sizing. No agents, filesystem polling, or wall-clock animations run here.
type screenHarness struct{ *Model }

func (screenHarness) Init() tea.Cmd { return nil }
func (s screenHarness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(finishScreen); ok {
		return s, tea.Quit
	}
	_, cmd := s.Model.Update(msg)
	return s, cmd
}

type finishScreen struct{}

func TestP4Screens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 48}} {
		for _, screen := range []string{"main", "empty", "launch-error", "newtask", "switch", "confirm", "history", "notes", "pr", "pr-existing", "merge", "cleanup", "force", "reopen", "help", "help-end", "palette", "palette-empty", "palette-disabled", "diff", "diff-empty", "diff-loading", "diff-error", "sidebar", "sidebar-overflow", "toast", "icons-unicode", "icons-ascii", "icons-nerd", "light", "tokyo-night", "themes"} {
			t.Run(fmt.Sprintf("%s-%dx%d", screen, size[0], size[1]), func(t *testing.T) {
				m := testModel(t)
				m.width, m.height = size[0], size[1]
				task := m.tabs[0].task
				task.BaseBranch = "main"
				m.tabs[0].task = task
				switch screen {
				case "empty":
					m.tabs = nil
				case "launch-error":
					m.tabs[0].err = errors.New("agent executable not found")
				case "newtask":
					m.dialog = newDialog(m.cfg, "main")
					for i := range m.dialog.detected {
						m.dialog.detected[i] = "installed"
					}
				case "switch", "confirm":
					m.switcher = &switchDialog{task: task, agents: []string{"agy", "codex"}, labels: []string{"agy · installed", "codex · current"}, confirm: screen == "confirm"}
				case "history":
					m.history = &historyView{data: fixtureHistory(), expanded: map[int64]bool{1: true}}
				case "notes":
					m.openNotes()
					m.notes.input.SetValue("Keep the public API compatible.\nTODO: verify session expiry.")
				case "pr":
					m.forgeUI = newPRDialog(task, forge.NewPR{Title: "Fix login", Base: "main", Body: "Keep login sessions valid."}, m.width, m.height)
				case "pr-existing":
					task.PRNumber = 42
					task.PRState = "open"
					task.PRURL = "https://github.com/owner/repo/pull/42"
					m.forgeUI = &forgeDialog{task: task, action: "pr"}
				case "merge", "cleanup":
					m.forgeUI = &forgeDialog{task: task, action: screen}
				case "force":
					m.forgeUI = &forgeDialog{task: task, action: "cleanup"}
					m.forgeKey(tea.KeyPressMsg{Code: 'f'})
				case "reopen":
					m.forgeUI = &forgeDialog{action: "reopen", archived: []store.Task{{Slug: "completed-task"}}}
				case "help", "help-end":
					m.help = true
					if screen == "help-end" {
						m.helpOffset = 1000
					}
				case "palette", "palette-empty", "palette-disabled":
					m.openPalette()
					if screen == "palette-empty" {
						m.palette.input.SetValue("zzzzzz")
					}
					if screen == "palette-disabled" {
						m.tabs = nil
						m.palette.input.SetValue("push")
					}
				case "diff", "diff-empty", "diff-loading", "diff-error":
					m.diff = &diffView{task: task}
					if screen == "diff" {
						m.diff.result = git.DiffResult{Patch: "diff --git a/auth.go b/auth.go\n--- a/auth.go\n+++ b/auth.go\n@@ -1 +1 @@\n-expired := true\n+expired := false\nBinary files a/logo.png and b/logo.png differ\n", Untracked: "notes.txt\n", Truncated: true}
					}
					if screen == "diff-loading" {
						m.diff.loading = true
					}
					if screen == "diff-error" {
						m.diff.err = errors.New("worktree no longer exists")
					}
				case "sidebar", "sidebar-overflow":
					m.sidebar = true
					if screen == "sidebar-overflow" {
						for i := 4; i < 35; i++ {
							m.tabs = append(m.tabs, tab{task: store.Task{ID: int64(i), Slug: fmt.Sprintf("task-%02d", i), Agent: "codex"}, state: term.Starting})
						}
						m.active = len(m.tabs) - 1
					}
				case "toast":
					m.notify("refactor: needs_input")
				case "themes":
					m.themes = &themePicker{original: "auto", scope: "global configuration", selected: 0}
				case "light":
					m.cfg.Theme = "light"
				case "tokyo-night":
					m.cfg.Theme = "tokyo-night"
				case "icons-unicode", "icons-ascii", "icons-nerd":
					m.cfg.Icons = strings.TrimPrefix(screen, "icons-")
					m.sidebar = true
					m.tabs = nil
					for i, state := range []term.State{term.Starting, term.Working, term.Done, term.NeedsInput, term.Exited, term.Crashed} {
						m.tabs = append(m.tabs, tab{task: store.Task{ID: int64(i + 1), Slug: string(state), Agent: "codex"}, state: state})
					}
				}
				tm := teatest.NewTestModel(t, screenHarness{m}, teatest.WithInitialTermSize(size[0], size[1]))
				tm.Send(finishScreen{})
				final := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(screenHarness)
				got := ansi.Strip(final.View().Content) + "\n"
				teatest.RequireEqualOutput(t, []byte(got))
			})
		}
	}
}

func TestDragReorderPersistsThroughRuntime(t *testing.T) {
	ctx := context.Background()
	m := testModel(t)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project, err := db.EnsureProject(ctx, store.Project{Root: "/ordering"})
	if err != nil {
		t.Fatal(err)
	}
	m.service.Store = db
	m.service.Project = project
	m.service.LockPath = filepath.Join(t.TempDir(), "lock")
	for i := range m.tabs {
		task := m.tabs[i].task
		task.ID = 0
		task.ProjectID = project.ID
		task, err = db.CreateTask(ctx, task)
		if err != nil {
			t.Fatal(err)
		}
		m.tabs[i].task = task
	}
	runtime, err := m.service.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	m.runtime = runtime
	for _, sidebar := range []bool{false, true} {
		m.sidebar = sidebar
		m.width = 120
		m.active = 0
		fromID := m.tabs[0].task.ID
		var start, end bounds
		if sidebar {
			start = bounds{x: 2, y: m.paneBounds().y}
			end = bounds{x: 2, y: m.paneBounds().y + 2}
		} else {
			regions, _ := m.tabLayout()
			start = regions[0]
			end = regions[2]
		}
		_, _ = m.Update(tea.MouseClickMsg{X: start.x, Y: start.y, Button: tea.MouseLeft})
		_, _ = m.Update(tea.MouseMotionMsg{X: end.x, Y: end.y, Button: tea.MouseLeft})
		_, cmd := m.Update(tea.MouseReleaseMsg{X: end.x, Y: end.y, Button: tea.MouseLeft})
		if cmd == nil {
			t.Fatal("drag did not persist")
		}
		_, _ = m.Update(cmd())
		if m.tabs[2].task.ID != fromID || m.tabs[m.active].task.ID != fromID {
			t.Fatal("drag order/focus wrong")
		}
		tasks, e := db.Tasks(ctx, project.ID, false)
		if e != nil || tasks[2].ID != fromID {
			t.Fatalf("order not saved: %+v %v", tasks, e)
		}
	}
}
