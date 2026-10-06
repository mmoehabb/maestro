package term

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

const maxPendingInput = 16 << 20

// inputQueue decouples emulator replies/keystrokes from a blocked agent. Its
// reader alone writes to the PTY; producers never wait for the agent to read.
type inputQueue struct {
	mu     sync.Mutex
	ready  *sync.Cond
	buffer bytes.Buffer
	closed bool
	err    error
}

func newInputQueue() *inputQueue {
	q := &inputQueue{}
	q.ready = sync.NewCond(&q.mu)
	return q
}

func (q *inputQueue) Write(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0, io.ErrClosedPipe
	}
	if len(p) > maxPendingInput-q.buffer.Len() {
		q.err = errors.New("agent input buffer exceeded 16 MiB; restart the agent before sending more input")
		q.closed = true
		q.buffer.Reset()
		q.ready.Broadcast()
		return 0, q.err
	}
	n, err := q.buffer.Write(p)
	q.ready.Signal()
	return n, err
}

func (q *inputQueue) Read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.buffer.Len() == 0 && !q.closed {
		q.ready.Wait()
	}
	if q.closed {
		return 0, io.EOF
	}
	return q.buffer.Read(p)
}

func (q *inputQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.buffer.Reset()
	q.ready.Broadcast()
}

func (q *inputQueue) Err() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err
}
