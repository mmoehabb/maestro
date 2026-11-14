package core

import (
	"testing"
	"time"
)

// SetHandoffDoneGrace shortens the wait for a missing agent-written handoff.
func SetHandoffDoneGrace(t *testing.T, d time.Duration) {
	t.Helper()
	previous := handoffDoneGrace
	handoffDoneGrace = d
	t.Cleanup(func() { handoffDoneGrace = previous })
}
