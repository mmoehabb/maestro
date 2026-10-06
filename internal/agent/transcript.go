package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a nativeAdapter) Read(ctx context.Context, id, dir string) (Transcript, error) {
	if id == "" {
		return Transcript{}, ErrUnsupported
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Transcript{}, err
	}
	switch a.Name {
	case "codex":
		root := os.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		var found string
		err = filepath.WalkDir(filepath.Join(root, "sessions"), func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			// IDs can appear in both old and new rollout filename forms. Always verify metadata.
			if !strings.Contains(d.Name(), id) {
				return nil
			}
			f, e := os.Open(p)
			if e != nil {
				return e
			}
			defer f.Close()
			r := bufio.NewReader(f)
			line, e := r.ReadBytes('\n')
			if e != nil && !errors.Is(e, io.EOF) {
				return e
			}
			var meta struct {
				Type    string
				Payload struct{ ID, Cwd string }
			}
			if json.Unmarshal(line, &meta) == nil && meta.Type == "session_meta" && meta.Payload.ID == id && sameDirectory(meta.Payload.Cwd, dir) {
				found = p
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			return Transcript{}, err
		}
		if found == "" {
			return Transcript{}, os.ErrNotExist
		}
		return readJSONL(ctx, found, "codex")
	case "agy":
		if filepath.Base(id) != id || strings.ContainsAny(id, "/\\") {
			return Transcript{}, errors.New("invalid native session ID")
		}
		return readJSONL(ctx, filepath.Join(home, ".gemini", "antigravity-cli", "brain", id, ".system_generated", "logs", "transcript.jsonl"), "agy")
	case "opencode":
		cmd := exec.CommandContext(ctx, a.Config.Cmd, "export", id)
		cmd.Dir = dir
		cmd.WaitDelay = time.Second
		var out limitedBuffer
		cmd.Stdout = &out
		commandErr := cmd.Run()
		var transcript Transcript
		if commandErr == nil {
			transcript, commandErr = parseOpenCode(out.Bytes())
		}
		if commandErr == nil {
			return transcript, nil
		}
		fallback, fallbackErr := readOpenCodeDB(ctx, id, dir)
		if fallbackErr == nil {
			return fallback, nil
		}
		return transcript, fmt.Errorf("opencode export/native history: %w", errors.Join(commandErr, fallbackErr))
	default:
		return Transcript{}, ErrUnsupported
	}
}

// Prevent a corrupt/exporting agent from exhausting Maestro's memory.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<20 {
		return 0, errors.New("transcript export exceeds 64 MiB")
	}
	return b.Buffer.Write(p)
}

func readJSONL(ctx context.Context, path, kind string) (Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()
	t, err := parseJSONL(ctx, f, kind)
	t.Path = path
	return t, err
}

