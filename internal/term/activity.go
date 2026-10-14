package term

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

type State string

const (
	Starting   State = "starting"
	Working    State = "working"
	Done       State = "done"
	NeedsInput State = "needs_input"
	Exited     State = "exited"
	Crashed    State = "crashed"
)

// Activity is owned by the pane mutex. Idle never completes an untouched pane.
type Activity struct {
	State         State
	Revision      uint64
	LastOutput    time.Time
	IdleAfter     time.Duration
	Hints         []string
	Native        bool
	NativeWorking bool
	Prompts       PromptDetector
	tail          string
	dialog        bool
	screenPrompt  bool
	seenPrompt    bool
	promptInput   bool
	promptGone    time.Time
	ignorePrompt  bool
	announcement  string
}

// PromptDetector supplements configurable output hints with agent-specific
// screen and terminal-notification recognition.
type PromptDetector struct {
	Screen       func(string) bool
	Notification func(string) bool
	// Submission distinguishes decisions from editing in notification-only dialogs.
	Submission func(notification, key string) bool
}

// A clear/redraw may span several PTY reads. Confirm disappearance before
// resuming work, rather than briefly notifying again on every menu repaint.
const promptSettleDelay = 150 * time.Millisecond

func (a *Activity) setState(state State) {
	if a.State != state {
		a.State = state
		a.Revision++
	}
}

func (a *Activity) Output(b []byte, now time.Time) {
	if a.State == Exited || a.State == Crashed {
		return
	}
	a.LastOutput = now
	a.tail += string(b)
	if len(a.tail) > 4096 {
		a.tail = a.tail[len(a.tail)-4096:]
	}
	text := strings.ToLower(ansi.Strip(a.tail))
	hinted := false
	for _, hint := range a.Hints {
		if hint != "" && strings.Contains(text, strings.ToLower(hint)) {
			hinted = true
			break
		}
	}
	switch {
	case hinted:
		a.promptGone = time.Time{}
		a.setState(NeedsInput)
	case a.State == NeedsInput:
		if !a.dialog && a.promptInput && a.promptGone.IsZero() {
			a.promptGone = now
		}
	case !a.Native || a.State != Done:
		a.setState(Working)
	}
}

func (a *Activity) Input(now time.Time) {
	if a.State == Exited || a.State == Crashed {
		return
	}
	a.LastOutput, a.tail = now, ""
	if a.State == NeedsInput {
		// Submission still requires subsequent output or native completion
		// before dismissing a prompt.
		a.promptInput = true
		return
	}
	a.setState(Working)
}

// Interact forwards editing/navigation without consuming an output hint. A
// submitted answer clears that hint through Input; a dialog is resolved by
// its screen disappearing regardless of the agent's configured shortcuts.
func (a *Activity) Interact(now time.Time) {
	if a.State == NeedsInput {
		return
	}
	a.Input(now)
}

// Screen observes plain current cells after the emulator has applied output.
// Scrollback and erased text must not keep a completed dialog outstanding.
func (a *Activity) Screen(text string, now time.Time) {
	if a.Prompts.Screen == nil || a.State == Exited || a.State == Crashed {
		return
	}
	matched := a.Prompts.Screen(text)
	if a.ignorePrompt {
		if !matched {
			a.ignorePrompt = false
		}
		return
	}
	a.screenPrompt = matched
	if matched {
		a.dialog, a.seenPrompt = true, true
		a.promptGone = time.Time{}
		a.setState(NeedsInput)
	} else if a.dialog && (a.seenPrompt || a.promptInput) && a.promptGone.IsZero() {
		a.promptGone = now
	}
}

func (a *Activity) Notification(message string) {
	if a.State == Exited || a.State == Crashed {
		return
	}
	if a.Prompts.Notification != nil && a.Prompts.Notification(message) {
		a.dialog = true
		a.announcement = message
		a.promptInput = false
		// The new announcement may describe a different, clipped dialog.
		// Evidence from a previous visible prompt must not resolve it.
		a.seenPrompt, a.screenPrompt = false, false
		a.promptGone = time.Time{}
		a.setState(NeedsInput)
		return
	}
	a.Complete()
}

func (a *Activity) clearPrompt() {
	a.dialog, a.screenPrompt, a.seenPrompt, a.promptInput = false, false, false, false
	a.promptGone = time.Time{}
	a.tail = ""
	a.announcement = ""
}

func (a *Activity) Complete() {
	if a.State == Working && !a.NativeWorking {
		a.setState(Done)
		a.tail = ""
	}
}

func (a *Activity) Tick(now time.Time) {
	if a.State == NeedsInput && !a.promptGone.IsZero() && now.Sub(a.promptGone) >= promptSettleDelay {
		a.clearPrompt()
		a.setState(Working)
	}
	if !a.NativeWorking && a.State == Working && now.Sub(a.LastOutput) >= a.IdleAfter {
		a.Complete()
	}
}

// ActivePrefix never relies on TERM guesses: Enter remains input until the
// outer terminal positively reports disambiguation support.
func ActivePrefix(preferred, fallback string, enhanced bool) string {
	if !enhanced && (preferred == "ctrl+m" || preferred == "ctrl+j" || preferred == "enter") {
		return fallback
	}
	return preferred
}

// NativeEvent is called under the pane mutex. Terminal silence and BEL cannot
// finish a turn for which the adapter has reported a reliable start.
func (a *Activity) NativeEvent(kind string) {
	if a.State == Exited || a.State == Crashed {
		return
	}
	switch kind {
	case "restored_idle":
		a.Native, a.NativeWorking = true, false
	case "restored_started":
		a.Native, a.NativeWorking = true, true
		if a.State != NeedsInput {
			a.setState(Working)
		}
	case "started":
		a.Native = true
		a.NativeWorking = true
		if a.State != NeedsInput {
			a.ignorePrompt = false
			a.setState(Working)
			a.tail = ""
		}
	case "done", "interrupted":
		wasActive := a.NativeWorking || a.State == Working || a.State == NeedsInput
		a.Native = true
		a.NativeWorking = false
		a.ignorePrompt = a.screenPrompt
		a.clearPrompt()
		if wasActive {
			a.setState(Done)
		}
	case "unavailable":
		if a.Native {
			a.LastOutput = time.Now()
		}
		a.Native = false
		a.NativeWorking = false
	}
}
