package tui

import (
	"errors"
	"testing"

	"github.com/mmoehabb/maestro/internal/store"
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
