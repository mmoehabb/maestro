package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/config"
)

func isolateAgentTestEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{
		"HOME", "USERPROFILE", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA",
		"CLINE_DATA_DIR", "CLINE_DB_DATA_DIR",
		"QWEN_HOME", "PI_CODING_AGENT_DIR", "VIBE_HOME",
		"GOOSE_PATH_ROOT", "KIRO_HOME", "KIRO_DATA_DIR", "FACTORY_HOME",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

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

func TestNewAgentPresetsAndCommands(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(config.Paths{ConfigFile: filepath.Join(dir, "absent"), DataDir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	const id = "cd830a00-8b2c-4a82-bb4b-56ab89ed5d01"
	const prompt = "--help; $(touch nope) 'quoted'\nsecond line"

	for _, tc := range []struct {
		name         string
		cmd          string
		manualPrompt bool
		newArgs      []string
		resumeArgs   []string
		promptArgs   []string
	}{
		{"cline", "cline", true, []string{"--tui"}, []string{"--tui", "--id", id}, nil},
		{"qwen", "qwen", false, nil, []string{"--resume", id}, []string{"--prompt-interactive", prompt}},
		{"pi", "pi", false, nil, []string{"--session", id}, []string{"--", prompt}},
		{"vibe", "vibe", true, nil, []string{"--resume", id}, nil},
		{"goose", "goose", true, []string{"session"}, []string{"session", "--resume", "--session-id", id}, nil},
		{"kiro", "kiro-cli", true, []string{"chat"}, []string{"chat", "--resume-id", id}, nil},
		{"droid", "droid", true, nil, []string{"--resume", id}, nil},
		{"auggie", "auggie", true, nil, []string{"--resume", id}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preset, ok := cfg.Agents[tc.name]
			if !ok {
				t.Fatalf("missing preset %q", tc.name)
			}
			if preset.Cmd != tc.cmd || preset.ManualPrompt != tc.manualPrompt || preset.GenerateSessionID {
				t.Fatalf("unexpected preset %q: %+v", tc.name, preset)
			}
			g := Generic{Name: tc.name, Config: preset}
			for _, fresh := range []bool{true, false} {
				for _, p := range []string{"", prompt} {
					cmd, err := g.Command(context.Background(), LaunchSpec{
						Dir:        dir,
						SessionID:  id,
						NewSession: fresh,
						Prompt:     p,
					})
					if tc.manualPrompt && p != "" {
						if err == nil || !strings.Contains(err.Error(), "manual prompt entry") {
							t.Fatalf("%s must reject startup prompt: %v", tc.name, err)
						}
						continue
					}
					if err != nil {
						t.Fatalf("fresh=%v prompt=%q: %v", fresh, p, err)
					}
					want := []string{tc.cmd}
					if fresh {
						want = append(want, tc.newArgs...)
					} else {
						want = append(want, tc.resumeArgs...)
					}
					if p != "" {
						want = append(want, tc.promptArgs...)
					}
					if !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != dir {
						t.Fatalf("fresh=%v: got %q in %s, want %q in %s", fresh, cmd.Args, cmd.Dir, want, dir)
					}
				}
			}
		})
	}
}

func TestClineDiscoverySQLiteAndFallback(t *testing.T) {
	isolateAgentTestEnv(t)
	ctx := context.Background()
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	dataDir := filepath.Join(t.TempDir(), "cline # data")
	dbDir := filepath.Join(dataDir, "db")
	t.Setenv("CLINE_DATA_DIR", dataDir)
	t.Setenv("CLINE_DB_DATA_DIR", dbDir)

	missingDB := filepath.Join(dbDir, "sessions.db")
	if _, err := openReadOnlySQLite(ctx, missingDB); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err := os.Stat(missingDB); !os.IsNotExist(err) {
		t.Fatalf("openReadOnlySQLite created missing DB: %v", err)
	}

	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", missingDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (
		session_id TEXT PRIMARY KEY,
		cwd TEXT,
		workspace_root TEXT,
		started_at TEXT,
		is_subagent INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, cwd, ws string
		started     time.Time
		subagent    int
	}{
		{"old-id", worktree, "", now.Add(-time.Hour), 0},
		{"wanted-id", "", worktree, now, 0},
		{"subagent-id", worktree, "", now.Add(time.Second), 1},
		{"other-id", other, "", now.Add(2 * time.Second), 0},
	} {
		if _, err := db.Exec(`INSERT INTO sessions VALUES(?,?,?,?,?)`,
			row.id, row.cwd, row.ws, row.started.Format(time.RFC3339Nano), row.subagent); err != nil {
			t.Fatal(err)
		}
	}

	g := Generic{Name: "cline"}
	id, err := g.DiscoverSession(ctx, worktree, now.Add(-time.Second))
	if err != nil || id != "wanted-id" {
		t.Fatalf("got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(ctx, t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree matched: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(ctx, worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session matched: %q, %v", id, err)
	}

	ro, err := openReadOnlySQLite(ctx, missingDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Exec(`DELETE FROM sessions`); err == nil {
		_ = ro.Close()
		t.Fatal("sqlite connection is not read-only")
	}
	_ = ro.Close()

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.DiscoverSession(cancelCtx, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}

	// Fallback to manifest JSON files when sessions.db is absent.
	fallbackDataDir := filepath.Join(t.TempDir(), "fallback")
	t.Setenv("CLINE_DATA_DIR", fallbackDataDir)
	t.Setenv("CLINE_DB_DATA_DIR", filepath.Join(fallbackDataDir, "db"))
	for _, item := range []struct {
		dirName, fileName string
		payload           map[string]any
	}{
		{"old", "old.json", map[string]any{"session_id": "old-json", "cwd": worktree, "started_at": now.Add(-time.Hour).Format(time.RFC3339Nano)}},
		{"wanted", "wanted.json", map[string]any{"sessionId": "wanted-json", "workspaceRoot": worktree, "createdAt": now.UnixMilli()}},
		{"messages", "ignored.messages.json", map[string]any{"session_id": "msg-json", "cwd": worktree, "started_at": now.Add(time.Minute).Format(time.RFC3339Nano)}},
		{"other", "other.json", map[string]any{"session_id": "other-json", "cwd": other, "started_at": now.Add(time.Minute).Format(time.RFC3339Nano)}},
	} {
		dir := filepath.Join(fallbackDataDir, "sessions", item.dirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(item.payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, item.fileName), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := g.DiscoverSession(ctx, worktree, now.Add(-time.Second)); err != nil || id != "wanted-json" {
		t.Fatalf("fallback got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(ctx, t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("fallback wrong worktree: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(ctx, worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("fallback stale: %q, %v", id, err)
	}
}

func TestQwenDiscoveryMatchesProject(t *testing.T) {
	isolateAgentTestEnv(t)
	qwenHome := t.TempDir()
	t.Setenv("QWEN_HOME", qwenHome)
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	chats := filepath.Join(qwenHome, "projects", "proj-a", "chats")
	archive := filepath.Join(chats, "archive")
	for _, p := range []string{chats, archive} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONL := func(path string, obj map[string]any) {
		t.Helper()
		b, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONL(filepath.Join(chats, "old.jsonl"), map[string]any{"sessionId": "old-id", "cwd": worktree, "timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano)})
	writeJSONL(filepath.Join(chats, "wanted.jsonl"), map[string]any{"sessionId": "wanted-id", "cwd": worktree, "timestamp": now.Format(time.RFC3339Nano)})
	writeJSONL(filepath.Join(chats, "wanted.ledger.jsonl"), map[string]any{"sessionId": "ledger-id", "cwd": worktree, "timestamp": now.Add(time.Minute).Format(time.RFC3339Nano)})
	writeJSONL(filepath.Join(archive, "archived.jsonl"), map[string]any{"sessionId": "archived-id", "cwd": worktree, "timestamp": now.Add(time.Minute).Format(time.RFC3339Nano)})
	writeJSONL(filepath.Join(chats, "other.jsonl"), map[string]any{"sessionId": "other-id", "cwd": other, "timestamp": now.Add(time.Minute).Format(time.RFC3339Nano)})

	g := Generic{Name: "qwen"}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
		t.Fatalf("got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.DiscoverSession(ctx, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestPiDiscoveryMatchesProject(t *testing.T) {
	isolateAgentTestEnv(t)
	piDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", piDir)
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	sessDir := filepath.Join(piDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeHeader := func(name, typ, id, cwd string, ts time.Time) {
		t.Helper()
		b, err := json.Marshal(map[string]any{"type": typ, "id": id, "cwd": cwd, "timestamp": ts.Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sessDir, name), append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeHeader("old.jsonl", "session", "old-id", worktree, now.Add(-time.Hour))
	writeHeader("wanted.jsonl", "session", "wanted-id", worktree, now)
	writeHeader("not-session.jsonl", "message", "msg-id", worktree, now.Add(time.Minute))
	writeHeader("other.jsonl", "session", "other-id", other, now.Add(time.Minute))

	g := Generic{Name: "pi"}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
		t.Fatalf("got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.DiscoverSession(ctx, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestVibeDiscoveryMatchesProject(t *testing.T) {
	isolateAgentTestEnv(t)
	vibeHome := t.TempDir()
	t.Setenv("VIBE_HOME", vibeHome)
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	for _, item := range []struct {
		dirName, id, cwd string
		start            time.Time
	}{
		{"session_old", "old-id", worktree, now.Add(-time.Hour)},
		{"session_wanted", "wanted-id", worktree, now},
		{"session_other", "other-id", other, now.Add(time.Minute)},
	} {
		dir := filepath.Join(vibeHome, "logs", "session", item.dirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(map[string]any{
			"session_id":  item.id,
			"start_time":  item.start.Format(time.RFC3339Nano),
			"environment": map[string]string{"working_directory": item.cwd},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	g := Generic{Name: "vibe"}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
		t.Fatalf("got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.DiscoverSession(ctx, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestGooseDiscoveryCLIAndSQLiteFallback(t *testing.T) {
	isolateAgentTestEnv(t)
	ctx := context.Background()
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	arrayJSON, err := json.Marshal([]map[string]any{
		{"id": "old-id", "working_dir": worktree, "created_at": now.Add(-time.Hour).Format(time.RFC3339Nano)},
		{"id": "wanted-id", "working_dir": worktree, "created_at": now.Format(time.RFC3339Nano)},
		{"id": "other-id", "working_dir": other, "created_at": now.Add(time.Minute).Format(time.RFC3339Nano)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := parseGooseSessionList(arrayJSON, worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
		t.Fatalf("array parse got %q, %v", id, err)
	}
	wrappedJSON, err := json.Marshal(map[string]any{
		"sessions": []map[string]any{
			{"id": "wrapped-id", "working_dir": worktree, "created_at": now.Format(time.RFC3339Nano)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := parseGooseSessionList(wrappedJSON, worktree, now.Add(-time.Second)); err != nil || id != "wrapped-id" {
		t.Fatalf("wrapped parse got %q, %v", id, err)
	}
	if id, err := parseGooseSessionList(arrayJSON, t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := parseGooseSessionList(arrayJSON, worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}

	if runtime.GOOS != "windows" {
		script := filepath.Join(t.TempDir(), "goose-mock.sh")
		content := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", string(arrayJSON))
		if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
		g := Generic{Name: "goose", Config: config.Agent{Cmd: script}}
		if id, err := g.DiscoverSession(ctx, worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
			t.Fatalf("CLI discovery got %q, %v", id, err)
		}
	}

	// Fallback to read-only SQLite via GOOSE_PATH_ROOT when CLI binary is absent.
	gooseRoot := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", gooseRoot)
	dbPath := filepath.Join(gooseRoot, "data", "sessions", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, working_dir TEXT, created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, dir string
		ts      time.Time
	}{
		{"db-old", worktree, now.Add(-time.Hour)},
		{"db-wanted", worktree, now},
		{"db-other", other, now.Add(time.Minute)},
	} {
		if _, err := db.Exec(`INSERT INTO sessions VALUES(?,?,?)`, row.id, row.dir, row.ts.Format("2006-01-02 15:04:05.999999")); err != nil {
			t.Fatal(err)
		}
	}

	gFallback := Generic{Name: "goose", Config: config.Agent{Cmd: filepath.Join(t.TempDir(), "absent-goose")}}
	if id, err := gFallback.DiscoverSession(ctx, worktree, now.Add(-time.Second)); err != nil || id != "db-wanted" {
		t.Fatalf("DB fallback got %q, %v", id, err)
	}
	if id, err := gFallback.DiscoverSession(ctx, t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("DB wrong worktree: %q, %v", id, err)
	}
	if id, err := gFallback.DiscoverSession(ctx, worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("DB stale session: %q, %v", id, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := gFallback.DiscoverSession(cancelCtx, worktree, now); err == nil {
		t.Fatal("DB fallback ignored cancellation")
	}
}

func TestKiroDiscoveryV3AndV2SQLite(t *testing.T) {
	isolateAgentTestEnv(t)
	ctx := context.Background()
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	kiroHome := t.TempDir()
	kiroData := t.TempDir()
	t.Setenv("KIRO_HOME", kiroHome)
	t.Setenv("KIRO_DATA_DIR", kiroData)

	cliDir := filepath.Join(kiroHome, "sessions", "cli")
	if err := os.MkdirAll(cliDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON := func(name string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cliDir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("old.json", map[string]any{"session_id": "v3-old", "cwd": worktree, "created_at": now.Add(-time.Hour).Format(time.RFC3339Nano)})
	writeJSON("mid.json", map[string]any{"sessionId": "v3-mid", "cwd": worktree, "createdAt": now.Format(time.RFC3339Nano)})
	writeJSON("other.json", map[string]any{"session_id": "v3-other", "cwd": other, "created_at": now.Add(time.Minute).Format(time.RFC3339Nano)})

	dbPath := filepath.Join(kiroData, "data.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE conversations_v2 (key TEXT NOT NULL, conversation_id TEXT NOT NULL, value TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (key, conversation_id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversations_v2 VALUES(?,?,?,?,?)`, worktree, "v2-newer", "{}", now.Add(5*time.Second).UnixMilli(), now.Add(5*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversations_v2 VALUES(?,?,?,?,?)`, other, "v2-other", "{}", now.Add(time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	g := Generic{Name: "kiro"}
	if id, err := g.DiscoverSession(ctx, worktree, now.Add(-time.Second)); err != nil || id != "v2-newer" {
		t.Fatalf("expected newer V2 session, got %q, %v", id, err)
	}

	// Add an even newer V3 JSONL file and verify V3 wins when latest.
	jsonlLine, err := json.Marshal(map[string]any{
		"type":       "session_start",
		"session_id": "v3-newest",
		"cwd":        worktree,
		"timestamp":  now.Add(10 * time.Second).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "newest.jsonl"), append(jsonlLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, err := g.DiscoverSession(ctx, worktree, now.Add(-time.Second)); err != nil || id != "v3-newest" {
		t.Fatalf("expected newest V3 JSONL session, got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(ctx, t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(ctx, worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.DiscoverSession(cancelCtx, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestDroidDiscoveryMatchesProject(t *testing.T) {
	isolateAgentTestEnv(t)
	factoryHome := t.TempDir()
	t.Setenv("FACTORY_HOME", factoryHome)
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	sessDir := filepath.Join(factoryHome, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLine := func(name string, payload map[string]any) string {
		t.Helper()
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(sessDir, name)
		if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	writeLine("old.jsonl", map[string]any{"type": "session_start", "id": "old-id", "cwd": worktree, "timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano)})
	writeLine("wanted.jsonl", map[string]any{"type": "session_start", "id": "wanted-id", "lastCwd": worktree, "timestamp": now.Format(time.RFC3339Nano)})
	writeLine("other-type.jsonl", map[string]any{"type": "message", "id": "ignored-id", "cwd": worktree, "timestamp": now.Add(time.Minute).Format(time.RFC3339Nano)})
	writeLine("other.jsonl", map[string]any{"type": "session_start", "id": "other-id", "cwd": other, "timestamp": now.Add(time.Minute).Format(time.RFC3339Nano)})

	g := Generic{Name: "droid"}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
		t.Fatalf("got %q, %v", id, err)
	}

	// Verify mtime fallback when timestamp is omitted.
	noTS := writeLine("mtime.jsonl", map[string]any{"type": "session_start", "id": "mtime-id", "cwd": worktree})
	future := now.Add(15 * time.Second)
	if err := os.Chtimes(noTS, future, future); err != nil {
		t.Fatal(err)
	}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(-time.Second)); err != nil || id != "mtime-id" {
		t.Fatalf("mtime fallback got %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := g.DiscoverSession(context.Background(), worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.DiscoverSession(ctx, worktree, now); err == nil {
		t.Fatal("discovery ignored cancellation")
	}
}

func TestAuggieDiscoveryCLIJSON(t *testing.T) {
	isolateAgentTestEnv(t)
	ctx := context.Background()
	worktree, other := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	arrayJSON, err := json.Marshal([]map[string]any{
		{"sessionId": "old-id", "workspace": worktree, "createdAt": now.Add(-time.Hour).Format(time.RFC3339Nano)},
		{"id": "wanted-id", "directory": worktree, "created": now.Format(time.RFC3339Nano)},
		{"sessionId": "other-id", "cwd": other, "createdAt": now.Add(time.Minute).Format(time.RFC3339Nano)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := parseAuggieSessionList(arrayJSON, worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
		t.Fatalf("array parse got %q, %v", id, err)
	}
	wrappedJSON, err := json.Marshal(map[string]any{
		"sessions": []map[string]any{
			{"sessionId": "wrapped-id", "workspace": worktree, "createdAt": now.Format(time.RFC3339Nano)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := parseAuggieSessionList(wrappedJSON, worktree, now.Add(-time.Second)); err != nil || id != "wrapped-id" {
		t.Fatalf("wrapped parse got %q, %v", id, err)
	}
	if id, err := parseAuggieSessionList(arrayJSON, t.TempDir(), now.Add(-time.Second)); err != nil || id != "" {
		t.Fatalf("wrong worktree: %q, %v", id, err)
	}
	if id, err := parseAuggieSessionList(arrayJSON, worktree, now.Add(time.Minute)); err != nil || id != "" {
		t.Fatalf("stale session: %q, %v", id, err)
	}

	if runtime.GOOS != "windows" {
		script := filepath.Join(t.TempDir(), "auggie-mock.sh")
		content := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", string(arrayJSON))
		if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
		g := Generic{Name: "auggie", Config: config.Agent{Cmd: script}}
		if id, err := g.DiscoverSession(ctx, worktree, now.Add(-time.Second)); err != nil || id != "wanted-id" {
			t.Fatalf("CLI discovery got %q, %v", id, err)
		}
	}
}

func TestParseTimeHelpers(t *testing.T) {
	wantRFC, _ := time.Parse(time.RFC3339Nano, "2026-11-21T16:15:14.123456Z")
	if got := parseTimeString("2026-11-21T18:15:14.123456+02:00"); !got.Equal(wantRFC) {
		t.Fatalf("RFC3339Nano got %v, want %v", got, wantRFC)
	}
	wantSQLite := time.Date(2026, 11, 21, 16, 15, 14, 123456000, time.UTC)
	if got := parseTimeString("2026-11-21 16:15:14.123456"); !got.Equal(wantSQLite) {
		t.Fatalf("SQLite datetime got %v, want %v", got, wantSQLite)
	}
	if got := parseTimeString("2026-11-21 18:15:14.123456+02:00"); !got.Equal(wantSQLite) {
		t.Fatalf("SQLite offset datetime got %v, want %v", got, wantSQLite)
	}
	if got := parseTimeString("1700000000"); !got.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("Unix sec string got %v", got)
	}
	if got := parseTimeString("1700000000123"); !got.Equal(time.UnixMilli(1700000000123).UTC()) {
		t.Fatalf("Unix milli string got %v", got)
	}
	for _, bad := range []string{"", "   ", "not-a-time", "0", "-5"} {
		if got := parseTimeString(bad); !got.IsZero() {
			t.Fatalf("expected zero time for %q, got %v", bad, got)
		}
	}

	if got := parseTimeJSON([]byte(`1700000000`)); !got.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("JSON unix sec got %v", got)
	}
	if got := parseTimeJSON([]byte(`1700000000123`)); !got.Equal(time.UnixMilli(1700000000123).UTC()) {
		t.Fatalf("JSON unix milli got %v", got)
	}
	if got := parseTimeJSON([]byte(`"2026-11-21T16:15:14.123456Z"`)); !got.Equal(wantRFC) {
		t.Fatalf("JSON RFC3339Nano got %v", got)
	}
	for _, bad := range []string{"", "null", "true", `""`, `"invalid"`, "0"} {
		if got := parseTimeJSON([]byte(bad)); !got.IsZero() {
			t.Fatalf("expected zero time for JSON %s, got %v", bad, got)
		}
	}
	if got := parseTimeJSON(nil); !got.IsZero() {
		t.Fatalf("expected zero time for nil, got %v", got)
	}
}
