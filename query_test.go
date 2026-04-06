package claude

import (
	"context"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// TestQuery tests the one-shot Query function.
func TestQuery(t *testing.T) {
	// Save original factory
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	// Setup mock transport
	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	// Override factory
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		if promptStr, ok := prompt.(string); !ok || promptStr != "Say hello" {
			t.Errorf("Expected prompt 'Say hello', got %v", prompt)
		}
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)

		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",

			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-3-5-sonnet",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Hello world",
					},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result", // Signals end of stream
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-123",
		}
		mockTrans.Close()
	}()

	ctx := context.Background()
	messages, errors := Query(ctx, "Say hello", nil)

	// Collect results
	var msgs []Message
	var errs []error

	done := make(chan struct{})
	go func() {
		for msg := range messages {
			msgs = append(msgs, msg)
		}
		for err := range errors {
			errs = append(errs, err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for query results")
	}

	if len(errs) > 0 {
		t.Errorf("Unexpected errors: %v", errs)
	}

	if len(msgs) != 2 { // Text message + Result message
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	}

	// Verify text message
	asstMsg, ok := msgs[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected AssistantMessage, got %T", msgs[0])
	}

	if len(asstMsg.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(asstMsg.Content))
	}

	txtBlock, ok := asstMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected TextBlock, got %T", asstMsg.Content[0])
	}

	if txtBlock.Text != "Hello world" {
		t.Errorf("Expected text 'Hello world', got %q", txtBlock.Text)
	}

	// Verify result message
	_, ok = msgs[1].(*ResultMessage)
	if !ok {
		t.Errorf("Expected ResultMessage, got %T", msgs[1])
	}
}

// TestQueryWithOptions tests Query with options.
func TestQueryWithOptions(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		if opts.CWD != "/tmp" {
			t.Errorf("Expected CWD='/tmp', got %q", opts.CWD)
		}
		if opts.SystemPrompt != "sys_prompt" {
			t.Errorf("Expected SystemPrompt='sys_prompt', got %q", opts.SystemPrompt)
		}
		if len(opts.AllowedTools) != 2 || opts.AllowedTools[0] != "Read" || opts.AllowedTools[1] != "Write" {
			t.Errorf("Expected AllowedTools=['Read', 'Write'], got %v", opts.AllowedTools)
		}
		if opts.PermissionMode != "acceptEdits" {
			t.Errorf("Expected PermissionMode='acceptEdits', got %q", opts.PermissionMode)
		}
		if opts.MaxTurns != 5 {
			t.Errorf("Expected MaxTurns=5, got %d", opts.MaxTurns)
		}
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-123",
		}
		mockTrans.Close()
	}()

	opts := &ClaudeAgentOptions{
		CWD:            "/tmp",
		SystemPrompt:   "sys_prompt",
		AllowedTools:   []string{"Read", "Write"},
		PermissionMode: PermissionModeAcceptEdits,
		MaxTurns:       5,
	}

	ctx := context.Background()
	messages, _ := Query(ctx, "test", opts)

	// Drain messages
	for range messages {
	}
}

// TestQuerySync tests QuerySync helper.
func TestQuerySync(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-3-5-sonnet",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Sync"},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-123",
		}
		mockTrans.Close()
	}()

	ctx := context.Background()
	msgs, err := QuerySync(ctx, "test", nil)

	if err != nil {
		t.Errorf("QuerySync failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	}
}

// TestQueryDeadlockRegression verifies that Query() does not deadlock when
// the transport only closes stdout after receiving stdin EOF.
//
// This regression test simulates real CLI behavior:
//   - CLI keeps stdout open until stdin is closed (EOF)
//   - Without the fix, Query() goroutine hangs forever waiting for channels
//     to close, while channels only close when the goroutine exits (circular)
//   - The fix: call q.EndInput() after forwarding a ResultMessage
func TestQueryDeadlockRegression(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	endInputCalled := make(chan struct{})

	// EndInput simulates real CLI: close channels (CLI exits) after stdin EOF.
	mockTrans.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			mockTrans.closeOnce.Do(func() {
				close(mockTrans.readCh)
				close(mockTrans.errCh)
			})
		}()
		return nil
	}

	mockTrans.CloseFunc = func() error {
		_ = mockTrans.EndInputFunc()
		return nil
	}

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	// Send messages WITHOUT calling mockTrans.Close() — the key difference
	// from existing tests. The transport only closes after EndInput().
	go func() {
		time.Sleep(20 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-sonnet-4-5",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello!"},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-deadlock",
		}
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)

	var msgs []Message
	done := make(chan struct{})
	go func() {
		for msg := range messages {
			msgs = append(msgs, msg)
		}
		for range errs {
		}
		close(done)
	}()

	select {
	case <-done:
		// Success — Query completed without deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: Query() did not complete within 5 seconds")
	}

	select {
	case <-endInputCalled:
	default:
		t.Error("EndInput was never called after ResultMessage")
	}

	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages (assistant + result), got %d", len(msgs))
	}
	if len(msgs) >= 2 {
		if _, ok := msgs[0].(*AssistantMessage); !ok {
			t.Errorf("First message should be AssistantMessage, got %T", msgs[0])
		}
		if _, ok := msgs[1].(*ResultMessage); !ok {
			t.Errorf("Second message should be ResultMessage, got %T", msgs[1])
		}
	}
}

// TestQuerySyncDeadlockRegression is the QuerySync variant of the deadlock test.
func TestQuerySyncDeadlockRegression(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	endInputCalled := make(chan struct{})

	mockTrans.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			mockTrans.closeOnce.Do(func() {
				close(mockTrans.readCh)
				close(mockTrans.errCh)
			})
		}()
		return nil
	}

	mockTrans.CloseFunc = func() error {
		_ = mockTrans.EndInputFunc()
		return nil
	}

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-sonnet-4-5",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Sync hello!"},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(200),
			"duration_api_ms": float64(100),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-sync-deadlock",
		}
	}()

	ctx := context.Background()
	done := make(chan struct{})
	var msgs []Message
	var queryErr error
	go func() {
		msgs, queryErr = QuerySync(ctx, "Hello", nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: QuerySync() did not complete within 5 seconds")
	}

	if queryErr != nil {
		t.Errorf("QuerySync failed: %v", queryErr)
	}
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	}
}
