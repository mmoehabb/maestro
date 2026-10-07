package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (r *Runtime) poll() {
	defer r.wg.Done()
	if !r.pollEnabled {
		return
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	notified := map[int64]bool{}
	lastError := ""
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(r.ctx, 50*time.Second)
		tasks, err := r.Service.List(ctx, false)
		for _, task := range tasks {
			if ctx.Err() != nil {
				break
			}
			previous := task
			if task.Lifecycle != "merged" && task.Lifecycle != "closed" && !task.CleanupPending {
				var refreshErr error
				task, refreshErr = r.Workflow(ctx, task, "refresh", WorkflowOptions{})
				if refreshErr != nil {
					err = errors.Join(err, fmt.Errorf("%s: %w", task.Slug, refreshErr))
					continue
				}
				if task != previous {
					copy := task
					r.emit(Event{TaskID: task.ID, Task: &copy})
				}
			}
			terminal := task.Lifecycle == "merged" || task.Lifecycle == "closed" || task.CleanupPending
			if !terminal {
				delete(notified, task.ID)
				continue
			}
			var cleanupErr error
			if r.pollCleanup == "auto" {
				task, cleanupErr = r.Workflow(ctx, task, "cleanup", WorkflowOptions{})
				if cleanupErr == nil {
					copy := task
					r.emit(Event{TaskID: task.ID, Task: &copy})
					continue
				}
			}
			if !notified[task.ID] {
				copy := task
				r.emit(Event{TaskID: task.ID, Task: &copy, Cleanup: r.pollCleanup != "never", Err: cleanupErr})
				notified[task.ID] = true
			}
		}
		cancel()
		if err != nil && r.ctx.Err() == nil {
			if err.Error() != lastError {
				r.emit(Event{Err: err})
				lastError = err.Error()
			}
		} else {
			lastError = ""
		}
		timer.Reset(time.Minute)
	}
}
