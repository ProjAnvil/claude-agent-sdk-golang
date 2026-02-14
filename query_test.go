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
