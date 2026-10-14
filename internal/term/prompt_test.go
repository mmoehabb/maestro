package term

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mmoehabb/maestro/internal/agent"
)

const approvalScreen = "Would you like to run the following command?\r\n" +
	"$ go test ./...\r\n› 1. Yes, proceed\r\n  2. No, and tell Codex what to do differently\r\n" +
	"Press enter to confirm or esc to cancel"

func codexActivity() Activity {
	g := agent.Generic{Name: "codex"}
	return Activity{
		State: Starting, IdleAfter: time.Second,
		Prompts: PromptDetector{Screen: g.InputPrompt, Notification: g.InputNotification, Submission: g.InputSubmission},
	}
}

func TestDialogInteractionAndRedrawKeepOneRevision(t *testing.T) {
	now := time.Now()
	a := codexActivity()
	a.NativeEvent("started")
	e := NewEmulator(40, 24, a.Complete, a.Notification)
	defer e.Close()
	write := func(output string, at time.Time) {
		t.Helper()
		a.Output([]byte(output), at)
		if _, err := e.Write([]byte(output)); err != nil {
			t.Fatal(err)
		}
		a.Screen(e.PlainText(), at)
	}
	write("\x1b[2J\x1b[H"+approvalScreen, now)
	if a.State != NeedsInput || !a.NativeWorking {
		t.Fatalf("native approval not recognized: %+v", a)
	}
	revision := a.Revision
	for _, interact := range []func(time.Time){a.Interact, a.Input, a.Interact} {
		interact(now)
		a.Tick(now.Add(time.Hour))
		if a.State != NeedsInput || a.Revision != revision {
			t.Fatalf("interaction cleared or repeated approval: %+v", a)
		}
	}
	// A split clear/repaint must not start a second attention transition.
	write("\x1b[2J\x1b[H", now)
	a.Tick(now.Add(promptSettleDelay / 2))
	write(approvalScreen, now.Add(promptSettleDelay/2))
	a.Tick(now.Add(time.Hour))
	if a.State != NeedsInput || a.Revision != revision {
		t.Fatalf("redraw duplicated approval: %+v", a)
	}
	// Native recovery/start delivery must preserve the pending dialog.
	a.NativeEvent("restored_started")
	a.NativeEvent("started")
	if a.State != NeedsInput || a.Revision != revision {
		t.Fatal("delayed start lost prompt", a)
	}
	// Approval or rejection closes the dialog while the turn is still live.
	write("\x1b[2J\x1b[HRunning command", now)
	a.Tick(now.Add(promptSettleDelay))
	if a.State != Working || a.Revision != revision+1 || !a.NativeWorking {
		t.Fatalf("dismissed dialog did not resume native work: %+v", a)
	}
	a.Tick(now.Add(time.Hour))
	if a.State != Working {
		t.Fatal("silence completed a native tool")
	}
	a.NativeEvent("done")
	if a.State != Done {
		t.Fatal(a)
	}
}

func TestApprovalBeyondOutputTailAndErasedCells(t *testing.T) {
	now := time.Now()
	a := codexActivity()
	a.NativeEvent("started")
	e := NewEmulator(100, 80, a.Complete, a.Notification)
	defer e.Close()
	output := "\x1b[2J\x1b[H" + approvalScreen + strings.Repeat("\r\n"+strings.Repeat("x", 90), 50)
	a.Output([]byte(output), now)
	_, _ = e.Write([]byte(output))
	a.Screen(e.PlainText(), now)
	if a.State != NeedsInput {
		t.Fatal("prompt before the 4096-byte tail cutoff was lost", a.State)
	}
	// Completion is authoritative even if the previous screen is repainted.
	a.NativeEvent("done")
	a.Screen(e.PlainText(), now)
	if a.State != Done {
		t.Fatal("old screen resurrected completed approval")
	}
	_, _ = e.Write([]byte("\x1b[2J\x1b[H"))
	a.Screen(e.PlainText(), now)
	a.NativeEvent("started")
	_, _ = e.Write([]byte("\x1b[1m" + approvalScreen + "\x1b[0m"))
	a.Screen(e.PlainText(), now)
	if a.State != NeedsInput {
		t.Fatal("new turn could not ask the same approval again")
	}
	// Cursor erasure removes the controls without relying on raw output text.
	_, _ = e.Write([]byte("\x1b[5;1H\x1b[0J"))
	a.Screen(e.PlainText(), now)
	a.Tick(now.Add(promptSettleDelay))
	if a.State != Working {
		t.Fatal("erased controls retained a pending dialog", a.State)
	}
}

