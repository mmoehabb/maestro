package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/term"
)

func TestCodexPromptScreens(t *testing.T) {
	b, err := os.ReadFile("testdata/codex-prompts-v0.162.1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Screen string
		Waiting      bool
	}
	if err = json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	g := Generic{Name: "codex"}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			for _, width := range []int{20, 40, 80, 160} {
				e := term.NewEmulator(width, 48, func() {}, nil)
				// Bytewise writes exercise ANSI sequences, UTF-8 and headings
				// split between PTY reads; vt reconstructs cursor-driven cells.
				output := "\x1b[?1049h\x1b[2J\x1b[H\x1b[1m" + strings.ReplaceAll(tc.Screen, "\n", "\r\n") + "\x1b[0m"
				for _, b := range []byte(output) {
					if _, err := e.Write([]byte{b}); err != nil {
						t.Fatal(err)
					}
				}
				if got := g.InputPrompt(e.PlainText()); got != tc.Waiting {
					t.Errorf("width=%d: waiting=%t, want %t; screen=%q", width, got, tc.Waiting, e.PlainText())
				}
				// Old dialogs retained in scrollback are not current prompts.
				_, _ = e.Write([]byte("\x1b[?1049l"))
				if g.InputPrompt(e.PlainText()) {
					t.Errorf("width=%d: approval survived alternate-screen teardown", width)
				}
				_ = e.Close()
			}
		})
	}
}

func TestPromptOverridesAndNotifications(t *testing.T) {
	const screen = "Would you like to grant these permissions?\nPress enter to confirm or esc to cancel"
	for _, cfg := range []config.Agent{{InputHints: []string{}}, {InputHints: []string{"custom approval"}}} {
		g := Generic{Name: "codex", Config: cfg}
		if g.InputPrompt(screen) || g.InputNotification("Approval requested: test") {
			t.Fatal("explicit input_hints did not replace built-in detection")
		}
		if len(g.Hints()) != len(cfg.InputHints) {
			t.Fatal("custom hints were lost")
		}
	}
	if (Generic{Name: "custom"}).InputPrompt(screen) || len((Generic{Name: "custom"}).Hints()) == 0 {
		t.Fatal("custom agent did not retain generic fallback")
	}
	g := Generic{Name: "codex"}
	for _, text := range []string{
		"Approval requested: go test ./...", "Approval requested by fixture-server",
		"Codex wants to edit main.go", "Plan mode prompt: Select an option",
		"Question: Which option?", "Codex\nApproval requested: network access",
	} {
		if !g.InputNotification(text) {
			t.Errorf("unrecognized notification %q", text)
		}
	}
	for _, text := range []string{"Agent turn complete", "Completed approval requested documentation", ""} {
		if g.InputNotification(text) {
			t.Errorf("completion treated as a prompt: %q", text)
		}
	}
}

func TestCodexInlineDialogMessages(t *testing.T) {
	g := Generic{Name: "codex"}
	for _, heading := range []string{"Question 1/1", "fixture-server needs your approval."} {
		for _, body := range []string{"```code```", "```" + strings.Repeat("some message ", 5) + "```", "```unclosed description"} {
			for _, width := range []int{20, 40, 80} {
				t.Run(fmt.Sprintf("heading=%s/body=%s/width=%d", heading, body, width), func(t *testing.T) {
					e := term.NewEmulator(width, 48, func() {}, nil)
					defer e.Close()
					screen := heading + "\n" + body + "\n› 1. Yes\nctrl+s to submit all\nPress enter to confirm or esc to cancel"
					if _, err := e.Write([]byte(strings.ReplaceAll(screen, "\n", "\r\n"))); err != nil {
						t.Fatal(err)
					}
					if !g.InputPrompt(e.PlainText()) {
						t.Fatalf("inline message hid dialog controls: %q", e.PlainText())
					}
					if g.InputPrompt("```text\n" + screen + "\n```") {
						t.Fatal("fenced example treated as a dialog")
					}
				})
			}
		}
	}
}

