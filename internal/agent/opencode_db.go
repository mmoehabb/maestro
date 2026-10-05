package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Native OpenCode fallback is strictly read-only.
)

// OpenCode uses XDG_DATA_HOME or ~/.local/share on all supported platforms.
// Do not use Maestro's store.Open here: it migrates databases and enables WAL.
func openCodeDB(ctx context.Context) (*sql.DB, error) {
	root := os.Getenv("XDG_DATA_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".local", "share")
	}
	path, err := filepath.Abs(filepath.Join(root, "opencode", "opencode.db"))
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
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

func discoverOpenCodeDB(ctx context.Context, dir string, since time.Time) (string, error) {
	db, err := openCodeDB(ctx)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,directory FROM session WHERE parent_id IS NULL AND time_created>=? ORDER BY time_created DESC,id DESC`, since.Add(-time.Second).UnixMilli())
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, path string
		if err = rows.Scan(&id, &path); err != nil {
			return "", err
		}
		if sameDirectory(path, dir) {
			return id, nil
		}
	}
	return "", rows.Err()
}

func readOpenCodeDB(ctx context.Context, id, dir string) (Transcript, error) {
	db, err := openCodeDB(ctx)
	if err != nil {
		return Transcript{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Transcript{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	var path string
	if err = tx.QueryRowContext(ctx, `SELECT directory FROM session WHERE id=?`, id).Scan(&path); err != nil {
		return Transcript{}, err
	}
	if dir != "" && !sameDirectory(path, dir) {
		return Transcript{}, errors.New("OpenCode native session belongs to a different directory")
	}
	type message struct {
		Info  map[string]any   `json:"info"`
		Parts []map[string]any `json:"parts"`
	}
	messages := []message{}
	indexes := map[string]int{}
	total := 0
	rows, err := tx.QueryContext(ctx, `SELECT id,data FROM message WHERE session_id=? ORDER BY time_created,id`, id)
	if err != nil {
		return Transcript{}, err
	}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			break
		}
		total += len(raw)
		if total > 64<<20 {
			err = errors.New("native transcript exceeds 64 MiB")
			break
		}
		var info map[string]any
		if err = json.Unmarshal([]byte(raw), &info); err != nil {
			break
		}
		if info == nil {
			err = ErrUnsupported
			break
		}
		info["id"] = key
		indexes[key] = len(messages)
		messages = append(messages, message{Info: info, Parts: []map[string]any{}})
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return Transcript{}, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,message_id,data FROM part WHERE session_id=? ORDER BY time_created,id`, id)
	if err != nil {
		return Transcript{}, err
	}
	for rows.Next() {
		var key, parent, raw string
		if err = rows.Scan(&key, &parent, &raw); err != nil {
			break
		}
		total += len(raw)
		if total > 64<<20 {
			err = errors.New("native transcript exceeds 64 MiB")
			break
		}
		var part map[string]any
		if err = json.Unmarshal([]byte(raw), &part); err != nil {
			break
		}
		if part == nil {
			err = ErrUnsupported
			break
		}
		part["id"] = key
		if i, ok := indexes[parent]; ok {
			messages[i].Parts = append(messages[i].Parts, part)
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return Transcript{}, err
	}
	if err = tx.Commit(); err != nil {
		return Transcript{}, err
	}
	b, err := json.Marshal(struct {
		Info     map[string]string `json:"info"`
		Messages []message         `json:"messages"`
	}{map[string]string{"id": id}, messages})
	if err != nil {
		return Transcript{}, err
	}
	transcript, err := parseOpenCode(b)
	if err != nil {
		return transcript, fmt.Errorf("OpenCode native schema: %w", err)
	}
	return transcript, nil
}
