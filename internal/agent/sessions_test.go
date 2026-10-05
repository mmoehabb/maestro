package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/config"
)

func TestCodexSessionDiscoveryMatchesWorktree(t *testing.T) {
	root, worktree, other := t.TempDir(), t.TempDir(), t.TempDir()
	now := time.Now()
	write := func(name, id, dir string, ts time.Time) {
		t.Helper()
		record := map[string]any{"type": "session_meta", "payload": map[string]string{"id": id, "cwd": dir, "timestamp": ts.Format(time.RFC3339Nano)}}
		b, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "rollout-"+name+".jsonl"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("old", "old-id", worktree, now.Add(-time.Hour))
	write("wanted", "wanted-id", worktree, now)
	write("other", "other-id", other, now.Add(time.Minute))
	id, err := discoverCodex(context.Background(), root, worktree, now.Add(-time.Second))
	if err != nil || id != "wanted-id" {
		t.Fatalf("got %q, %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverCodex(ctx, root, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestGenericSessionFile(t *testing.T) {
	dir := t.TempDir()
	g := Generic{Name: "custom", Config: config.Agent{SessionFile: "session-id"}}
	if id, err := g.DiscoverSession(context.Background(), dir, time.Time{}); err != nil || id != "" {
		t.Fatal(id, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session-id"), []byte("native-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, err := g.DiscoverSession(context.Background(), dir, time.Time{}); err != nil || id != "native-123" {
		t.Fatal(id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), dir, time.Now().Add(time.Minute)); err != nil || id != "" {
		t.Fatal("stale ID accepted", id, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session-id"), []byte("two\nlines"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.DiscoverSession(context.Background(), dir, time.Time{}); err == nil {
		t.Fatal("invalid session ID accepted")
	}
}