func TestCodexNotificationShortcuts(t *testing.T) {
	g := Generic{Name: "codex"}
	for _, tc := range []struct{ message, keys string }{
		{"Approval requested: command", "yapdn"},
		{"Approval requested by server", "ync"},
		{"Codex wants to edit main.go", "yan"},
	} {
		message := tc.message
		for _, code := range tc.keys {
			key := string(code)
			if !g.InputSubmission(message, key) {
				t.Errorf("approval shortcut missed: %q / %q", message, key)
			}
			if g.InputSubmission("Question: What name?", key) || g.InputSubmission("Plan mode prompt: What plan?", key) ||
				g.InputSubmission("Question: What name?\nApproval requested: quoted example", key) ||
				(Generic{Name: "codex", Config: config.Agent{InputHints: []string{}}}).InputSubmission(message, key) {
				t.Errorf("editing or override treated as a decision: %q", key)
			}
		}
		for _, key := range []string{"z", "Y", "ay", "", "1", "r"} {
			if g.InputSubmission(message, key) {
				t.Errorf("non-decision treated as submission: %q / %q", message, key)
			}
		}
	}
}

func TestCodexPromptExampleBoundaries(t *testing.T) {
	g := Generic{Name: "codex"}
	for _, example := range []string{
		"> fixture-server needs your approval.\n> Press enter to confirm or esc to cancel",
		"```text\nfixture-server needs your approval.\nPress enter to confirm or esc to cancel\n```",
		"Would you like to run\n> the following command?\n> Press enter to confirm or esc to cancel",
		"Would you like to run the following command?\n```text\nPress enter to confirm or esc to cancel\n```",
	} {
		t.Run(example, func(t *testing.T) {
			if g.InputPrompt("The dialog contains:\n" + example) {
				t.Fatal("quoted or fenced example recognized as an active prompt")
			}
			if !g.InputPrompt("The dialog contains:\n" + example + "\n\nfixture-server needs your approval.\nPress enter to confirm or esc to cancel") {
				t.Fatal("example suppressed a subsequent real prompt")
			}
		})
	}
}

func TestCodexDialogAfterHistoryAndLiteralCommand(t *testing.T) {
	g := Generic{Name: "codex"}
	for _, history := range []string{"", "› Run the tests\n\n", "› Question 1/1\n\n"} {
		for _, command := range []string{"$ go test ./...", "$ cat > README.md <<'EOF'\n```markdown\nEOF"} {
			for _, width := range []int{20, 40, 80} {
				t.Run(fmt.Sprintf("history=%q/command=%q/width=%d", history, command, width), func(t *testing.T) {
					e := term.NewEmulator(width, 48, func() {}, nil)
					defer e.Close()
					screen := history + "Would you like to run the following command?\n\n" + command +
						"\n\n› 1. Yes, proceed\n2. No\n\nPress enter to confirm or esc to cancel"
					if _, err := e.Write([]byte(strings.ReplaceAll(screen, "\n", "\r\n"))); err != nil {
						t.Fatal(err)
					}
					if !g.InputPrompt(e.PlainText()) {
						t.Fatalf("live approval was hidden: %q", e.PlainText())
					}
				})
			}
		}
	}
}

func TestCodexDialogBodyPreservesControls(t *testing.T) {
	g := Generic{Name: "codex"}
	for _, body := range []string{
		"# New name", "> New name", "`New name`", "```New name",
		"```text\n# command output\n```",
	} {
		for _, selected := range []bool{false, true} {
			if !selected && body == "```New name" {
				continue // An unclosed fence excludes subsequent controls.
			}
			for _, heading := range []string{
				"Question 1/1\nWhat should the feature be called?",
				"Would you like to run the following command?",
			} {
				text := body
				if selected {
					text = "› " + text
					if strings.Contains(body, "\n") {
						continue
					}
				}
				screen := heading + "\n" + text + "\nctrl+s to submit all\nPress enter to confirm or esc to cancel"
				t.Run(screen, func(t *testing.T) {
					if !g.InputPrompt(screen) {
						t.Fatal("dialog body hid submission controls")
					}
					now := time.Now()
					a := term.Activity{State: term.Working, IdleAfter: time.Second, Prompts: term.PromptDetector{Screen: g.InputPrompt}}
					a.Screen(heading+"\nctrl+s to submit all\nPress enter to confirm or esc to cancel", now)
					revision := a.Revision
					a.Screen(screen, now)
					a.Tick(now.Add(time.Hour))
					if a.State != term.NeedsInput || a.Revision != revision {
						t.Fatal("editing dismissed an unanswered dialog", a.State)
					}
				})
			}
		}
	}
}

