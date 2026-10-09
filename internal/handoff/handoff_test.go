package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mmoehabb/maestro/internal/store"
)

func TestBudgetAndRecentContext(t *testing.T) {
	h := store.History{Task: store.Task{Title: "Login", Goal: "Fix authentication", Notes: "TODO: retain coverage"}}
	for i := range 30 {
		h.Turns = append(h.Turns, store.Turn{SessionID: 1, Role: "assistant", Content: strings.Repeat("older ", 200)})
		_ = i
	}
	h.Turns = append(h.Turns, store.Turn{SessionID: 2, Role: "assistant", Content: "Recent fix: validate the cookie."})
	out := Build(h, "Clean tree", "codex", 6000)
	if Estimate(out) > 6000 || !strings.Contains(out, "Recent fix") || !strings.Contains(out, "Fix authentication") || !strings.Contains(out, "omitted") {
		t.Fatal(out)
	}
	if out != Build(h, "Clean tree", "codex", 6000) {
		t.Fatal("nondeterministic handoff")
	}
	for _, n := range []int{1, 12, 100, 6000} {
		out := Build(h, strings.Repeat("你好", 10000), "codex", n)
		if Estimate(out) > n || !utf8.ValidString(out) {
			t.Fatalf("invalid budget %d", n)
		}
	}
}

func TestWriteAndSymlinkRefusal(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "first"); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "second"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".maestro", "local", "handoff.md")
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "second" {
		t.Fatal(string(b), err)
	}
	if err = os.Remove(p); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err = os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, p); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if err = Write(dir, "overwrite"); err == nil {
		t.Fatal("followed handoff symlink")
	}
	b, err = os.ReadFile(outside)
	if err != nil || string(b) != "keep" {
		t.Fatal("outside content changed")
	}
}

func TestTerminalFallbackKeepsNewestLines(t *testing.T) {
	h := store.History{Turns: []store.Turn{{SessionID: 1, Role: "terminal", Content: strings.Repeat("old output 你好\n", 2000) + "LATEST RESULT\nNEXT STEP: verify deployment"}}}
	for _, budget := range []int{512, 1000, 6000} {
		out := Build(h, "", "agy", budget)
		if len(out) > budget || !utf8.ValidString(out) || !strings.Contains(out, "LATEST RESULT\nNEXT STEP: verify deployment") {
			t.Fatalf("budget %d: %s", budget, out)
		}
	}
}

func TestIncompleteNativeHistoryRetainsTerminal(t *testing.T) {
	h := store.History{Sessions: []store.Session{{ID: 1}}, Turns: []store.Turn{{SessionID: 1, Role: "assistant", Content: "Imported prefix"}, {SessionID: 1, Role: "terminal", Content: "Later result available only in terminal"}}}
	out := Build(h, "", "agy", 6000)
	if !strings.Contains(out, "Imported prefix") || !strings.Contains(out, "Later result") {
		t.Fatal(out)
	}
	h.Sessions[0].NativeComplete = true
	out = Build(h, "", "agy", 6000)
	if !strings.Contains(out, "Imported prefix") || strings.Contains(out, "Later result") {
		t.Fatal(out)
	}
}
