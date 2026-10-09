package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmoehabb/maestro/internal/core"
	"github.com/mmoehabb/maestro/internal/term"
)

func TestActivityObservationOrder(t *testing.T) {
	for _, snapshotsFirst := range []bool{false, true} {
		m := testModel(t)
		// Event reads are normally scheduled by Bubble Tea. Supply harmless
		// messages so executing a returned batch cannot block this unit test.
		runtime := &core.Runtime{Events: make(chan core.Event, 32)}
		m.runtime = runtime
		for range cap(runtime.Events) {
			runtime.Events <- core.Event{}
		}
		pane := new(term.Pane)
		m.tabs[1].pane = pane
		m.tabs[1].state = term.Starting
		n := &recordedNotifier{}
		m.SetNotifier(n)
		executeNotification(t, m.observeActivity())
		event := func(state term.State, revision uint64, p *term.Pane) {
			_, cmd := m.Update(core.Event{TaskID: m.tabs[1].task.ID, Pane: p, State: state, Revision: revision})
			executeNotification(t, cmd)
		}
		snapshot := func(state term.State, revision uint64) {
			m.tabs[1].acceptActivity(pane, state, revision)
			executeNotification(t, m.observeActivity())
		}
		if snapshotsFirst {
			snapshot(term.Done, 2)
		}
		event(term.Working, 1, pane)
		event(term.Done, 2, pane)
		snapshot(term.Done, 2)
		event(term.Done, 2, pane)
		if len(n.calls) != 1 || m.tabs[1].state != term.Done {
			t.Fatalf("same transition replayed: %v", n.calls)
		}
		// The intermediate Working state can be lost. A newer Done revision
		// is still a new turn and must not be swallowed by a time debounce.
		snapshot(term.Done, 4)
		event(term.Working, 3, pane)
		event(term.Done, 4, pane)
		event(term.NeedsInput, 5, pane)
		snapshot(term.NeedsInput, 5)
		if len(n.calls) != 3 || m.tabs[1].state != term.NeedsInput {
			t.Fatalf("rapid turns/needs input mishandled: %v", n.calls)
		}
		old := pane
		pane = new(term.Pane)
		m.tabs[1].attachPane(pane)
		event(term.Done, 100, old)
		if len(n.calls) != 3 || m.tabs[1].state != term.Starting {
			t.Fatal("replacement inherited old activity before its first snapshot")
		}
		event(term.Done, 2, pane)
		event(term.NeedsInput, 100, old)
		snapshot(term.Done, 2)
		if len(n.calls) != 4 || m.tabs[1].state != term.Done {
			t.Fatalf("replacement pane reused an old revision: %v", n.calls)
		}
	}
}

func TestNotificationHelperFailureKeepsLocalAlerts(t *testing.T) {
	m := testModel(t)
	m.observeActivity()
	m.tabs[1].state = term.Working
	m.observeActivity()
	m.tabs[1].state = term.Done
	m.observeActivity()
	if !strings.Contains(m.toast, "done") || m.attention[m.tabs[1].task.ID].IsZero() {
		t.Fatal("local alerts depend on desktop delivery")
	}
	_, _ = m.Update(notificationMsg{err: errors.New("helper unavailable")})
	if !m.notificationFailed || !strings.Contains(m.toast, "unavailable") {
		t.Fatal("helper failure not reported")
	}
	m.tabs[1].state = term.NeedsInput
	m.observeActivity()
	_, _ = m.Update(notificationMsg{err: errors.New("still unavailable")})
	if !strings.Contains(m.toast, "needs_input") {
		t.Fatal("repeated helper error obscured local alert")
	}
}
