package daemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mmoehabb/maestro/internal/core"
)

func TestInputQueueFailsVisiblyAndStopsAccepting(t *testing.T) {
	c := &Client{input: make(chan request, 1), events: make(chan core.Event, 1)}
	p := &remotePane{client: c}
	p.Paste("first")
	p.Paste("overflow")
	select {
	case event := <-c.events:
		if event.Err == nil || !strings.Contains(event.Err.Error(), "reattach") {
			t.Fatal(event.Err)
		}
	default:
		t.Fatal("overflow was silent")
	}
	<-c.input
	if err := c.enqueue(request{Method: "input"}); err == nil {
		t.Fatal("accepted input after overflow")
	}
}

func TestResponseErrorMapsHandoffFailed(t *testing.T) {
	r := result(nil, fmt.Errorf("failed: %w", core.ErrHandoffFailed))
	if r.Code != "handoff" {
		t.Fatalf("expected code handoff, got %q", r.Code)
	}
	err := responseError(r)
	if !errors.Is(err, core.ErrHandoffFailed) {
		t.Fatalf("expected ErrHandoffFailed, got %v", err)
	}
}
