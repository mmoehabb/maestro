package agent

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Update struct {
	Transcript Transcript
	Err        error
	Reset      bool
}

// Watch reconciles snapshots after filesystem changes and at least once a second.
// Watching the directory survives atomic replacement. Unchanged files are not
// reparsed; OpenCode exports are polled once per second without overlapping calls.
func Watch(ctx context.Context, a Adapter, id, dir string) <-chan Update {
	ch := make(chan Update, 1)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		watcher, _ := fsnotify.NewWatcher()
		var events <-chan fsnotify.Event
		var failures <-chan error
		if watcher != nil {
			defer watcher.Close()
			events = watcher.Events
			failures = watcher.Errors
		}
		var previous os.FileInfo
		path, watched := "", ""
		read := func() {
			var before os.FileInfo
			reset := false
			if path != "" {
				before, _ = os.Stat(path)
				if before != nil && previous != nil {
					reset = before.Size() < previous.Size() || !os.SameFile(before, previous)
				}
				if before != nil && previous != nil && os.SameFile(before, previous) && before.Size() == previous.Size() && before.ModTime().Equal(previous.ModTime()) {
					return
				}
			}
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			transcript, err := a.Read(readCtx, id, dir)
			cancel()
			if transcript.Path != "" {
				path = transcript.Path
			}
			if err == nil && path != "" {
				previous = before
			} else {
				previous = nil
			}
			if path != "" && watcher != nil && watched == "" {
				if watcher.Add(filepath.Dir(path)) == nil {
					watched = filepath.Dir(path)
				}
			}
			select {
			case ch <- Update{Transcript: transcript, Err: err, Reset: reset}:
			case <-ctx.Done():
			}
		}
		read()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				read()
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if event.Name == path {
					read()
				}
			case _, ok := <-failures:
				if !ok {
					failures = nil
				}
				previous = nil
			}
		}
	}()
	return ch
}