func TestCodexWrappedAnswerPreservesPrompt(t *testing.T) {
	g := Generic{Name: "codex"}
	for _, width := range []int{20, 40, 80} {
		for _, syntax := range []string{"```", ">", "#", "`"} {
			for _, prefix := range []string{"", "enterprise", "ctrl+snotes", "pressenterprise", strings.Repeat(" ", width), strings.Repeat(" ", 2*width)} {
				t.Run(fmt.Sprintf("width=%d/syntax=%s/prefix=%q", width, syntax, prefix), func(t *testing.T) {
					e := term.NewEmulator(width, 24, func() {}, nil)
					defer e.Close()
					// The marker and space occupy two cells. Fill the remainder
					// so Markdown syntax begins on an unmarked continuation row.
					answer := strings.Repeat("x", width-2) + syntax + "New name"
					if prefix != "" {
						answer = strings.Repeat("x", width-2) + prefix + strings.Repeat("x", max(0, width-len(prefix))) + syntax + "New name"
					}
					write := func(answer string) {
						t.Helper()
						_, err := e.Write([]byte("\x1b[2J\x1b[HQuestion 1/1\r\nWhat name?\r\n› " + answer + "\r\nctrl+s to submit all"))
						if err != nil {
							t.Fatal(err)
						}
					}
					now := time.Now()
					a := term.Activity{State: term.Working, IdleAfter: time.Second, Prompts: term.PromptDetector{Screen: g.InputPrompt}}
					write("New name")
					a.Screen(e.PlainText(), now)
					revision := a.Revision
					write(answer)
					if !strings.Contains(e.PlainText(), "\n"+syntax) {
						t.Fatalf("answer did not wrap as intended: %q", e.PlainText())
					}
					a.Screen(e.PlainText(), now)
					a.Tick(now.Add(time.Hour))
					if a.State != term.NeedsInput || a.Revision != revision {
						t.Fatalf("wrapped answer dismissed unanswered question: %q", e.PlainText())
					}
				})
			}
		}
	}
}

func FuzzCodexQuestionAnswer(f *testing.F) {
	for _, answer := range []string{
		"New name", "# > ```answer", strings.Repeat("x", 18) + strings.Repeat(" ", 20) + "```",
		strings.Repeat("x", 18) + "enterprise" + strings.Repeat("x", 10) + "```", "日本語🙂",
	} {
		f.Add(answer, byte(0))
	}
	f.Fuzz(func(t *testing.T, answer string, size byte) {
		// Answers are printable terminal text. Control sequences could erase
		// the heading itself and are outside the editing invariant.
		if !utf8.ValidString(answer) || len([]rune(answer)) > 200 {
			return
		}
		for _, r := range answer {
			if !unicode.IsPrint(r) {
				return
			}
		}
		e := term.NewEmulator(20+int(size)%61, 48, func() {}, nil)
		defer e.Close()
		g := Generic{Name: "codex"}
		now := time.Now()
		a := term.Activity{State: term.Working, IdleAfter: time.Second, Prompts: term.PromptDetector{Screen: g.InputPrompt}}
		a.Screen("Question 1/1\n› New name\nctrl+s to submit all", now)
		revision := a.Revision
		if _, err := e.Write([]byte("Question 1/1\r\nWhat name?\r\n› " + answer + "\r\nctrl+s to submit all")); err != nil {
			t.Fatal(err)
		}
		a.Screen(e.PlainText(), now)
		a.Tick(now.Add(time.Hour))
		if a.State != term.NeedsInput || a.Revision != revision {
			t.Fatalf("editing dismissed question: answer=%q screen=%q", answer, e.PlainText())
		}
	})
}
