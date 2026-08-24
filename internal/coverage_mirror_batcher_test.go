package internal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// flakyAppender fails the first failTimes AppendRaw calls, then succeeds.
type flakyAppender struct {
	mu        sync.Mutex
	failTimes int
	calls     int
}

func (f *flakyAppender) AppendRaw(_ context.Context, _ string, _ []map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failTimes {
		return errors.New("transient store failure")
	}
	return nil
}

func (f *flakyAppender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// waitForEntered waits for the stubAppender worker to enter AppendRaw.
func waitForEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker to enter AppendRaw")
	}
}

// TestBatcherQueueFullErrorMessage verifies the queue-full sentinel error
// text and that a dropped item reports exactly that error via onError.
func TestBatcherQueueFullErrorMessage(t *testing.T) {
	if got := errBatcherQueueFull.Error(); got != "mirror batcher queue full; item dropped" {
		t.Errorf("Error() = %q, want %q", got, "mirror batcher queue full; item dropped")
	}

	block := make(chan struct{})
	entered := make(chan struct{})
	s := &stubAppender{block: block, entered: entered}
	errCh := make(chan error, 1)
	b := NewSimpleMirrorBatcher(s, func(err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	// Occupy the worker, then fill the queue completely.
	b.Enqueue("full.jsonl", []map[string]interface{}{{"i": 0}})
	waitForEntered(t, entered)
	const queueCap = 256
	for i := 0; i < queueCap; i++ {
		b.Enqueue("full.jsonl", []map[string]interface{}{{"i": i + 1}})
	}
	// This one overflows and must be dropped with the sentinel error.
	b.Enqueue("full.jsonl", []map[string]interface{}{{"i": queueCap + 1}})

	select {
	case err := <-errCh:
		if err.Error() != "mirror batcher queue full; item dropped" {
			t.Errorf("onError error = %q, want queue-full message", err.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected queue-full error for the overflow item")
	}

	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestBatcherFlushCancelledWhenQueueFull verifies Flush honors an
// already-cancelled context when the sentinel cannot even be queued.
func TestBatcherFlushCancelledWhenQueueFull(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{})
	s := &stubAppender{block: block, entered: entered}
	b := NewSimpleMirrorBatcher(s, nil)

	b.Enqueue("worker.jsonl", []map[string]interface{}{{"i": 0}})
	waitForEntered(t, entered)
	const queueCap = 256
	for i := 0; i < queueCap; i++ {
		b.Enqueue("full.jsonl", []map[string]interface{}{{"i": i + 1}})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the call
	if err := b.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Flush: got %v, want context.Canceled", err)
	}

	close(block)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := b.Close(ctx2); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestBatcherFlushTimeoutWhileWorkerBusy verifies Flush honors the context
// deadline while waiting for the sentinel to be processed.
func TestBatcherFlushTimeoutWhileWorkerBusy(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{})
	s := &stubAppender{block: block, entered: entered}
	b := NewSimpleMirrorBatcher(s, nil)

	b.Enqueue("busy.jsonl", []map[string]interface{}{{"i": 0}})
	waitForEntered(t, entered)

	// The sentinel fits in the queue, but the blocked worker never reaches
	// it, so Flush must give up at the context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := b.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Flush: got %v, want context.DeadlineExceeded", err)
	}

	close(block)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := b.Close(ctx2); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestBatcherFlushAfterClose verifies flushing a closed batcher returns
// promptly with no error instead of hanging on a worker that is gone.
func TestBatcherFlushAfterClose(t *testing.T) {
	s := &stubAppender{}
	b := NewSimpleMirrorBatcher(s, nil)
	b.Enqueue("x.jsonl", []map[string]interface{}{{"a": 1}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Repeat: depending on which select branch wins, Flush either notices the
	// closed done channel before or after queueing the sentinel; both must
	// return nil without blocking.
	for i := 0; i < 20; i++ {
		fctx, fcancel := context.WithTimeout(context.Background(), time.Second)
		err := b.Flush(fctx)
		fcancel()
		if err != nil {
			t.Fatalf("Flush after Close: got %v, want nil", err)
		}
	}
}

// TestBatcherRetriesTransientFailure verifies a failing store is retried
// with backoff and eventually delivered without an onError report.
func TestBatcherRetriesTransientFailure(t *testing.T) {
	store := &flakyAppender{failTimes: 2}
	var errCount int
	var errMu sync.Mutex
	b := NewSimpleMirrorBatcher(store, func(err error) {
		errMu.Lock()
		errCount++
		errMu.Unlock()
	})

	b.Enqueue("retry.jsonl", []map[string]interface{}{{"a": 1}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got := store.callCount(); got != 3 {
		t.Errorf("AppendRaw calls: got %d, want 3 (2 failures + 1 success)", got)
	}
	errMu.Lock()
	defer errMu.Unlock()
	if errCount != 0 {
		t.Errorf("onError calls: got %d, want 0 after eventual success", errCount)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := b.Close(ctx2); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestBatcherDropsAfterExhaustingRetries verifies an item whose store keeps
// failing is dropped after all retry attempts and reported via onError.
func TestBatcherDropsAfterExhaustingRetries(t *testing.T) {
	store := &mockAppender{fail: true, failErr: errors.New("permanent failure")}
	errCh := make(chan error, 1)
	b := NewSimpleMirrorBatcher(store, func(err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	b.Enqueue("drop.jsonl", []map[string]interface{}{{"a": 1}})

	select {
	case err := <-errCh:
		if err.Error() != "permanent failure" {
			t.Errorf("onError error = %q, want %q", err.Error(), "permanent failure")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("expected onError after retries were exhausted")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := b.Close(ctx2); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestBatcherEnqueueOverThreshold verifies that exceeding the pending-entry
// threshold still accepts the item (only a warning is logged) and delivery
// proceeds normally.
func TestBatcherEnqueueOverThreshold(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{})
	s := &stubAppender{block: block, entered: entered}
	b := NewSimpleMirrorBatcher(s, nil)

	entries := make([]map[string]interface{}, 0, MaxPendingEntries+1)
	for i := 0; i < MaxPendingEntries+1; i++ {
		entries = append(entries, map[string]interface{}{"i": i})
	}
	b.Enqueue("big.jsonl", entries)
	waitForEntered(t, entered)

	// The oversized item is pending in the worker, not dropped.
	pe, _ := b.PendingStats()
	if pe != int64(MaxPendingEntries+1) {
		t.Errorf("pendingEntries: got %d, want %d", pe, MaxPendingEntries+1)
	}

	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	calls := s.getCalls()
	if len(calls) != 1 || len(calls[0].entries) != MaxPendingEntries+1 {
		t.Errorf("AppendRaw calls: got %d with sizes %v, want 1 call with %d entries",
			len(calls), calls, MaxPendingEntries+1)
	}
}
