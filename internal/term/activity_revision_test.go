package term

import (
	"testing"
	"time"
)

func TestActivityRevisionsTrackTransitions(t *testing.T) {
	now := time.Now()
	a := Activity{State: Starting, IdleAfter: time.Second, Hints: []string{"approve?"}}
	check := func(state State, revision uint64) {
		t.Helper()
		if a.State != state || a.Revision != revision {
			t.Fatalf("got %s/%d; want %s/%d", a.State, a.Revision, state, revision)
		}
	}
	a.Output([]byte("working"), now)
	check(Working, 1)
	a.Output([]byte("more output"), now)
	check(Working, 1)
	a.Tick(now.Add(2 * time.Second))
	check(Done, 2)
	a.Complete()
	check(Done, 2)
	a.Input(now)
	check(Working, 3)
	a.Output([]byte("approve?"), now)
	check(NeedsInput, 4)
	a.Output([]byte(" waiting"), now)
	check(NeedsInput, 4)
	a.Input(now)
	a.NativeEvent("started")
	check(Working, 5)
	a.Tick(now.Add(time.Hour))
	check(Working, 5)
	a.NativeEvent("done")
	check(Done, 6)
	a.NativeEvent("done")
	check(Done, 6)
	a.NativeEvent("started")
	a.NativeEvent("interrupted")
	check(Done, 8)
	a.setState(Exited)
	check(Exited, 9)
	a.NativeEvent("started")
	a.Input(now)
	check(Exited, 9)
}