func TestPromptNotificationsAndNativePrecedence(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, sequence := range []string{
			"\x1b]9;Approval requested: go test ./...\a",
			"\x1b]9;Question: Which option?\x1b\\",
			"\x1b]777;notify;Codex;Approval requested: network access\a",
		} {
			t.Run(fmt.Sprintf("native=%t/%q", native, sequence), func(t *testing.T) {
				now := time.Now()
				a := codexActivity()
				a.Output([]byte("working"), now)
				if native {
					a.NativeEvent("started")
				}
				e := NewEmulator(80, 24, a.Complete, a.Notification)
				defer e.Close()
				for _, b := range []byte(sequence) {
					_, _ = e.Write([]byte{b})
				}
				a.Screen(e.PlainText(), now)
				a.Tick(now.Add(time.Hour))
				if a.State != NeedsInput {
					t.Fatal("notification became completion or was lost", a.State)
				}
				revision := a.Revision
				_, _ = e.Write([]byte(sequence))
				a.Interact(now)
				if a.State != NeedsInput || a.Revision != revision {
					t.Fatal("repeated notification/navigation changed revision", a)
				}
				a.Output([]byte("resumed"), now)
				_, _ = e.Write([]byte("resumed"))
				a.Screen(e.PlainText(), now)
				a.Tick(now.Add(promptSettleDelay))
				if a.State != NeedsInput || a.Revision != revision {
					t.Fatal("navigation/repaint dismissed notification-only prompt", a)
				}
				a.Input(now)
				a.Output([]byte("resumed after submission"), now)
				_, _ = e.Write([]byte("resumed after submission"))
				a.Screen(e.PlainText(), now)
				a.Tick(now.Add(promptSettleDelay))
				if a.State != Working {
					t.Fatal("notification-only prompt did not resume", a.State)
				}
				_, _ = e.Write([]byte("\x1b]9;Agent turn complete\a"))
				if native && a.State != Working || !native && a.State != Done {
					t.Fatal("terminal completion changed native precedence", a.State)
				}
				a.NativeEvent("interrupted")
				if a.State != Done {
					t.Fatal("native cancellation failed", a.State)
				}
			})
		}
	}
}

func TestGenericPromptTypingAndEmptyHints(t *testing.T) {
	now := time.Now()
	a := Activity{State: Working, IdleAfter: time.Second, Hints: []string{"", "Allow command?"}}
	a.Output([]byte("ordinary output"), now)
	if a.State != Working {
		t.Fatal("empty hint matched all output")
	}
	a.Output([]byte("Allow command?"), now)
	revision := a.Revision
	a.Interact(now)
	a.Output([]byte("typed text"), now)
	a.Tick(now.Add(time.Hour))
	if a.State != NeedsInput || a.Revision != revision {
		t.Fatal("editing consumed generic approval", a)
	}
	a.Input(now)
	a.Output([]byte("working"), now)
	a.Tick(now.Add(promptSettleDelay))
	if a.State != Working {
		t.Fatal("submitted generic response did not resume", a.State)
	}
	for _, terminal := range []State{Exited, Crashed} {
		a.setState(terminal)
		a.Notification("Approval requested: test")
		a.Screen(approvalScreen, now)
		a.Interact(now)
		a.Tick(now.Add(time.Hour))
		if a.State != terminal {
			t.Fatal("prompt resurrected terminal process", a)
		}
	}
}

