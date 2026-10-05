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
	State      State
	LastOutput time.Time
	IdleAfter  time.Duration
	Hints      []string
	tail       string
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
	if a.State != NeedsInput {
		a.State = Working
	}
	for _, hint := range a.Hints {
		if strings.Contains(text, strings.ToLower(hint)) {
			a.State = NeedsInput
			break
		}
	}
}

func (a *Activity) Input(now time.Time) {
	if a.State == Exited || a.State == Crashed {
		return
	}
	a.State, a.LastOutput, a.tail = Working, now, ""
}

func (a *Activity) Complete() {
	if a.State == Working {
		a.State = Done
		a.tail = ""
	}
}

func (a *Activity) Tick(now time.Time) {
	if a.State == Working && now.Sub(a.LastOutput) >= a.IdleAfter {
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
