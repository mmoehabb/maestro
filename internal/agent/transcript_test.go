package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/config"
)

func TestTranscriptFixtures(t *testing.T) {
	for _, tc := range []struct {
		name            string
		records, events int
	}{{"codex", 4, 4}, {"agy", 5, 2}, {"opencode", 4, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			ext := ".jsonl"
			if tc.name == "opencode" {
				ext = ".json"
			}
			b, err := os.ReadFile("testdata/" + tc.name + "-v1" + ext)
			if err != nil {
				t.Fatal(err)
			}
			var out Transcript
			if tc.name == "opencode" {
				out, err = parseOpenCode(b)
			} else {
				out, err = parseJSONL(context.Background(), strings.NewReader(string(b)), tc.name)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Records) != tc.records || len(out.Events) != tc.events {
				t.Fatalf("records=%d events=%d: %+v", len(out.Records), len(out.Events), out)
			}
			for _, r := range out.Records {
				if strings.Contains(r.Content, "Do not import me") {
					t.Fatal("private instructions/reasoning imported")
				}
				if r.Key == "" || r.TS.IsZero() {
					t.Fatal("missing identity/timestamp", r)
				}
			}
		})
	}
}

func TestPartialMalformedAndUnknownRecords(t *testing.T) {
	prefix := `{"type":"session_meta","payload":{}}` + "\n"
	out, err := parseJSONL(context.Background(), strings.NewReader(prefix+`{"type":`), "codex")
	if err != nil || len(out.Records) != 0 {
		t.Fatal(out, err)
	}
	if _, err = parseJSONL(context.Background(), strings.NewReader(prefix+"invalid\n"), "codex"); err == nil {
		t.Fatal("malformed complete line accepted")
	}
	if _, err = parseJSONL(context.Background(), strings.NewReader("{\"new_schema\":true}\n"), "codex"); err == nil {
		t.Fatal("unknown format accepted")
	}
	if _, err = parseOpenCode([]byte(`{"unrecognized":true}`)); err == nil {
		t.Fatal("unknown export accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = parseJSONL(ctx, strings.NewReader(prefix), "codex"); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestCodexSourceKeysSurvivePrefixChanges(t *testing.T) {
	line := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n"
	a, _ := parseJSONL(context.Background(), strings.NewReader(line), "codex")
	b, _ := parseJSONL(context.Background(), strings.NewReader("{\"type\":\"session_meta\"}\n"+line), "codex")
	if a.Records[0].Key != b.Records[0].Key {
		t.Fatal("source keys depend on file offset")
	}
}

func TestWatcherCancellationAndReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "test", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"USER_INPUT","created_at":"2026-10-05T12:00:00Z","step_index":0,"content":"one"}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := Watch(ctx, nativeAdapter{Generic: Generic{Name: "agy"}}, "test", home)
	select {
	case u := <-ch:
		if u.Err != nil || len(u.Transcript.Records) != 1 {
			t.Fatal(u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch timeout")
	}
	replacement := strings.ReplaceAll(line, "one", "two")
	if err := os.WriteFile(p+".tmp", []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case u := <-ch:
			if len(u.Transcript.Records) > 0 && u.Transcript.Records[0].Content == "two" {
				cancel()
				for range ch {
				}
				return
			}
		case <-deadline:
			t.Fatal("replacement not observed")
		}
	}
}

// Opt-in read-only compatibility check. Logs counts, never conversation content.
func TestLocalTranscriptCompatibility(t *testing.T) {
	if os.Getenv("MAESTRO_TRANSCRIPT_SMOKE") != "1" {
		t.Skip("set MAESTRO_TRANSCRIPT_SMOKE=1 to inspect local native record compatibility")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, pattern string }{
		{"codex", filepath.Join(home, ".codex", "sessions", "*", "*", "*", "*.jsonl")},
		{"agy", filepath.Join(home, ".gemini", "antigravity-cli", "brain", "*", ".system_generated", "logs", "transcript.jsonl")},
	} {
		paths, err := filepath.Glob(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 0 {
			t.Log(tc.kind, "no local transcripts")
			continue
		}
		var newest string
		var modified time.Time
		for _, path := range paths {
			st, e := os.Stat(path)
			if e == nil && st.ModTime().After(modified) {
				newest = path
				modified = st.ModTime()
			}
		}
		transcript, err := readJSONL(context.Background(), newest, tc.kind)
		if err != nil {
			t.Fatalf("%s compatibility: %v", tc.kind, err)
		}
		t.Logf("%s: %d normalized records, %d turn events", tc.kind, len(transcript.Records), len(transcript.Events))
	}
}

func TestLocalOpenCodeExportCompatibility(t *testing.T) {
	if os.Getenv("MAESTRO_OPENCODE_SMOKE") != "1" {
		t.Skip("set MAESTRO_OPENCODE_SMOKE=1 to inspect local OpenCode compatibility")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openCodeDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id, dir string
	if err = db.QueryRowContext(ctx, `SELECT id,directory FROM session WHERE parent_id IS NULL ORDER BY time_created DESC LIMIT 1`).Scan(&id, &dir); err != nil {
		t.Fatal(err)
	}
	a := nativeAdapter{Generic: Generic{Name: "opencode", Config: config.Agent{Cmd: "opencode"}}}
	result, err := a.Read(ctx, id, dir)
	if err != nil {
		t.Fatal("export/native compatibility:", err)
	}
	found, err := a.DiscoverSession(ctx, dir, time.Time{})
	if err != nil || found == "" {
		t.Fatal("discovery failed", err)
	}
	t.Logf("opencode: %d normalized records, %d turn events", len(result.Records), len(result.Events))
}

func TestPartialTranscriptCoverage(t *testing.T) {
	prefix := "{\"type\":\"USER_INPUT\",\"created_at\":\"2026-10-05T12:00:00Z\",\"content\":\"hello\"}\n"
	snapshot, err := parseJSONL(context.Background(), strings.NewReader(prefix+"{\"type\":"), "agy")
	if err != nil || !snapshot.Incomplete || len(snapshot.Records) != 1 {
		t.Fatal(snapshot, err)
	}
	snapshot, err = parseJSONL(context.Background(), strings.NewReader(prefix), "agy")
	if err != nil || snapshot.Incomplete {
		t.Fatal(snapshot, err)
	}
}
