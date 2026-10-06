package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationPreservesP1History(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA user_version=1; INSERT INTO projects VALUES(1,'/repo','','main',1);
 INSERT INTO tasks(id,project_id,slug,title,branch,base_branch,worktree,tab_order,agent,created_at,updated_at) VALUES(1,1,'one','One','branch','main','/gone',0,'codex',1,1);
 INSERT INTO agent_sessions(id,task_id,agent,native_id,started_at) VALUES(1,1,'codex','native',1);
 INSERT INTO turns(session_id,seq,role,content,ts) VALUES(1,0,'terminal','saved screen',2);`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := s.History(ctx, Task{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Sessions) != 1 || h.Sessions[0].NativeComplete {
		t.Fatal("legacy coverage must default to unknown", h.Sessions)
	}
	if len(h.Turns) != 1 || h.Turns[0].Content != "saved screen" {
		t.Fatal(h)
	}
	if err = s.ImportTurns(ctx, 1, []Turn{{SourceKey: "record", Role: "user", Content: "native history", TS: time.UnixMilli(1)}}); err != nil {
		t.Fatal(err)
	}
	for _, complete := range []bool{true, false} {
		if err = s.SetNativeComplete(ctx, 1, complete); err != nil {
			t.Fatal(err)
		}
		h, err = s.History(ctx, Task{ID: 1})
		if err != nil || h.Sessions[0].NativeComplete != complete {
			t.Fatal(h.Sessions, err)
		}
	}
}

func TestTranscriptDeduplicationOwnershipAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject(ctx, Project{Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, Task{ProjectID: p.ID, Slug: "one", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	first, err := s.StartSession(ctx, task, "same-id", start)
	if err != nil {
		t.Fatal(err)
	}
	records := []Turn{{SourceKey: "one", Role: "assistant", Content: "partial", TS: start.Add(time.Second)}}
	if err = s.ImportTurns(ctx, first.ID, records); err != nil {
		t.Fatal(err)
	}
	second, err := s.StartSession(ctx, task, "same-id", start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	records[0].Content = "complete"
	records = append(records, Turn{SourceKey: "two", Role: "user", Content: "next", TS: start.Add(2 * time.Minute)})
	for range 2 {
		if err = s.ImportTurns(ctx, second.ID, records); err != nil {
			t.Fatal(err)
		}
	}
	task.Agent = "agy"
	third, err := s.StartSession(ctx, task, "same-id", start.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ImportTurns(ctx, third.ID, []Turn{{SourceKey: "one", Role: "assistant", Content: "different agent", TS: start.Add(4 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Turns) != 3 || h.Turns[0].Content != "complete" || h.Turns[0].SessionID != first.ID || h.Turns[1].SessionID != second.ID {
		t.Fatalf("bad ownership/dedup: %+v", h.Turns)
	}
	latest, err := s.LatestAgentSession(ctx, task.ID, "codex")
	if err != nil || latest.ID != second.ID {
		t.Fatal(latest, err)
	}
	task.Slug = "other"
	other, err := s.CreateTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := s.StartSession(ctx, other, "same-id", start)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ImportTurns(ctx, fourth.ID, records[:1]); err != nil {
		t.Fatal(err)
	}
	h, err = s.History(ctx, other)
	if err != nil || len(h.Turns) != 1 {
		t.Fatal(h, err)
	}
}

func TestPendingHandoffConsumedOnlyByTargetLaunch(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject(ctx, Project{Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, Task{ProjectID: p.ID, Slug: "one", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.StartSession(ctx, task, "codex", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareHandoff(ctx, task.ID, first.ID, "agy", "context"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingHandoff(ctx, task.ID)
	if err != nil || pending.Content != "context" {
		t.Fatal(pending, err)
	}
	task.Agent = "agy"
	second, err := s.StartSession(ctx, task, "agy", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if pending, err = s.PendingHandoff(ctx, task.ID); err != nil || pending.ID == 0 {
		t.Fatal("process creation consumed pending handoff", pending, err)
	}
	if err = s.ConfirmHandoff(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if pending, err = s.PendingHandoff(ctx, task.ID); err != nil || pending.ID == 0 {
		t.Fatal("wrong session consumed pending handoff", pending, err)
	}
	if err = s.ConfirmHandoff(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingHandoff(ctx, task.ID)
	if err != nil || pending.ID != 0 {
		t.Fatal(pending, err)
	}
	h, err := s.History(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Handoffs) != 1 || h.Handoffs[0].ToSession != second.ID || h.Sessions[1].HandoffFrom != first.ID || h.Events[len(h.Events)-1].Kind != "switched" {
		t.Fatal(h)
	}
}
