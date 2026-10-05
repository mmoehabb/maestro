package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestKimiSessionDiscoveryMatchesWorktree(t *testing.T) {
	root, worktree, other := t.TempDir(), t.TempDir(), t.TempDir()
	now := time.Now().UTC()
	for _, tc := range []struct {
		id, dir string
		created time.Time
	}{
		{"old", worktree, now.Add(-time.Hour)},
		{"wanted", worktree, now},
		{"other", other, now.Add(time.Minute)},
	} {
		dir := filepath.Join(root, "workspace", tc.id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(map[string]any{"workDir": tc.dir, "createdAt": tc.created})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := discoverKimi(context.Background(), root, worktree, now.Add(-time.Second)); err != nil || id != "wanted" {
		t.Fatalf("got %q, %v", id, err)
	}
	if id, err := discoverKimi(context.Background(), root, worktree, now.Add(time.Hour)); err != nil || id != "" {
		t.Fatalf("stale session accepted: %q, %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverKimi(ctx, root, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestSessionCreatorProcess(t *testing.T) {
	if os.Getenv("MAESTRO_SESSION_CREATOR_HELPER") != "1" {
		return
	}
	fmt.Print(os.Getenv("MAESTRO_SESSION_CREATOR_OUTPUT"))
	if os.Getenv("MAESTRO_SESSION_CREATOR_FAIL") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCreateSession(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAESTRO_SESSION_CREATOR_HELPER", "1")
	g := Generic{Name: "custom", Config: config.Agent{Cmd: exe, SessionCreate: []string{"-test.run=^TestSessionCreatorProcess$"}}}
	for _, output := range []string{"native-id\r\n", "", "two\nlines", "not an id", "\x1b[32mid"} {
		t.Setenv("MAESTRO_SESSION_CREATOR_OUTPUT", output)
		id, err := g.CreateSession(context.Background(), t.TempDir())
		if output == "native-id\r\n" {
			if err != nil || id != "native-id" {
				t.Fatal(id, err)
			}
		} else if err == nil {
			t.Fatalf("invalid output accepted: %q", output)
		}
	}
	t.Setenv("MAESTRO_SESSION_CREATOR_FAIL", "1")
	t.Setenv("MAESTRO_SESSION_CREATOR_OUTPUT", "valid-looking-id")
	if _, err := g.CreateSession(context.Background(), t.TempDir()); err == nil {
		t.Fatal("failed creation accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.CreateSession(ctx, t.TempDir()); err == nil {
		t.Fatal("creation ignored cancellation")
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
