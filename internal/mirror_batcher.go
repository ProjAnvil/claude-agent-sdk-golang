package internal

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// MaxPendingEntries is the threshold for the number of pending entries above
// which a warning is logged. It mirrors the Python SDK constant
// MAX_PENDING_ENTRIES.
const MaxPendingEntries = 500

// MaxPendingBytes is the threshold for the approximate size of pending entry
// data (in bytes) above which a warning is logged. It mirrors the Python SDK
// constant MAX_PENDING_BYTES (1 MiB).
const MaxPendingBytes = 1 << 20 // 1 MiB

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
	filePath  string
	entries   []map[string]interface{}
	sizeBytes int           // approximate serialized size of entries
	done      chan struct{} // closed when this item has been processed
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

	pendingEntries int64 // atomic: number of entries waiting in the queue
	pendingBytes   int64 // atomic: approximate byte size of pending entries
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
	// Calculate approximate serialized size of entries.
	size := 0
	for _, e := range entries {
		if data, err := json.Marshal(e); err == nil {
			size += len(data)
		}
	}

	item := &mirrorItem{
		filePath:  filePath,
		entries:   entries,
		sizeBytes: size,
		done:      make(chan struct{}),
	}

	atomic.AddInt64(&b.pendingEntries, int64(len(entries)))
	atomic.AddInt64(&b.pendingBytes, int64(size))

	pe := atomic.LoadInt64(&b.pendingEntries)
	pb := atomic.LoadInt64(&b.pendingBytes)
	if pe > MaxPendingEntries || pb > MaxPendingBytes {
		slog.Warn("mirror batcher pending threshold exceeded",
			"pending_entries", pe,
			"pending_bytes", pb,
			"max_pending_entries", MaxPendingEntries,
			"max_pending_bytes", MaxPendingBytes,
		)
	}

	select {
	case b.queue <- item:
	default:
		// Queue full; decrement counters since this item will not be processed.
		atomic.AddInt64(&b.pendingEntries, -int64(len(entries)))
		atomic.AddInt64(&b.pendingBytes, -int64(size))
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

// PendingStats returns the current number of pending entries and approximate
// byte size of those entries in the batcher queue. The values are snapshots
// and may change concurrently.
func (b *SimpleMirrorBatcher) PendingStats() (entries int64, bytes int64) {
	return atomic.LoadInt64(&b.pendingEntries), atomic.LoadInt64(&b.pendingBytes)
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
			// Decrement pending counters now that this item has been processed.
			atomic.AddInt64(&b.pendingEntries, -int64(len(item.entries)))
			atomic.AddInt64(&b.pendingBytes, -int64(item.sizeBytes))
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