func TestNotificationOnlyPromptEditingAndSubmission(t *testing.T) {
	for _, submit := range []uv.Key{
		{Code: uv.KeyEnter},
		{Code: uv.KeyEscape},
		{Code: 'c', Mod: uv.ModCtrl},
		{Code: 'y'},
		{Code: 'n'},
		{Code: 'a'},
		{Code: 'p'},
		{Code: 'd'},
	} {
		t.Run(fmt.Sprint(submit), func(t *testing.T) {
			p := &Pane{activity: codexActivity()}
			p.emu = NewEmulator(80, 24, p.activity.Complete, p.activity.Notification)
			drained := make(chan struct{})
			go func() { _, _ = io.Copy(io.Discard, p.emu); close(drained) }()
			defer func() { _ = p.emu.Close(); <-drained }()
			p.activity.Notification("Approval requested: clipped footer")
			revision := p.activity.Revision
			for _, edit := range []func(){
				func() { p.Key(uv.Key{Code: uv.KeyDown}, false) },
				func() { p.Key(uv.Key{Code: 'z', Text: "z"}, false) },
				func() { p.Paste("answer") },
			} {
				edit()
				now := time.Now()
				p.activity.Output([]byte("repaint"), now)
				p.activity.Screen("clipped dialog", now)
				p.activity.Tick(now.Add(time.Hour))
				if p.activity.State != NeedsInput || p.activity.Revision != revision {
					t.Fatal("editing dismissed notification-only approval", p.activity)
				}
			}
			p.Key(submit, false)
			now := time.Now()
			p.activity.Output([]byte("resumed"), now)
			p.activity.Screen("running", now)
			p.activity.Tick(now.Add(promptSettleDelay))
			if p.activity.State != Working {
				t.Fatal("submitted response did not resume work", p.activity)
			}
		})
	}
}

func TestQuestionNotificationTypingPreservesPrompt(t *testing.T) {
	for _, announcement := range []string{"Question: What name?", "Plan mode prompt: What plan?"} {
		p := &Pane{activity: codexActivity()}
		p.emu = NewEmulator(80, 24, p.activity.Complete, p.activity.Notification)
		drained := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, p.emu); close(drained) }()
		t.Cleanup(func() { _ = p.emu.Close(); <-drained })
		p.activity.Notification(announcement)
		revision := p.activity.Revision
		for _, code := range "yapdnrc" {
			p.Key(uv.Key{Code: code}, false)
			now := time.Now()
			p.activity.Output([]byte("editing"), now)
			p.activity.Screen("clipped questionnaire", now)
			p.activity.Tick(now.Add(time.Hour))
			if p.activity.State != NeedsInput || p.activity.Revision != revision {
				t.Fatalf("question text submitted: %q / %c", announcement, code)
			}
		}
		now := time.Now()
		submit := uv.Key{Code: uv.KeyEnter}
		if strings.HasPrefix(announcement, "Question:") {
			p.Key(uv.Key{Code: uv.KeyEnter}, false)
			p.activity.Output([]byte("next answer"), now)
			p.activity.Screen("clipped questionnaire", now)
			p.activity.Tick(now.Add(time.Hour))
			if p.activity.State != NeedsInput || p.activity.Revision != revision {
				t.Fatal("selecting a question option dismissed the questionnaire", p.activity)
			}
			submit = uv.Key{Code: 's', Mod: uv.ModCtrl}
		}
		p.Key(submit, false)
		p.activity.Output([]byte("resuming"), now)
		p.activity.Screen("running", now)
		p.activity.Tick(now.Add(promptSettleDelay))
		if p.activity.State != Working {
			t.Fatal("submitting all question answers did not resume", p.activity)
		}
	}
}

func TestNewNotificationDoesNotInheritPromptResolution(t *testing.T) {
	now := time.Now()
	a := codexActivity()
	a.Screen(approvalScreen, now)
	a.Input(now)
	a.Screen("resuming", now)
	a.Notification("Question: What name?")
	revision := a.Revision
	a.Screen("clipped question", now)
	a.Tick(now.Add(time.Hour))
	if a.State != NeedsInput || a.Revision != revision {
		t.Fatal("new question inherited approval submission or disappearance", a)
	}
	a.NativeEvent("done")
	if a.State != Done || a.announcement != "" {
		t.Fatal("completion retained notification context", a)
	}
}
