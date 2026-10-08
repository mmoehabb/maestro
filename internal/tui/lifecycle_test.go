package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/term"
)

func TestArchiveResult(t *testing.T) {
	for _, active := range []int{0, 1, 2} {
		m := &Model{active: active, tabs: []tab{{task: store.Task{ID: 1}}, {task: store.Task{ID: 2}, pending: true}, {task: store.Task{ID: 3}}}}
		m.Update(archivedMsg{2, errors.New("save failed")})
		if len(m.tabs) != 3 || m.tabs[1].pending {
			t.Fatal("failure removed or blocked tab")
		}
		m.Update(archivedMsg{id: 2})
		if len(m.tabs) != 2 || m.active < 0 || m.active >= len(m.tabs) {
			t.Fatal("invalid selection")
		}
		if active == 2 && m.tabs[m.active].task.ID != 3 {
			t.Fatal("focus changed")
		}
		m.Update(archivedMsg{id: 1})
		m.Update(archivedMsg{id: 3})
		if len(m.tabs) != 0 || m.active != 0 {
			t.Fatal("last tab archive failed")
		}
	}
}

func TestArchiveReleasesActivityObservations(t *testing.T) {
	for _, workflow := range []bool{false, true} {
		m := testModel(t)
		task := m.tabs[1].task
		m.observed[task.ID] = activityObservation{pane: new(term.Pane), state: term.Done, revision: 2}
		m.attention[task.ID] = time.Now().Add(time.Minute)
		if workflow {
			task.Lifecycle = "archived"
			m.applyTask(task)
		} else {
			_, _ = m.Update(archivedMsg{id: task.ID})
		}
		if _, ok := m.observed[task.ID]; ok {
			t.Fatal("archived pane retained by activity tracking")
		}
		if _, ok := m.attention[task.ID]; ok {
			t.Fatal("archived task retained an attention marker")
		}
	}
}
