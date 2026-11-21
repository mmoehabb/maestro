package agent

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func openReadOnlySQLite(ctx context.Context, path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("sqlite path is a directory")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)", "query_only(1)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func parseTimeString(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if n >= 1_000_000_000_000 {
			return time.UnixMilli(n).UTC()
		}
		return time.Unix(n, 0).UTC()
	}
	return time.Time{}
}

func parseTimeJSON(b json.RawMessage) time.Time {
	if len(b) == 0 || string(b) == "null" {
		return time.Time{}
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return parseTimeString(s)
	}
	var n int64
	if json.Unmarshal(b, &n) == nil && n > 0 {
		if n >= 1_000_000_000_000 {
			return time.UnixMilli(n).UTC()
		}
		return time.Unix(n, 0).UTC()
	}
	return time.Time{}
}

func readFirstLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return append([]byte(nil), scanner.Bytes()...), nil
}

func validSessionID(id string) bool {
	return id != "" && len(id) <= 512 && !strings.ContainsAny(id, " \t\r\n\x00\x1b/\\")
}

func sessionEligible(ts, since, latest time.Time) bool {
	return !ts.IsZero() && !ts.Before(since.Add(-time.Second)) && ts.After(latest)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstTimeJSON(values ...json.RawMessage) time.Time {
	for _, v := range values {
		if ts := parseTimeJSON(v); !ts.IsZero() {
			return ts
		}
	}
	return time.Time{}
}

func discoverCline(ctx context.Context, home, dir string, since time.Time) (string, error) {
	dataDir := os.Getenv("CLINE_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".cline", "data")
	}
	dbDir := os.Getenv("CLINE_DB_DATA_DIR")
	if dbDir == "" {
		dbDir = filepath.Join(dataDir, "db")
	}
	if id, err := discoverClineDB(ctx, filepath.Join(dbDir, "sessions.db"), dir, since); err == nil && id != "" {
		return id, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var id string
	var latest time.Time
	err := filepath.WalkDir(filepath.Join(dataDir, "sessions"), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".json" || strings.HasSuffix(d.Name(), ".messages.json") {
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
		var meta struct {
			SessionIDSnake string          `json:"session_id"`
			SessionIDCamel string          `json:"sessionId"`
			Cwd            string          `json:"cwd"`
			WorkspaceCamel string          `json:"workspaceRoot"`
			WorkspaceSnake string          `json:"workspace_root"`
			StartedAt      json.RawMessage `json:"started_at"`
			StartedCamel   json.RawMessage `json:"startedAt"`
			CreatedSnake   json.RawMessage `json:"created_at"`
			CreatedCamel   json.RawMessage `json:"createdAt"`
		}
		if json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&meta) != nil {
			return nil
		}
		sid := firstNonEmpty(meta.SessionIDSnake, meta.SessionIDCamel)
		workDir := firstNonEmpty(meta.Cwd, meta.WorkspaceCamel, meta.WorkspaceSnake)
		ts := firstTimeJSON(meta.StartedAt, meta.StartedCamel, meta.CreatedSnake, meta.CreatedCamel)
		if validSessionID(sid) && workDir != "" && sameDirectory(workDir, dir) && sessionEligible(ts, since, latest) {
			id, latest = sid, ts
		}
		return nil
	})
	return id, err
}

func discoverClineDB(ctx context.Context, dbPath, dir string, since time.Time) (string, error) {
	db, err := openReadOnlySQLite(ctx, dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT session_id, COALESCE(cwd, ''), COALESCE(workspace_root, ''), COALESCE(started_at, '') FROM sessions WHERE COALESCE(is_subagent, 0) = 0 ORDER BY started_at DESC, session_id DESC`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var id string
	var latest time.Time
	for rows.Next() {
		var sid, cwd, workspaceRoot, startedAt string
		if err = rows.Scan(&sid, &cwd, &workspaceRoot, &startedAt); err != nil {
			return "", err
		}
		path := firstNonEmpty(cwd, workspaceRoot)
		ts := parseTimeString(startedAt)
		if validSessionID(sid) && path != "" && sameDirectory(path, dir) && sessionEligible(ts, since, latest) {
			id, latest = sid, ts
		}
	}
	return id, rows.Err()
}

func discoverQwen(ctx context.Context, home, dir string, since time.Time) (string, error) {
	root := os.Getenv("QWEN_HOME")
	if root == "" {
		root = filepath.Join(home, ".qwen")
	}
	var id string
	var latest time.Time
	err := filepath.WalkDir(filepath.Join(root, "projects"), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "archive" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "chats" || !strings.HasSuffix(d.Name(), ".jsonl") || strings.HasSuffix(d.Name(), ".ledger.jsonl") {
			return nil
		}
		line, err := readFirstLine(path)
		if err != nil {
			return nil
		}
		var header struct {
			SessionID string          `json:"sessionId"`
			Cwd       string          `json:"cwd"`
			Timestamp json.RawMessage `json:"timestamp"`
		}
		if json.Unmarshal(line, &header) != nil {
			return nil
		}
		ts := parseTimeJSON(header.Timestamp)
		if validSessionID(header.SessionID) && header.Cwd != "" && sameDirectory(header.Cwd, dir) && sessionEligible(ts, since, latest) {
			id, latest = header.SessionID, ts
		}
		return nil
	})
	return id, err
}

func discoverPi(ctx context.Context, home, dir string, since time.Time) (string, error) {
	root := os.Getenv("PI_CODING_AGENT_DIR")
	if root == "" {
		root = filepath.Join(home, ".pi", "agent")
	}
	var id string
	var latest time.Time
	err := filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		line, err := readFirstLine(path)
		if err != nil {
			return nil
		}
		var header struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Cwd       string          `json:"cwd"`
			Timestamp json.RawMessage `json:"timestamp"`
		}
		if json.Unmarshal(line, &header) != nil {
			return nil
		}
		ts := parseTimeJSON(header.Timestamp)
		if header.Type == "session" && validSessionID(header.ID) && header.Cwd != "" && sameDirectory(header.Cwd, dir) && sessionEligible(ts, since, latest) {
			id, latest = header.ID, ts
		}
		return nil
	})
	return id, err
}

func discoverVibe(ctx context.Context, home, dir string, since time.Time) (string, error) {
	root := os.Getenv("VIBE_HOME")
	if root == "" {
		root = filepath.Join(home, ".vibe")
	}
	var id string
	var latest time.Time
	err := filepath.WalkDir(filepath.Join(root, "logs", "session"), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "meta.json" {
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
		var meta struct {
			SessionID   string          `json:"session_id"`
			StartTime   json.RawMessage `json:"start_time"`
			Environment struct {
				WorkingDirectory string `json:"working_directory"`
			} `json:"environment"`
		}
		if json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&meta) != nil {
			return nil
		}
		ts := parseTimeJSON(meta.StartTime)
		if validSessionID(meta.SessionID) && meta.Environment.WorkingDirectory != "" && sameDirectory(meta.Environment.WorkingDirectory, dir) && sessionEligible(ts, since, latest) {
			id, latest = meta.SessionID, ts
		}
		return nil
	})
	return id, err
}

func discoverGoose(ctx context.Context, cmdName, home, dir string, since time.Time) (string, error) {
	cmd := exec.CommandContext(ctx, cmdName, "session", "list", "--format", "json")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	cmd.Stdout = &out
	cliErr := cmd.Run()
	if cliErr == nil {
		id, parseErr := parseGooseSessionList(out.Bytes(), dir, since)
		if parseErr == nil {
			return id, nil
		}
		cliErr = parseErr
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	id, dbErr := discoverGooseDB(ctx, home, dir, since)
	if dbErr == nil {
		return id, nil
	}
	return "", errors.Join(cliErr, dbErr)
}

func parseGooseSessionList(data []byte, dir string, since time.Time) (string, error) {
	type entry struct {
		ID         string          `json:"id"`
		WorkingDir string          `json:"working_dir"`
		CreatedAt  json.RawMessage `json:"created_at"`
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		var wrapped struct {
			Sessions []entry `json:"sessions"`
		}
		if wrapErr := json.Unmarshal(data, &wrapped); wrapErr != nil || wrapped.Sessions == nil {
			return "", err
		}
		entries = wrapped.Sessions
	}
	var id string
	var latest time.Time
	for _, e := range entries {
		ts := parseTimeJSON(e.CreatedAt)
		if validSessionID(e.ID) && e.WorkingDir != "" && sameDirectory(e.WorkingDir, dir) && sessionEligible(ts, since, latest) {
			id, latest = e.ID, ts
		}
	}
	return id, nil
}

func gooseCandidateDBPaths(home string) []string {
	if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" {
		return []string{
			filepath.Join(root, "data", "sessions", "sessions.db"),
			filepath.Join(root, "sessions", "sessions.db"),
		}
	}
	var paths []string
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		paths = append(paths, filepath.Join(xdgData, "goose", "sessions", "sessions.db"))
	}
	paths = append(paths,
		filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db"),
		filepath.Join(home, "Library", "Application Support", "Block.goose", "sessions", "sessions.db"),
		filepath.Join(home, "Library", "Application Support", "Block", "goose", "data", "sessions", "sessions.db"),
	)
	if appData := os.Getenv("APPDATA"); appData != "" {
		paths = append(paths, filepath.Join(appData, "Block", "goose", "data", "sessions", "sessions.db"))
	}
	return paths
}

func discoverGooseDB(ctx context.Context, home, dir string, since time.Time) (string, error) {
	var id string
	var latest time.Time
	var firstErr error
	opened := false
	for _, dbPath := range gooseCandidateDBPaths(home) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		db, err := openReadOnlySQLite(ctx, dbPath)
		if err != nil {
			if !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT id, COALESCE(working_dir, ''), COALESCE(created_at, '') FROM sessions ORDER BY created_at DESC, id DESC`)
		if err != nil {
			_ = db.Close()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		opened = true
		for rows.Next() {
			var sid, workingDir, createdAt string
			if scanErr := rows.Scan(&sid, &workingDir, &createdAt); scanErr != nil {
				err = scanErr
				break
			}
			ts := parseTimeString(createdAt)
			if validSessionID(sid) && workingDir != "" && sameDirectory(workingDir, dir) && sessionEligible(ts, since, latest) {
				id, latest = sid, ts
			}
		}
		err = errors.Join(err, rows.Err(), rows.Close(), db.Close())
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if opened {
		return id, nil
	}
	if firstErr != nil {
		return "", firstErr
	}
	return "", os.ErrNotExist
}

func discoverKiro(ctx context.Context, home, dir string, since time.Time) (string, error) {
	var id string
	var latest time.Time

	kiroHome := os.Getenv("KIRO_HOME")
	if kiroHome == "" {
		kiroHome = filepath.Join(home, ".kiro")
	}
	err := filepath.WalkDir(filepath.Join(kiroHome, "sessions", "cli"), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(d.Name())
		if ext != ".json" && ext != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		sid, cwd, ts, ok := readKiroV3Entry(path, ext)
		if !ok {
			return nil
		}
		if ts.IsZero() {
			ts = info.ModTime().UTC()
		}
		if validSessionID(sid) && cwd != "" && sameDirectory(cwd, dir) && sessionEligible(ts, since, latest) {
			id, latest = sid, ts
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	for _, dbPath := range kiroCandidateDBPaths(home) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		db, openErr := openReadOnlySQLite(ctx, dbPath)
		if openErr != nil {
			continue
		}
		rows, queryErr := db.QueryContext(ctx, `SELECT key, conversation_id, created_at FROM conversations_v2 WHERE created_at >= ? ORDER BY created_at DESC, conversation_id DESC`, since.Add(-time.Second).UnixMilli())
		if queryErr != nil {
			_ = db.Close()
			continue
		}
		for rows.Next() {
			var key, convID string
			var createdMs int64
			if scanErr := rows.Scan(&key, &convID, &createdMs); scanErr != nil {
				break
			}
			ts := time.UnixMilli(createdMs).UTC()
			if validSessionID(convID) && key != "" && sameDirectory(key, dir) && sessionEligible(ts, since, latest) {
				id, latest = convID, ts
			}
		}
		_ = rows.Err()
		_ = rows.Close()
		_ = db.Close()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return id, nil
}

func readKiroV3Entry(path, ext string) (string, string, time.Time, bool) {
	if ext == ".json" {
		f, err := os.Open(path)
		if err != nil {
			return "", "", time.Time{}, false
		}
		defer f.Close()
		var meta struct {
			SessionIDSnake string          `json:"session_id"`
			SessionIDCamel string          `json:"sessionId"`
			Cwd            string          `json:"cwd"`
			CreatedSnake   json.RawMessage `json:"created_at"`
			CreatedCamel   json.RawMessage `json:"createdAt"`
			Timestamp      json.RawMessage `json:"timestamp"`
		}
		if json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&meta) != nil {
			return "", "", time.Time{}, false
		}
		return firstNonEmpty(meta.SessionIDSnake, meta.SessionIDCamel), meta.Cwd, firstTimeJSON(meta.CreatedSnake, meta.CreatedCamel, meta.Timestamp), true
	}
	line, err := readFirstLine(path)
	if err != nil {
		return "", "", time.Time{}, false
	}
	var header struct {
		Type           string          `json:"type"`
		SessionIDSnake string          `json:"session_id"`
		SessionIDCamel string          `json:"sessionId"`
		ID             string          `json:"id"`
		Cwd            string          `json:"cwd"`
		Timestamp      json.RawMessage `json:"timestamp"`
		CreatedSnake   json.RawMessage `json:"created_at"`
	}
	if json.Unmarshal(line, &header) != nil {
		return "", "", time.Time{}, false
	}
	if header.Type != "" && header.Type != "session_start" && header.Type != "session" {
		return "", "", time.Time{}, false
	}
	return firstNonEmpty(header.SessionIDSnake, header.SessionIDCamel, header.ID), header.Cwd, firstTimeJSON(header.Timestamp, header.CreatedSnake), true
}

func kiroCandidateDBPaths(home string) []string {
	if dataDir := os.Getenv("KIRO_DATA_DIR"); dataDir != "" {
		return []string{filepath.Join(dataDir, "data.sqlite3")}
	}
	var paths []string
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		paths = append(paths, filepath.Join(xdgData, "kiro-cli", "data.sqlite3"))
	}
	paths = append(paths,
		filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3"),
		filepath.Join(home, "Library", "Application Support", "kiro-cli", "data.sqlite3"),
	)
	if appData := os.Getenv("APPDATA"); appData != "" {
		paths = append(paths, filepath.Join(appData, "kiro-cli", "data.sqlite3"))
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		paths = append(paths, filepath.Join(localAppData, "kiro-cli", "data.sqlite3"))
	}
	return paths
}

func discoverDroid(ctx context.Context, home, dir string, since time.Time) (string, error) {
	root := os.Getenv("FACTORY_HOME")
	if root == "" {
		root = filepath.Join(home, ".factory")
	}
	var id string
	var latest time.Time
	err := filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		line, err := readFirstLine(path)
		if err != nil {
			return nil
		}
		var header struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Cwd       string          `json:"cwd"`
			LastCwd   string          `json:"lastCwd"`
			Timestamp json.RawMessage `json:"timestamp"`
		}
		if json.Unmarshal(line, &header) != nil {
			return nil
		}
		cwd := firstNonEmpty(header.Cwd, header.LastCwd)
		ts := parseTimeJSON(header.Timestamp)
		if ts.IsZero() {
			ts = info.ModTime().UTC()
		}
		if header.Type == "session_start" && validSessionID(header.ID) && cwd != "" && sameDirectory(cwd, dir) && sessionEligible(ts, since, latest) {
			id, latest = header.ID, ts
		}
		return nil
	})
	return id, err
}

