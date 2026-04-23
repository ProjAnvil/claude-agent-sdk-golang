package internal

import (
	"context"
	"sync"
	"time"
)

// SessionStoreAppender is the minimal interface required by the batcher to
// forward transcript entries to a session store.
// It mirrors the Append method of claude.SessionStore but is defined here as
// an interface over primitive types to avoid circular imports.
type SessionStoreAppender interface {
	// Append adds entries to the session identified by filePath.
	// filePath is the raw file path from the transcript_mirror frame; the
	// store implementation is responsible for deriving the session key.
	AppendRaw(ctx context.Context, filePath string, entries []map[string]interface{}) error
}

// mirrorItem is a single unit of work for the batcher.
type mirrorItem struct {
	filePath string
	entries  []map[string]interface{}
	done     chan struct{} // closed when this item has been processed
}

// SimpleMirrorBatcher is a goroutine-based batcher that forwards
// transcript_mirror frames to a SessionStoreAppender.
//
// Items are processed serially in the order they are enqueued. Flush blocks
// until all currently-enqueued items have been processed.
//
// Adapter failures are retried up to 3 times total with short backoff (200 ms,
// then 800 ms). After the final attempt fails the item is dropped and onError
// is called. Adapters should dedupe by entry["uuid"] when present (some entry
// types lack a uuid) since a retried batch may partially overlap a prior
// partial write.
type SimpleMirrorBatcher struct {
	store    SessionStoreAppender
	onError  func(err error)
	queue    chan *mirrorItem
	wg       sync.WaitGroup
	stopOnce sync.Once
	done     chan struct{}
}

// NewSimpleMirrorBatcher creates and starts a new SimpleMirrorBatcher.
// onError is called (synchronously on the worker goroutine) for each item
// that fails after all retries. It may be nil to suppress error reporting.
func NewSimpleMirrorBatcher(store SessionStoreAppender, onError func(error)) *SimpleMirrorBatcher {
	b := &SimpleMirrorBatcher{
		store:   store,
		onError: onError,
		queue:   make(chan *mirrorItem, 256),
		done:    make(chan struct{}),
	}
	b.wg.Add(1)
	go b.run()
	return b
}

// Enqueue schedules (filePath, entries) for delivery to the session store.
// It does not block; if the internal queue is full, the item is dropped and
// onError is called with the reason.
func (b *SimpleMirrorBatcher) Enqueue(filePath string, entries []map[string]interface{}) {
	item := &mirrorItem{
		filePath: filePath,
		entries:  entries,
		done:     make(chan struct{}),
	}
	select {
	case b.queue <- item:
	default:
		close(item.done)
		if b.onError != nil {
			b.onError(errBatcherQueueFull)
		}
	}
}

// Flush waits for all currently-enqueued items to be processed.
func (b *SimpleMirrorBatcher) Flush(ctx context.Context) error {
	// Enqueue a sentinel item and wait for it to be processed.
	sentinel := &mirrorItem{done: make(chan struct{})}
	select {
	case b.queue <- sentinel:
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return nil
	}
	select {
	case <-sentinel.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return nil
	}
}

// Close flushes all pending items and shuts down the batcher.
func (b *SimpleMirrorBatcher) Close(ctx context.Context) error {
	err := b.Flush(ctx)
	b.stopOnce.Do(func() { close(b.done) })
	b.wg.Wait()
	return err
}

// mirrorRetryBackoff holds the sleep durations between successive retry
// attempts. Its length must be maxMirrorAttempts-1.
var mirrorRetryBackoff = [2]time.Duration{200 * time.Millisecond, 800 * time.Millisecond}

const maxMirrorAttempts = 3

// run is the worker goroutine.
func (b *SimpleMirrorBatcher) run() {
	defer b.wg.Done()
	for {
		select {
		case item, ok := <-b.queue:
			if !ok {
				return
			}
			if item.filePath != "" {
				// Attempt to deliver with up to maxMirrorAttempts retries.
				// Sleep between attempts to give transient adapter errors a
				// chance to resolve (mirrors Python SDK backoff of 200ms/800ms).
				var lastErr error
				for attempt := 0; attempt < maxMirrorAttempts; attempt++ {
					if attempt > 0 {
						time.Sleep(mirrorRetryBackoff[attempt-1])
					}
					if err := b.store.AppendRaw(context.Background(), item.filePath, item.entries); err != nil {
						lastErr = err
						continue
					}
					lastErr = nil
					break
				}
				if lastErr != nil && b.onError != nil {
					b.onError(lastErr)
				}
			}
			close(item.done)
		case <-b.done:
			return
		}
	}
}

// errBatcherQueueFull is returned when the batcher queue is full.
type batcherQueueFullError struct{}

func (batcherQueueFullError) Error() string { return "mirror batcher queue full; item dropped" }

var errBatcherQueueFull = batcherQueueFullError{}
