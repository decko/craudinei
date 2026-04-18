package claude

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInputQueue_EnqueueDequeue(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(3)

	if err := q.Enqueue("hello"); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	got, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected %q, got: %q", "hello", got)
	}

	if q.Len() != 0 {
		t.Fatalf("expected len 0, got %d", q.Len())
	}
}

func TestInputQueue_FullQueue(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(2)

	if err := q.Enqueue("a"); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if err := q.Enqueue("b"); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	err := q.Enqueue("c")
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got: %v", err)
	}
}

func TestInputQueue_DequeueBlocking(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(1)

	done := make(chan string, 1)
	go func() {
		msg, err := q.Dequeue(context.Background())
		if err != nil {
			t.Errorf("Dequeue failed: %v", err)
			done <- ""
			return
		}
		done <- msg
	}()

	time.Sleep(10 * time.Millisecond)

	if q.Len() != 0 {
		t.Fatal("queue should be empty before Enqueue")
	}

	if err := q.Enqueue("blocking"); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	select {
	case got := <-done:
		if got != "blocking" {
			t.Fatalf("expected %q, got: %q", "blocking", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Dequeue did not unblock after Enqueue")
	}
}

func TestInputQueue_DequeueContextCancellation(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(ctx)
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Dequeue did not return after context cancellation")
	}
}

func TestInputQueue_Close(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(1)

	if err := q.Enqueue("msg"); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	q.Close()

	err := q.Enqueue("after close")
	if !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("expected ErrQueueClosed, got: %v", err)
	}

	got, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("first Dequeue failed: %v", err)
	}
	if got != "msg" {
		t.Fatalf("expected %q, got: %q", "msg", got)
	}

	_, err = q.Dequeue(context.Background())
	if !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("expected ErrQueueClosed, got: %v", err)
	}
}

func TestInputQueue_CloseIdempotent(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(1)

	q.Close()
	q.Close()
	q.Close()
}

func TestInputQueue_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(10)

	var wg sync.WaitGroup
	wg.Add(20)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			defer wg.Done()
			q.Enqueue(string(rune('a' + idx)))
		}(i)
	}

	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			q.Dequeue(context.Background())
		}()
	}

	wg.Wait()
}

func TestInputQueue_Ordering(t *testing.T) {
	t.Parallel()

	q := NewInputQueue(5)

	msgs := []string{"one", "two", "three"}

	for _, msg := range msgs {
		if err := q.Enqueue(msg); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	}

	for _, want := range msgs {
		got, err := q.Dequeue(context.Background())
		if err != nil {
			t.Fatalf("Dequeue failed: %v", err)
		}
		if got != want {
			t.Fatalf("expected %q, got: %q", want, got)
		}
	}
}