func parseJSONL(ctx context.Context, r io.Reader, kind string) (Transcript, error) {
	t := Transcript{}
	s := bufio.NewReaderSize(r, 64*1024)
	lineNo := 0
	recognized := false
	for {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		var line []byte
		for {
			fragment, e := s.ReadSlice('\n')
			line = append(line, fragment...)
			if len(line) > 8<<20 {
				return t, errors.New("transcript record exceeds 8 MiB")
			}
			if errors.Is(e, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(e, io.EOF) { // An incomplete final record is retried after the next write.
				if len(line) == 0 || !json.Valid(line) {
					t.Incomplete = len(bytes.TrimSpace(line)) != 0
					if !recognized && lineNo > 0 {
						return t, ErrUnsupported
					}
					return t, nil
				}
			} else if e != nil {
				return t, e
			}
			break
		}
		lineNo++
		var r map[string]json.RawMessage
		if err := json.Unmarshal(line, &r); err != nil {
			return t, fmt.Errorf("transcript line %d: %w", lineNo, err)
		}
		key := strconv.Itoa(lineNo)
		if kind == "codex" {
			key = fmt.Sprintf("%x", sha256.Sum256(bytes.TrimSpace(line)))
			typ := str(r["type"])
			var p map[string]json.RawMessage
			_ = json.Unmarshal(r["payload"], &p)
			ts := stamp(r["timestamp"])
			switch typ {
			case "session_meta":
				recognized = true
			case "response_item":
				recognized = true
				role := str(p["role"])
				ty := str(p["type"])
				switch ty {
				case "message":
					if role == "user" || role == "assistant" {
						content := messageText(p["content"])
						if content != "" {
							t.Records = append(t.Records, Record{Key: key, Role: role, Content: content, TS: ts})
						}
					}
				case "function_call", "custom_tool_call":
					t.Records = append(t.Records, Record{Key: key, Role: "tool_call", Content: str(p["name"]) + "\n" + rawText(first(p["arguments"], p["input"])), ToolCallID: str(p["call_id"]), TS: ts})
				case "function_call_output", "custom_tool_call_output":
					t.Records = append(t.Records, Record{Key: key, Role: "tool_result", Content: rawText(p["output"]), ToolCallID: str(p["call_id"]), TS: ts})
				}
			case "event_msg":
				event := ""
				switch str(p["type"]) {
				case "task_started":
					event = "started"
				case "task_complete":
					event = "done"
				case "turn_aborted":
					event = "interrupted"
				}
				if event != "" {
					recognized = true
					t.Events = append(t.Events, TurnEvent{Key: key, Kind: event, TS: ts})
				}
			}
		} else {
			typ := str(r["type"])
			ts := stamp(r["created_at"])
			idx := rawText(r["step_index"])
			if idx != "" {
				key = idx
			}
			content := rawText(r["content"])
			switch typ {
			case "USER_INPUT":
				recognized = true
				t.Records = append(t.Records, Record{Key: key, Role: "user", Content: content, TS: ts})
				t.Events = append(t.Events, TurnEvent{Key: key + ":start", Kind: "started", TS: ts})
			case "PLANNER_RESPONSE":
				recognized = true
				if content != "" {
					t.Records = append(t.Records, Record{Key: key, Role: "assistant", Content: content, TS: ts})
				}
				var calls []struct {
					Name string
					Args json.RawMessage
				}
				_ = json.Unmarshal(r["tool_calls"], &calls)
				for i, c := range calls {
					k := fmt.Sprintf("%s:tool:%d", key, i)
					t.Records = append(t.Records, Record{Key: k, Role: "tool_call", Content: c.Name + "\n" + rawText(c.Args), ToolCallID: k, TS: ts})
				}
				// agy marks every tool/planner step DONE. Only a textual final planner
				// response without tool calls is evidence of turn completion.
				if content != "" && len(calls) == 0 && str(r["status"]) == "DONE" {
					t.Events = append(t.Events, TurnEvent{Key: key + ":done", Kind: "done", TS: ts})
				}
			case "GENERIC":
				recognized = true
				if content != "" {
					t.Records = append(t.Records, Record{Key: key, Role: "tool_result", Content: content, TS: ts})
				}
			case "CHECKPOINT", "SYSTEM_MESSAGE", "ERROR_MESSAGE":
				recognized = true
			}
		}
	}
}

func first(a, b json.RawMessage) json.RawMessage {
	if len(a) > 0 {
		return a
	}
	return b
}
func str(b json.RawMessage) string { var s string; _ = json.Unmarshal(b, &s); return s }
func rawText(b json.RawMessage) string {
	if len(b) == 0 || string(b) == "null" {
		return ""
	}
	if b[0] == '"' {
		return str(b)
	}
	return string(b)
}

func stamp(b json.RawMessage) time.Time {
	if s := str(b); s != "" {
		t, _ := time.Parse(time.RFC3339Nano, s)
		return t
	}
	var n int64
	if json.Unmarshal(b, &n) == nil && n > 0 {
		return time.UnixMilli(n)
	}
	return time.Time{}
}

func messageText(b json.RawMessage) string {
	var parts []struct{ Type, Text string }
	if json.Unmarshal(b, &parts) != nil {
		return str(b)
	}
	var text []string
	for _, p := range parts {
		if p.Type == "input_text" || p.Type == "output_text" || p.Type == "text" {
			text = append(text, p.Text)
		}
	}
	return strings.Join(text, "\n")
}

func parseOpenCode(b []byte) (Transcript, error) {
	var data struct {
		Info     json.RawMessage
		Messages []struct {
			Info struct {
				ID, Role, Finish string
				Time             struct{ Created, Completed int64 }
				Error            json.RawMessage
			}
			Parts []struct {
				ID, Type, Text, Tool, CallID string
				State                        struct {
					Status        string
					Input         json.RawMessage
					Output, Error string
				}
			}
		}
	}
	t := Transcript{}
	if err := json.Unmarshal(b, &data); err != nil {
		return t, err
	}
	if len(data.Info) == 0 || data.Messages == nil {
		return t, ErrUnsupported
	}
	for _, m := range data.Messages {
		if m.Info.ID == "" {
			return t, ErrUnsupported
		}
		ts := time.UnixMilli(m.Info.Time.Created)
		if m.Info.Role == "user" {
			t.Events = append(t.Events, TurnEvent{Key: m.Info.ID + ":start", Kind: "started", TS: ts})
		}
		for _, p := range m.Parts {
			key := m.Info.ID + ":" + p.ID
			switch p.Type {
			case "text":
				if m.Info.Role == "user" || m.Info.Role == "assistant" {
					t.Records = append(t.Records, Record{Key: key, Role: m.Info.Role, Content: p.Text, TS: ts})
				}
			case "tool":
				t.Records = append(t.Records, Record{Key: key + ":call", Role: "tool_call", Content: p.Tool + "\n" + rawText(p.State.Input), ToolCallID: p.CallID, TS: ts})
				if p.State.Status == "completed" || p.State.Status == "error" {
					t.Records = append(t.Records, Record{Key: key + ":result", Role: "tool_result", Content: p.State.Output + p.State.Error, ToolCallID: p.CallID, TS: ts})
				}
			}
		}
		if m.Info.Role == "assistant" && m.Info.Time.Completed > 0 && m.Info.Finish != "" && m.Info.Finish != "tool-calls" && m.Info.Finish != "unknown" {
			t.Events = append(t.Events, TurnEvent{Key: m.Info.ID + ":done", Kind: "done", TS: time.UnixMilli(m.Info.Time.Completed)})
		}
	}
	return t, nil
}