func discoverAuggie(ctx context.Context, cmdName, dir string, since time.Time) (string, error) {
	cmd := exec.CommandContext(ctx, cmdName, "session", "list", "--json")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return parseAuggieSessionList(out.Bytes(), dir, since)
}

func parseAuggieSessionList(data []byte, dir string, since time.Time) (string, error) {
	type entry struct {
		SessionID string          `json:"sessionId"`
		ID        string          `json:"id"`
		Workspace string          `json:"workspace"`
		Directory string          `json:"directory"`
		Cwd       string          `json:"cwd"`
		CreatedAt json.RawMessage `json:"createdAt"`
		Created   json.RawMessage `json:"created"`
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		var wrapped struct {
			Sessions []entry `json:"sessions"`
		}
		if wrapErr := json.Unmarshal(data, &wrapped); wrapErr != nil || wrapped.Sessions == nil {
			return "", err
		}
		entries = wrapped.Sessions
	}
	var id string
	var latest time.Time
	for _, e := range entries {
		sid := firstNonEmpty(e.SessionID, e.ID)
		if !validSessionID(sid) {
			continue
		}
		ws := firstNonEmpty(e.Workspace, e.Directory, e.Cwd)
		if ws != "" && !sameDirectory(ws, dir) {
			continue
		}
		ts := firstTimeJSON(e.CreatedAt, e.Created)
		if sessionEligible(ts, since, latest) {
			id, latest = sid, ts
		}
	}
	return id, nil
}
