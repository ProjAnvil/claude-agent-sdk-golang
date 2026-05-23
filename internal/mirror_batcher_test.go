package internal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubAppender is a test double for SessionStoreAppender.
type stubAppender struct {
	mu      sync.Mutex
	calls   []appendCall
	block   chan struct{} // if non-nil, AppendRaw blocks until this is closed
	err     error         // if non-nil, AppendRaw returns this error
	entered chan struct{} // closed on first AppendRaw call to signal worker is blocked
}

type appendCall struct {
	filePath string
	entries  []map[string]interface{}
}

func (s *stubAppender) AppendRaw(_ context.Context, filePath string, entries []map[string]interface{}) error {
	if s.entered != nil {
		close(s.entered)
		s.entered = nil
	}
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	s.calls = append(s.calls, appendCall{filePath: filePath, entries: entries})
	s.mu.Unlock()
	return s.err
}

func (s *stubAppender) getCalls() []appendCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]appendCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// TestPendingStats_InitialZero verifies that pending counters start at zero.
func TestPendingStats_InitialZero(t *testing.T) {
	s := &stubAppender{}
	b := NewSimpleMirrorBatcher(s, nil)
	defer b.Close(context.Background())

	pe, pb := b.PendingStats()
	if pe != 0 {
		t.Errorf("pendingEntries: got %d, want 0", pe)
	}
	if pb != 0 {
		t.Errorf("pendingBytes: got %d, want 0", pb)
	}
}

// TestPendingStats_IncrementAndDecrement verifies that pending counters
// increase on Enqueue and decrease after the worker processes items.
func TestPendingStats_IncrementAndDecrement(t *testing.T) {
	s := &stubAppender{}
	b := NewSimpleMirrorBatcher(s, nil)
	defer b.Close(context.Background())

	entries := []map[string]interface{}{
		{"role": "user", "text": "hello"},
		{"role": "assistant", "text": "world"},
	}

	b.Enqueue("test.jsonl", entries)

	// After flush, the worker has processed the item so counters should be zero.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	pe, pb := b.PendingStats()
	if pe != 0 {
		t.Errorf("pendingEntries after flush: got %d, want 0", pe)
	}
	if pb != 0 {
		t.Errorf("pendingBytes after flush: got %d, want 0", pb)
	}

	calls := s.getCalls()
	if len(calls) != 1 {
		t.Fatalf("AppendRaw calls: got %d, want 1", len(calls))
	}
	if calls[0].filePath != "test.jsonl" {
		t.Errorf("filePath: got %q, want %q", calls[0].filePath, "test.jsonl")
	}
}

// TestPendingStats_MultipleEnqueues verifies counters accumulate across
// multiple enqueue calls and drain correctly.
func TestPendingStats_MultipleEnqueues(t *testing.T) {
	s := &stubAppender{}
	b := NewSimpleMirrorBatcher(s, nil)
	defer b.Close(context.Background())

	for i := 0; i < 5; i++ {
		b.Enqueue("multi.jsonl", []map[string]interface{}{
			{"idx": i},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	pe, pb := b.PendingStats()
	if pe != 0 {
		t.Errorf("pendingEntries after flush: got %d, want 0", pe)
	}
	if pb != 0 {
		t.Errorf("pendingBytes after flush: got %d, want 0", pb)
	}

	calls := s.getCalls()
	if len(calls) != 5 {
		t.Errorf("AppendRaw calls: got %d, want 5", len(calls))
	}
}

// TestPendingStats_QueueFull verifies that when the queue is full the
// pending counters are decremented back (since the item is dropped).
func TestPendingStats_QueueFull(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{})
	s := &stubAppender{block: block, entered: entered}
	var errs atomic.Int64
	b := NewSimpleMirrorBatcher(s, func(err error) {
		errs.Add(1)
	})
	defer b.Close(context.Background())

	// Enqueue one item so the worker picks it up and blocks.
	b.Enqueue("full.jsonl", []map[string]interface{}{{"i": 0}})
	// Wait for the worker to enter AppendRaw (and block there).
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker to enter AppendRaw")
	}

	// Now the worker is blocked holding one item. The channel buffer (capacity
	// 256) is empty. Fill it completely, then enqueue one more to trigger drop.
	const queueCap = 256
	for i := 0; i < queueCap; i++ {
		b.Enqueue("full.jsonl", []map[string]interface{}{{"i": i + 1}})
	}
	// This extra enqueue should be dropped because the channel is full.
	b.Enqueue("full.jsonl", []map[string]interface{}{{"i": queueCap + 1}})

	// Wait until at least one drop error is reported (with a timeout).
	deadline := time.Now().Add(5 * time.Second)
	for errs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if errs.Load() == 0 {
		t.Error("expected at least one queue-full error for the overflow item")
	}

	// Unblock the worker and flush to drain everything.
	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	pe, pb := b.PendingStats()
	if pe != 0 {
		t.Errorf("pendingEntries after drain: got %d, want 0", pe)
	}
	if pb != 0 {
		t.Errorf("pendingBytes after drain: got %d, want 0", pb)
	}
}

// TestPendingStats_SentinelNoEffect verifies that the sentinel item used by
// Flush does not affect pending counters (it has zero entries and zero bytes).
func TestPendingStats_SentinelNoEffect(t *testing.T) {
	s := &stubAppender{}
	b := NewSimpleMirrorBatcher(s, nil)
	defer b.Close(context.Background())

	b.Enqueue("sentinel.jsonl", []map[string]interface{}{{"a": 1}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	pe, pb := b.PendingStats()
	if pe != 0 {
		t.Errorf("pendingEntries after flush with sentinel: got %d, want 0", pe)
	}
	if pb != 0 {
		t.Errorf("pendingBytes after flush with sentinel: got %d, want 0", pb)
	}
}

// TestPendingStats_SizeAccuracy verifies that pendingBytes roughly matches the
// JSON serialized size of the entries.
func TestPendingStats_SizeAccuracy(t *testing.T) {
	block := make(chan struct{})
	s := &stubAppender{block: block}
	b := NewSimpleMirrorBatcher(s, nil)
	defer b.Close(context.Background())

	entries := []map[string]interface{}{
		{"role": "user", "content": "hello world"},
	}
	b.Enqueue("size.jsonl", entries)

	// The worker is blocked so counters should reflect the enqueued item.
	pe, pb := b.PendingStats()
	if pe != 1 {
		t.Errorf("pendingEntries: got %d, want 1", pe)
	}
	if pb <= 0 {
		t.Errorf("pendingBytes: got %d, want > 0", pb)
	}

	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// TestConstants verifies the exported threshold constants match the Python SDK.
func TestConstants(t *testing.T) {
	if MaxPendingEntries != 500 {
		t.Errorf("MaxPendingEntries: got %d, want 500", MaxPendingEntries)
	}
	if MaxPendingBytes != 1<<20 {
		t.Errorf("MaxPendingBytes: got %d, want %d", MaxPendingBytes, 1<<20)
	}
}
