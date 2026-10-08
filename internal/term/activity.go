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
	tail          string
}

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
	if a.State != NeedsInput && (!a.Native || a.State != Done) {
		a.setState(Working)
	}
	for _, hint := range a.Hints {
		if strings.Contains(text, strings.ToLower(hint)) {
			a.setState(NeedsInput)
			break
		}
	}
}

func (a *Activity) Input(now time.Time) {
	if a.State == Exited || a.State == Crashed {
		return
	}
	a.setState(Working)
	a.LastOutput, a.tail = now, ""
}

func (a *Activity) Complete() {
	if a.State == Working && !a.NativeWorking {
		a.setState(Done)
		a.tail = ""
	}
}

func (a *Activity) Tick(now time.Time) {
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
			a.setState(Working)
			a.tail = ""
		}
	case "done", "interrupted":
		wasActive := a.NativeWorking || a.State == Working || a.State == NeedsInput
		a.Native = true
		a.NativeWorking = false
		a.tail = ""
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
