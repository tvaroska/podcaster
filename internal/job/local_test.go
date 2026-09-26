package job

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalDispatcher(t *testing.T) {
	var n atomic.Int32
	d := NewLocal(func(ctx context.Context, id string) error {
		if id != "ep_1" {
			t.Errorf("id %s", id)
		}
		n.Add(1)
		return nil
	}, 1, time.Second, nil)
	if err := d.Enqueue(context.Background(), "ep_1"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if n.Load() != 1 {
		t.Fatalf("processed %d", n.Load())
	}
}

func TestLocalDispatcherContextCancel(t *testing.T) {
	// Create dispatcher with buffer of 1 and a blocked worker
	blockCh := make(chan struct{})
	d := NewLocalWithQueueSize(func(ctx context.Context, id string) error {
		<-blockCh
		return nil
	}, 1, 1, time.Second, nil)
	t.Cleanup(func() {
		close(blockCh)
		d.Close()
	})

	// Fill the worker and the 1-slot channel
	if err := d.Enqueue(context.Background(), "ep_in_worker"); err != nil {
		t.Fatal(err)
	}
	// Give worker time to pull from queue
	time.Sleep(10 * time.Millisecond)
	if err := d.Enqueue(context.Background(), "ep_in_channel"); err != nil {
		t.Fatal(err)
	}

	// Next Enqueue with timed-out context should return ctx.Err() without blocking indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := d.Enqueue(ctx, "ep_overflow")
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestLocalDispatcherClosed(t *testing.T) {
	d := NewLocal(func(ctx context.Context, id string) error {
		return nil
	}, 1, time.Second, nil)
	d.Close()

	if err := d.Enqueue(context.Background(), "ep_after_close"); err != ErrDispatcherClosed {
		t.Fatalf("expected ErrDispatcherClosed, got %v", err)
	}
	if err := d.TryEnqueue("ep_after_close"); err != ErrDispatcherClosed {
		t.Fatalf("expected ErrDispatcherClosed, got %v", err)
	}
}
