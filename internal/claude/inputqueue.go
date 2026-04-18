package claude

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrQueueFull   = errors.New("queue full: please wait for Claude to finish")
	ErrQueueClosed = errors.New("queue closed")
)

type InputQueue struct {
	mu        sync.Mutex
	ch        chan string
	capacity  int
	closed    bool
	closeOnce sync.Once
}

func NewInputQueue(capacity int) *InputQueue {
	if capacity <= 0 {
		capacity = 5
	}
	return &InputQueue{
		ch:       make(chan string, capacity),
		capacity: capacity,
	}
}

func (q *InputQueue) Enqueue(msg string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	select {
	case q.ch <- msg:
		return nil
	default:
		return ErrQueueFull
	}
}

func (q *InputQueue) Dequeue(ctx context.Context) (string, error) {
	select {
	case msg, ok := <-q.ch:
		if !ok {
			return "", ErrQueueClosed
		}
		return msg, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (q *InputQueue) Close() {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.closed = true
		close(q.ch)
	})
}

func (q *InputQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ch)
}
