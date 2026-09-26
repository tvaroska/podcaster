package job

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var (
	ErrDispatcherClosed = errors.New("local dispatcher is closed")
	ErrQueueFull        = errors.New("job queue is full")
)

// Processor is the function invoked for each queued episode.
type Processor func(ctx context.Context, episodeID string) error

// LocalDispatcher runs work in-process on a bounded worker pool.
type LocalDispatcher struct {
	process Processor
	timeout time.Duration
	log     *slog.Logger
	jobs    chan string
	wg      sync.WaitGroup
	once    sync.Once
	mu      sync.RWMutex
	closed  bool
}

func NewLocal(process Processor, workers int, timeout time.Duration, log *slog.Logger) *LocalDispatcher {
	return NewLocalWithQueueSize(process, workers, 64, timeout, log)
}

func NewLocalWithQueueSize(process Processor, workers int, queueSize int, timeout time.Duration, log *slog.Logger) *LocalDispatcher {
	if workers < 1 {
		workers = 1
	}
	if queueSize < 1 {
		queueSize = 64
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	d := &LocalDispatcher{
		process: process,
		timeout: timeout,
		log:     log,
		jobs:    make(chan string, queueSize),
	}
	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go d.loop()
	}
	return d
}

func (d *LocalDispatcher) Enqueue(ctx context.Context, episodeID string) error {
	d.mu.RLock()
	closed := d.closed
	d.mu.RUnlock()
	if closed {
		return ErrDispatcherClosed
	}

	select {
	case d.jobs <- episodeID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryEnqueue attempts to enqueue without blocking if the queue is full.
func (d *LocalDispatcher) TryEnqueue(episodeID string) error {
	d.mu.RLock()
	closed := d.closed
	d.mu.RUnlock()
	if closed {
		return ErrDispatcherClosed
	}

	select {
	case d.jobs <- episodeID:
		return nil
	default:
		return ErrQueueFull
	}
}

func (d *LocalDispatcher) Close() {
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		close(d.jobs)
		d.mu.Unlock()
	})
	d.wg.Wait()
}

func (d *LocalDispatcher) loop() {
	defer d.wg.Done()
	for id := range d.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		err := d.process(ctx, id)
		cancel()
		if err != nil {
			d.log.Error("local job failed", "episode_id", id, "err", err)
		}
	}
}
