package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CreateSession asks agents such as Cursor to allocate an ID before launch.
// Arguments are passed directly to the configured executable, without a shell.
func (g Generic) CreateSession(ctx context.Context, dir string) (string, error) {
	if len(g.Config.SessionCreate) == 0 {
		return "", errors.New("no session creation command configured")
	}
	cmd := exec.CommandContext(ctx, g.Config.Cmd, g.Config.SessionCreate...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("agent %s: %w", g.Name, err)
	}
	id := strings.TrimSpace(string(b))
	if id == "" || len(id) > 512 || strings.ContainsAny(id, " \t\r\n\x00\x1b") {
		return "", fmt.Errorf("agent %s returned an invalid session ID", g.Name)
	}
	return id, nil
}

// DiscoverSession only reads identity metadata; normalized transcripts belong to
// P2. A unique task worktree is mandatory for matching native sessions safely.
func (g Generic) DiscoverSession(ctx context.Context, dir string, since time.Time) (string, error) {
	if g.Config.SessionFile != "" {
		f, err := os.Open(filepath.Join(dir, g.Config.SessionFile))
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return "", err
		}
		if info.ModTime().Before(since) {
			return "", nil
		}
		b, err := io.ReadAll(io.LimitReader(f, 513))
		if err != nil {
			return "", err
		}
		id := strings.TrimSpace(string(b))
		if len(id) > 512 || strings.ContainsAny(id, "\r\n\x00") {
			return "", errors.New("invalid session ID file")
		}
		return id, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch g.Name {
	case "kimi":
		return discoverKimi(ctx, filepath.Join(home, ".kimi-code", "sessions"), dir, since)
	case "codex":
		root := os.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		return discoverCodex(ctx, filepath.Join(root, "sessions"), dir, since)
	case "agy":
		b, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "cache", "last_conversations.json"))
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var entries map[string]string
		if err := json.Unmarshal(b, &entries); err != nil {
			return "", err
		}
		for path, id := range entries {
			if sameDirectory(path, dir) {
				return id, nil
			}
		}
	case "opencode":
		cmd := exec.CommandContext(ctx, g.Config.Cmd, "session", "list", "--format", "json")
		cmd.Dir = dir
		b, err := cmd.Output()
		if err != nil {
			return "", err
		}
		var entries []struct {
			ID, Directory string
			Created       int64
		}
		if err := json.Unmarshal(b, &entries); err != nil {
			return "", err
		}
		var id string
		var latest int64
		for _, e := range entries {
			if sameDirectory(e.Directory, dir) && e.Created >= since.Add(-time.Second).UnixMilli() && e.Created >= latest {
				latest, id = e.Created, e.ID
			}
		}
		return id, nil
	}
	return "", nil
}

// Kimi Code writes state.json under sessions/<workDirKey>/<sessionId>/.
// Only identity metadata is read; conversation content stays with the agent.
func discoverKimi(ctx context.Context, root, dir string, since time.Time) (string, error) {
	var id string
	var latest time.Time
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "state.json" {
			return nil
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		var state struct {
			WorkDir   string
			CreatedAt time.Time
		}
		// A file can be observed mid-write; retry on the next discovery tick.
		if json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&state) != nil {
			return nil
		}
		if state.WorkDir != "" && sameDirectory(state.WorkDir, dir) && !state.CreatedAt.Before(since.Add(-time.Second)) && state.CreatedAt.After(latest) {
			id, latest = filepath.Base(filepath.Dir(path)), state.CreatedAt
		}
		return nil
	})
	return id, err
}

func sameDirectory(a, b string) bool {
	aa, errA := filepath.EvalSymlinks(a)
	bb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return aa == bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func discoverCodex(ctx context.Context, root, dir string, since time.Time) (string, error) {
	var id string
	var latest time.Time
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(since) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		var record struct {
			Type    string
			Payload struct{ ID, Cwd, Timestamp string }
		}
		if scanner.Scan() {
			_ = json.Unmarshal(scanner.Bytes(), &record)
		}
		_ = f.Close()
		if record.Type != "session_meta" || !sameDirectory(record.Payload.Cwd, dir) {
			return nil
		}
		created, err := time.Parse(time.RFC3339Nano, record.Payload.Timestamp)
		if err != nil {
			created = info.ModTime()
		}
		if !created.Before(since.Add(-time.Second)) && created.After(latest) {
			id, latest = record.Payload.ID, created
		}
		return nil
	})
	return id, err
}

func (g Generic) Hints() []string {
	if g.Config.InputHints != nil {
		return g.Config.InputHints
	}
	return []string{"allow command?", "approve?", "do you want to proceed?", "allow this command", "permission required"}
}
