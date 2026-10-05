package core

import (
	"context"
	"strings"
	"testing"

	"github.com/mmoehabb/maestro/internal/config"
)

func TestSlug(t *testing.T) {
	for title, want := range map[string]string{" Fix AUTH! ": "fix-auth", "../../escape": "escape", "---": "", "a   b___c": "a-b-c"} {
		if got := Slug(title); got != want {
			t.Errorf("Slug(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestManualPromptRejectedBeforeProvisioning(t *testing.T) {
	s := &TaskService{Config: config.Config{Agents: map[string]config.Agent{"kimi": {Cmd: "kimi", ManualPrompt: true}}}}
	// No store or repository: validation must finish before either is touched.
	if _, err := s.create(context.Background(), NewTask{Title: "Test", Agent: "kimi", Prompt: "fix it"}); err == nil || !strings.Contains(err.Error(), "manual prompt entry") {
		t.Fatalf("expected actionable prompt error, got %v", err)
	}
}
