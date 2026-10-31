package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGeminiDiscoveryMatchesProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", home)
	dir := t.TempDir()
	root := filepath.Join(home, ".gemini", "tmp", "project", "chats")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := fmt.Sprintf(`{"sessionId":"wanted","projectHash":"%x","startTime":"%s"}`, sha256.Sum256([]byte(dir)), now.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(root, "session-test.jsonl"), []byte(data+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := Generic{Name: "gemini"}
	id, err := g.DiscoverSession(context.Background(), dir, now.Add(-time.Second))
	if err != nil || id != "wanted" {
		t.Fatalf("got %q, %v", id, err)
	}
	id, err = g.DiscoverSession(context.Background(), t.TempDir(), now.Add(-time.Second))
	if err != nil || id != "" {
		t.Fatalf("wrong workspace: %q, %v", id, err)
	}
	id, err = g.DiscoverSession(context.Background(), dir, now.Add(time.Minute))
	if err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}
}
