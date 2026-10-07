// Package handoff builds bounded, deterministic context without model calls.
package handoff

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mmoehabb/maestro/internal/store"
)

// Prompt returns the instruction for the agent to read the handoff file.
func Prompt(worktree string) string {
	return fmt.Sprintf("Read %s first. Continue this task using its goal, notes, conversation and Git state.", filepath.Join(worktree, ".maestro", "handoff.md"))
}

// Estimate conservatively budgets one token per UTF-8 byte. It is deterministic
// across agents, and deliberately leaves room for tokenizer differences.
func Estimate(s string) int { return len(s) }

func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	suffix := "\n[trimmed]\n"
	if n < len(suffix) {
		return strings.Repeat(".", n)
	}
	s = s[:n-len(suffix)]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + suffix
}

// clipRecent keeps the newest complete lines where possible, including UTF-8.
func clipRecent(s string, n int) string {
	if len(s) <= n {
		return s
	}
	marker := "[trimmed]\n"
	if n <= len(marker) {
		return clip(s, n)
	}
	start := len(s) - (n - len(marker))
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	tail := s[start:]
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
		tail = tail[i+1:]
	}
	return marker + tail
}

func Build(h store.History, gitState, target string, budget int) string {
	if budget <= 0 {
		return ""
	}
	var fixed strings.Builder
	fmt.Fprintf(&fixed, "# Task: %s\n\n## Goal\n%s\n\n## Notes\n%s\n", h.Task.Title, h.Task.Goal, h.Task.Notes)
	fixed.WriteString("\n## Explicit TODOs mentioned in notes/conversation\n")
	seenTODO := map[string]bool{}
	sources := []string{h.Task.Notes}
	for _, turn := range h.Turns {
		if turn.Role == "user" || turn.Role == "assistant" {
			sources = append(sources, turn.Content)
		}
	}
	todoCount := 0
	for _, source := range sources {
		for _, line := range strings.Split(source, "\n") {
			line = strings.TrimSpace(line)
			if (strings.Contains(line, "TODO:") || strings.HasPrefix(line, "- [ ]")) && !seenTODO[line] {
				seenTODO[line] = true
				fmt.Fprintln(&fixed, line)
				todoCount++
			}
		}
	}
	if todoCount == 0 {
		fixed.WriteString("None explicitly recorded.\n")
	}
	fixed.WriteString("\n## Agent timeline\n")
	var last int64
	for _, s := range h.Sessions {
		fmt.Fprintf(&fixed, "- %s: %s\n", s.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), s.Agent)
		if s.Agent == target {
			last = s.ID
		}
	}
	if last != 0 {
		fmt.Fprintf(&fixed, "\nReturning to %s; prioritize conversation after session %d.\n", target, last)
	}
	// Fixed context and Git state cannot consume the entire conversation budget.
	prefix := clip(fixed.String(), budget/3)
	gitSection := clip("\n## Git state\n"+gitState+"\n", budget/5)
	header := "\n## Conversation (oldest to newest)\n"
	omission := "[Earlier conversation omitted]\n"
	available := budget - len(prefix) - len(gitSection) - len(header) - len(omission)
	selected := []string{}
	omitted := false
	native := map[int64]bool{}
	for _, session := range h.Sessions {
		native[session.ID] = session.NativeComplete
	}
	// Preserve recent dialogue first. Tool results get a small per-record allowance.
	for i := len(h.Turns) - 1; i >= 0; i-- {
		t := h.Turns[i]
		if t.Role == "terminal" && native[t.SessionID] {
			continue
		}
		body := t.Content
		if strings.HasPrefix(t.Role, "tool_") {
			body = clip(body, min(300, budget/20))
		}
		s := fmt.Sprintf("\n[%s · session %d]\n%s\n", t.Role, t.SessionID, body)
		if len(s) > available {
			omitted = true
			if len(selected) == 0 && available > 64 {
				if t.Role == "terminal" {
					label := fmt.Sprintf("\n[terminal · session %d]\n", t.SessionID)
					selected = append(selected, label+clipRecent(body, available-len(label)-1)+"\n")
				} else {
					selected = append(selected, clip(s, available))
				}
			}
			break
		}
		selected = append(selected, s)
		available -= len(s)
	}
	var out strings.Builder
	out.WriteString(prefix)
	out.WriteString(gitSection)
	out.WriteString(header)
	if omitted {
		out.WriteString(omission)
	}
	for i := len(selected) - 1; i >= 0; i-- {
		out.WriteString(selected[i])
	}
	return clip(out.String(), budget)
}

// Write refuses symlink paths and installs a private file using an atomic rename.
func Write(worktree, content string) error {
	dir := filepath.Join(worktree, ".maestro")
	if st, err := os.Lstat(dir); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return errors.New(".maestro must be a real directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "handoff.md")
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return errors.New("handoff.md must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".handoff-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
