package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/forge"
	"github.com/mmoehabb/maestro/internal/git"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

func updateGolden() bool { return flag.Lookup("update").Value.(flag.Getter).Get().(bool) }

func testModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(config.Paths{ConfigFile: filepath.Join(dir, "missing"), DataDir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	s := &core.TaskService{Config: cfg, Repo: git.Repo{Root: "/repo", DefaultBranch: "main"}}
	m := New(s, nil, "")
	m.loaded = true
	for i, name := range []string{"fix-auth", "add-tui", "refactor"} {
		m.tabs = append(m.tabs, tab{task: store.Task{ID: int64(i + 1), Slug: name, Agent: "codex", Branch: "maestro/" + name, Lifecycle: "active"}, state: []term.State{term.Working, term.Done, term.NeedsInput}[i]})
	}
	return m
}

func TestMainAndDialogSnapshots(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"80x24", 80, 24}, {"160x48", 160, 48}} {
		for _, dialog := range []bool{false, true} {
			m := testModel(t)
			m.width, m.height = size.w, size.h
			name := "main-" + size.name
			if dialog {
				m.dialog = newDialog(m.cfg, "main")
				for i := range m.dialog.detected {
					m.dialog.detected[i] = "installed"
				}
				name = "newtask-" + size.name
			}
			got := ansi.Strip(m.View().Content) + "\n"
			path := filepath.Join("testdata", name+".golden")
			if updateGolden() {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != got {
				t.Errorf("%s snapshot differs; run go test ./internal/tui -update", name)
			}
			for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
				if ansi.StringWidth(line) > size.w {
					t.Fatalf("line exceeds viewport: %q", line)
				}
			}
		}
	}
}

func TestPrefixEnterAndTabRouting(t *testing.T) {
	m := testModel(t)
	press := func(code rune, mod tea.KeyMod) { t.Helper(); _, _ = m.Update(tea.KeyPressMsg{Code: code, Mod: mod}) }
	press(tea.KeyEnter, 0)
	if m.prefixed {
		t.Fatal("Enter captured as prefix")
	}
	press('b', tea.ModCtrl)
	if !m.prefixed {
		t.Fatal("fallback prefix ignored")
	}
	press('b', tea.ModCtrl)
	if m.prefixed {
		t.Fatal("double prefix not forwarded")
	}
	press('3', tea.ModAlt)
	if m.active != 2 {
		t.Fatal("tab shortcut failed")
	}
	press('l', tea.ModAlt)
	if m.active != 0 {
		t.Fatal("next tab did not wrap")
	}
	_, _ = m.Update(tea.KeyboardEnhancementsMsg{Flags: 1})
	if m.prefix != "ctrl+m" {
		t.Fatal(m.prefix)
	}
	press(tea.KeyEnter, 0)
	if m.prefixed {
		t.Fatal("enhanced Enter captured")
	}
	press('m', tea.ModCtrl)
	press('c', 0)
	if m.dialog == nil {
		t.Fatal("new dialog not opened")
	}
	_, _ = m.Update(tea.KeyReleaseMsg{Code: 'c'})
	if m.swallowed['c'] {
		t.Fatal("consumed shortcut release not cleared")
	}
	press(tea.KeyEscape, 0)
	if m.dialog != nil {
		t.Fatal("dialog did not cancel")
	}
	press('m', tea.ModCtrl)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'e'})
	if cmd == nil {
		t.Fatal("expected editor command on prefix e")
	}
}

func TestIconsAndOverflow(t *testing.T) {
	for _, mode := range []string{"nerd", "unicode", "ascii"} {
		for _, state := range []term.State{term.Starting, term.Working, term.Done, term.NeedsInput, term.Exited, term.Crashed} {
			if icon(state, 0, mode) == "" {
				t.Fatalf("no icon for %s/%s", mode, state)
			}
		}
	}
	m := testModel(t)
	m.width = 40
	m.active = 2
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "refactor") {
		t.Fatal("active tab hidden", got)
	}
}

func TestPrefixSuspendAndShell(t *testing.T) {
	m := testModel(t)
	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.prefixed {
		t.Fatal("expected prefixed state")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'z'})
	if cmd == nil {
		t.Fatal("expected tea.Suspend cmd")
	}

	_, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, cmd = m.Update(tea.KeyPressMsg{Code: 't'})
	if cmd == nil {
		t.Fatal("expected exec cmd for shell")
	}
}

func TestShiftedPrefixShortcuts(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: '?', Text: "?"},
		{Code: '/', ShiftedCode: '?', Mod: tea.ModShift},
	} {
		m := testModel(t)
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		m.Update(key)
		if !m.help {
			t.Fatalf("help shortcut lost: %+v", key)
		}
	}
}

func TestStaleForgeResultsDoNotReplaceDialog(t *testing.T) {
	m := testModel(t)
	old := &forgeDialog{action: "pr"}
	current := &forgeDialog{action: "merge", busy: true}
	m.forgeUI = current
	m.Update(prDraftMsg{dialog: old})
	if m.forgeUI != current || !current.busy {
		t.Fatal("stale PR result replaced dialog")
	}
	m.Update(reopenListMsg{dialog: old})
	if m.forgeUI != current || !current.busy {
		t.Fatal("stale reopen result changed dialog")
	}
}

func TestShiftedCreatePRShortcut(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'P', Text: "P"},
		{Code: 'p', ShiftedCode: 'P', Mod: tea.ModShift},
	} {
		m := testModel(t)
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		_, cmd := m.Update(key)
		if cmd == nil || m.forgeUI == nil || m.forgeUI.action != "pr" {
			t.Fatalf("PR shortcut lost: %+v", key)
		}
	}
}

func TestPRTitleAcceptsPrintableKeys(t *testing.T) {
	m := testModel(t)
	m.forgeUI = newPRDialog(m.tabs[0].task, forge.NewPR{}, 80, 24)
	var want strings.Builder
	for code := rune(' '); code <= '~'; code++ {
		m.Update(tea.KeyPressMsg{Code: code})
		want.WriteRune(code)
	}
	if got := m.forgeUI.title.Value(); got != want.String() {
		t.Fatalf("got %q, want %q", got, want.String())
	}
}
