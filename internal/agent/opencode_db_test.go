package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/config"
)

func TestOpenCodeNativeFallback(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "xdg # data")
	t.Setenv("XDG_DATA_HOME", root)
	dir := t.TempDir()
	path := filepath.Join(root, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE session(id TEXT PRIMARY KEY,directory TEXT,parent_id TEXT,time_created INTEGER);
 CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,time_created INTEGER,data TEXT);
 CREATE TABLE part(id TEXT PRIMARY KEY,session_id TEXT,message_id TEXT,time_created INTEGER,data TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, row := range []struct {
		id, dir string
		parent  any
		ts      int64
	}{
		{"wanted", dir, nil, now.UnixMilli()},
		{"old", dir, nil, now.Add(-time.Hour).UnixMilli()},
		{"other", t.TempDir(), nil, now.Add(time.Second).UnixMilli()},
		{"child", dir, "wanted", now.Add(2 * time.Second).UnixMilli()},
	} {
		if _, err = db.Exec(`INSERT INTO session VALUES(?,?,?,?)`, row.id, row.dir, row.parent, row.ts); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile("testdata/opencode-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Messages []struct {
			Info  map[string]any
			Parts []map[string]any
		}
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, m := range fixture.Messages {
		id := m.Info["id"].(string)
		delete(m.Info, "id")
		raw, e := json.Marshal(m.Info)
		if e != nil {
			t.Fatal(e)
		}
		if _, err = db.Exec(`INSERT INTO message VALUES(?,?,?,?)`, id, "wanted", i, string(raw)); err != nil {
			t.Fatal(err)
		}
		for j, p := range m.Parts {
			pid := p["id"].(string)
			delete(p, "id")
			raw, e = json.Marshal(p)
			if e != nil {
				t.Fatal(e)
			}
			if _, err = db.Exec(`INSERT INTO part VALUES(?,?,?,?,?)`, pid, "wanted", id, j, string(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	id, err := discoverOpenCodeDB(ctx, dir, now.Add(-time.Second))
	if err != nil || id != "wanted" {
		t.Fatal(id, err)
	}
	// A failed CLI command uses the same fallback that empty JSON output uses.
	a := nativeAdapter{Generic: Generic{Name: "opencode", Config: config.Agent{Cmd: filepath.Join(t.TempDir(), "absent")}}}
	transcript, err := a.Read(ctx, "wanted", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Records) != 4 || len(transcript.Events) != 2 {
		t.Fatalf("unexpected normalized output: %+v", transcript)
	}
	if _, err = readOpenCodeDB(ctx, "wanted", t.TempDir()); err == nil {
		t.Fatal("wrong workspace accepted")
	}
	readonly, err := openCodeDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if _, err = readonly.Exec(`DELETE FROM session`); err == nil {
		t.Fatal("native database is writable")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM session`).Scan(&count); err != nil || count != 4 {
		t.Fatal("native database changed", count, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = readOpenCodeDB(cancelCtx, "wanted", dir); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestOpenCodeFallbackNeverCreatesDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	if db, err := openCodeDB(context.Background()); err == nil {
		_ = db.Close()
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "opencode", "opencode.db")); !os.IsNotExist(err) {
		t.Fatal("read-only fallback created database", err)
	}
}
