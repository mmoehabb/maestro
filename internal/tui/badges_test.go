package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mmoehabb/maestro/internal/store"
)

func TestActiveBadgePreservesANSI(t *testing.T) {
	m := testModel(t)
	m.tabs[0].task = store.Task{ID: 1, Slug: "auth", PRNumber: 42, Lifecycle: "pr_open", PRState: "open", CIState: "failure"}
	rendered := ansi.Strip(m.renderTabs())
	if strings.Contains(rendered, "[38;") || !strings.Contains(rendered, "#42!") {
		t.Fatalf("badge escape sequences corrupted: %q", rendered)
	}
}
