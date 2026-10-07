package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/store"
)

func TestForgeSnapshots(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"80x24", 80, 24}, {"160x48", 160, 48}} {
		for _, screen := range []string{"badges", "pr", "merge", "cleanup", "force", "reopen"} {
			m := testModel(t)
			m.width = size.w
			m.height = size.h
			task := m.tabs[0].task
			task.Lifecycle = "pr_open"
			task.PRState = "open"
			task.PRNumber = 42
			task.PRURL = "https://github.com/owner/repo/pull/42"
			task.PRHeadSHA = "abc123"
			task.CIState = "failure"
			task.ReviewState = "changes_requested"
			m.tabs[0].task = task
			m.tabs[1].task.Lifecycle = "merged"
			m.tabs[2].task.Lifecycle = "pushed"
			switch screen {
			case "pr":
				task.PRNumber = 0
				m.forgeUI = newPRDialog(task, forge.NewPR{Title: "Fix login", Base: "main", Body: "## Goal\n\nKeep login sessions valid.\n\n## Commits\n\nabc123 Fix refresh"}, size.w, size.h)
			case "merge", "cleanup":
				m.forgeUI = &forgeDialog{task: task, action: screen}
			case "force":
				m.forgeUI = &forgeDialog{task: task, action: "cleanup"}
				m.forgeKey(tea.KeyPressMsg{Code: 'f'})
			case "reopen":
				m.forgeUI = &forgeDialog{action: "reopen", archived: []store.Task{{Slug: "fix-login"}, {Slug: "docs"}}}
			}
			got := ansi.Strip(m.View().Content) + "\n"
			name := filepath.Join("testdata", "forge-"+screen+"-"+size.name+".golden")
			if *update {
				if err := os.WriteFile(name, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != got {
				t.Errorf("snapshot differs: %s", name)
			}
			for _, line := range strings.Split(got, "\n") {
				if ansi.StringWidth(line) > size.w {
					t.Fatalf("line too wide: %q", line)
				}
			}
		}
	}
}

func TestForgeDialogInputAndConfirmation(t *testing.T) {
	m := testModel(t)
	task := m.tabs[0].task
	m.forgeUI = &forgeDialog{task: task, action: "cleanup"}
	m.forgeKey(tea.KeyPressMsg{Code: 'f'})
	if !m.forgeUI.force {
		t.Fatal("no second force confirmation")
	}
	if cmd := m.forgeKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("accepted empty confirmation")
	}
	m.forgeKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.forgeUI != nil {
		t.Fatal("escape did not cancel")
	}
	m.forgeUI = newPRDialog(task, forge.NewPR{Title: "Title", Base: "main", Body: "body"}, 80, 24)
	m.forgeKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m.forgeKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.PasteMsg{Content: " reviewed"})
	if !strings.Contains(m.forgeUI.body.Value(), "reviewed") {
		t.Fatal("paste not routed to description")
	}
}

func TestWorkflowUpdatesAndRemoval(t *testing.T) {
	m := testModel(t)
	task := m.tabs[0].task
	task.Lifecycle = "merged"
	m.applyTask(task)
	if m.tabs[0].task.Lifecycle != "merged" {
		t.Fatal("lifecycle not updated")
	}
	task.Lifecycle = "archived"
	m.applyTask(task)
	if len(m.tabs) != 2 || m.active != 0 {
		t.Fatal("archive did not remove tab")
	}
}

func TestCleanupPromptWaitsForPendingTask(t *testing.T) {
	m := testModel(t)
	m.loaded = false
	m.tabs[0].pending = true
	m.queueCleanup(m.tabs[0].task)
	m.Update(tickMsg(time.Now()))
	if m.forgeUI != nil || len(m.cleanupQueue) != 1 {
		t.Fatal("cleanup prompt was consumed while task was busy")
	}
	m.tabs[0].pending = false
	m.Update(tickMsg(time.Now()))
	if m.forgeUI == nil || m.forgeUI.action != "cleanup" || len(m.cleanupQueue) != 0 {
		t.Fatal("cleanup prompt was not delivered when task became ready")
	}
}

func TestUnrelatedWorkflowPreservesPREditor(t *testing.T) {
	for _, failure := range []error{nil, errors.New("push failed")} {
		m := testModel(t)
		pushing, editing := m.tabs[0].task, m.tabs[1].task
		// Begin the push before opening the other task's editor.
		m.workflow(pushing, "push", core.WorkflowOptions{})
		dialog := newPRDialog(editing, forge.NewPR{Title: "Unsaved title", Base: "main", Body: "Unsaved description"}, 80, 24)
		m.forgeUI = dialog
		m.workflowResult(workflowMsg{task: pushing, action: "push", err: failure})
		if m.forgeUI != dialog || dialog.title.Value() != "Unsaved title" || dialog.body.Value() != "Unsaved description" || dialog.busy || dialog.err != "" {
			t.Fatal("unrelated push changed or dismissed the PR editor")
		}
		if m.tabs[0].pending {
			t.Fatal("completed push left the tab pending")
		}
	}
}

func TestWorkflowOnlyUpdatesItsOwningDialog(t *testing.T) {
	m := testModel(t)
	task := m.tabs[0].task
	dialog := newPRDialog(task, forge.NewPR{Title: "Title", Base: "main", Body: "Description"}, 80, 24)
	m.forgeUI = dialog
	m.workflow(task, "pr", core.WorkflowOptions{})
	if !dialog.busy {
		t.Fatal("PR submission did not mark its editor busy")
	}
	m.workflowResult(workflowMsg{task: task, action: "pr", dialog: dialog, err: errors.New("create failed")})
	if m.forgeUI != dialog || dialog.busy || dialog.err != "create failed" || dialog.body.Value() != "Description" {
		t.Fatal("own submission failure lost the draft")
	}
	m.workflowResult(workflowMsg{task: task, action: "pr", dialog: dialog})
	if m.forgeUI != nil {
		t.Fatal("own successful submission did not dismiss the editor")
	}
	// A result from an old editor must not close a new editor for the same task.
	m.forgeUI = newPRDialog(task, forge.NewPR{Body: "Replacement draft"}, 80, 24)
	m.workflowResult(workflowMsg{task: task, action: "pr", dialog: dialog})
	if m.forgeUI == nil || m.forgeUI.body.Value() != "Replacement draft" {
		t.Fatal("stale result dismissed the replacement editor")
	}
}
