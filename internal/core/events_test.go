package core

import (
	"context"
	"testing"
	"time"

	"github.com/mmoehabb/maestro/internal/store"
)

func TestLifecycleEventWaitsForConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runtime{ctx: ctx, Events: make(chan Event, 1)}
	r.Events <- Event{}
	sent := make(chan struct{})
	task := store.Task{ID: 42, Lifecycle: "archived"}
	go func() { r.emit(Event{Task: &task, TaskID: task.ID}); close(sent) }()
	select {
	case <-sent:
		t.Fatal("lifecycle event was dropped while buffer was full")
	case <-time.After(30 * time.Millisecond):
	}
	<-r.Events
	select {
	case e := <-r.Events:
		if e.Task == nil || e.Task.Lifecycle != "archived" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer resumed without receiving the archive update")
	}
	<-sent
}

func TestLifecycleDeliveryCancelsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runtime{ctx: ctx, Events: make(chan Event, 1)}
	r.Events <- Event{}
	sent := make(chan struct{})
	go func() { r.emit(Event{Task: &store.Task{ID: 42}}); close(sent) }()
	cancel()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on lifecycle delivery")
	}
}
