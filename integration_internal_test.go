package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// Helper to restore makeTransport after test
func cleanupMakeTransport(original func(interface{}, *transport.TransportOptions) (transport.Transport, error)) {
	makeTransport = original
}

// TestSimpleQueryResponse ports test_simple_query_response from test_integration.py
func TestSimpleQueryResponse(t *testing.T) {
	// Save original factory
	originalMakeTransport := makeTransport
	defer cleanupMakeTransport(originalMakeTransport)

	// Create mock transport
	mockT := newMockTransport()

	// Setup auto-initialization
	handleInitialization(mockT, nil)

	// Mock transport factory
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	// Mock the message stream
	go func() {
		// Wait a bit to simulate processing
		time.Sleep(10 * time.Millisecond)

		// Assistant message
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "2 + 2 equals 4"},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}

		// Result message
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(1000),
			"duration_api_ms": float64(800),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "test-session",
			"total_cost_usd":  0.001,
		}

		// Close channel to signal end of stream
		mockT.Close()
	}()

	// Run query
	ctx := context.Background()
	messagesChan, errChan := Query(ctx, "What is 2 + 2?", nil)

	// Collect messages
	var messages []Message
	timeout := time.After(1 * time.Second)

loop:
	for {
		select {
		case msg, ok := <-messagesChan:
			if !ok {
				break loop
			}
			messages = append(messages, msg)
		case err := <-errChan:
			if err != nil {
				t.Fatalf("Query received error: %v", err)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for messages")
		}
	}

	// Verify results
	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	// Check assistant message
	assistantMsg, ok := messages[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected AssistantMessage at index 0, got %T", messages[0])
	}
	if len(assistantMsg.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(assistantMsg.Content))
	}
	textBlock, ok := assistantMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected TextBlock, got %T", assistantMsg.Content[0])
	}
	if textBlock.Text != "2 + 2 equals 4" {
		t.Errorf("Expected '2 + 2 equals 4', got '%s'", textBlock.Text)
	}

	// Check result message
	resultMsg, ok := messages[1].(*ResultMessage)
	if !ok {
		t.Fatalf("Expected ResultMessage at index 1, got %T", messages[1])
	}
	if resultMsg.TotalCostUSD != 0.001 {
		t.Errorf("Expected cost 0.001, got %f", resultMsg.TotalCostUSD)
	}
	if resultMsg.SessionID != "test-session" {
		t.Errorf("Expected session ID 'test-session', got '%s'", resultMsg.SessionID)
	}
}

// TestQueryWithToolUse ports test_query_with_tool_use from test_integration.py
func TestQueryWithToolUse(t *testing.T) {
	originalMakeTransport := makeTransport
	defer cleanupMakeTransport(originalMakeTransport)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Let me read that file for you.",
					},
					map[string]interface{}{
						"type": "tool_use",
						"id":   "tool-123",
						"name": "Read",
						"input": map[string]interface{}{
							"file_path": "/test.txt",
						},
					},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(1500),
			"duration_api_ms": float64(1200),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "test-session-2",
			"total_cost_usd":  0.002,
		}

		mockT.Close()
	}()

	ctx := context.Background()
	opts := &ClaudeAgentOptions{
		AllowedTools: []string{"Read"},
	}
	messagesChan, errChan := Query(ctx, "Read /test.txt", opts)

	var messages []Message
	timeout := time.After(1 * time.Second)

loop:
	for {
		select {
		case msg, ok := <-messagesChan:
			if !ok {
				break loop
			}
			messages = append(messages, msg)
		case err := <-errChan:
			if err != nil {
				t.Fatalf("Query received error: %v", err)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for messages")
		}
	}

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	assistantMsg, ok := messages[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected AssistantMessage at index 0, got %T", messages[0])
	}
	if len(assistantMsg.Content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %d", len(assistantMsg.Content))
	}

	textBlock, ok := assistantMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected TextBlock at content[0], got %T", assistantMsg.Content[0])
	}
	if textBlock.Text != "Let me read that file for you." {
		t.Errorf("Expected text match, got '%s'", textBlock.Text)
	}

	toolUse, ok := assistantMsg.Content[1].(*ToolUseBlock)
	if !ok {
		t.Fatalf("Expected ToolUseBlock at content[1], got %T", assistantMsg.Content[1])
	}
	if toolUse.Name != "Read" {
		t.Errorf("Expected tool name 'Read', got '%s'", toolUse.Name)
	}
	if filePath, ok := toolUse.Input["file_path"].(string); !ok || filePath != "/test.txt" {
		t.Errorf("Expected file_path '/test.txt', got %v", toolUse.Input["file_path"])
	}
}

// TestContinuationOption ports test_continuation_option from test_integration.py
func TestContinuationOption(t *testing.T) {
	originalMakeTransport := makeTransport
	defer cleanupMakeTransport(originalMakeTransport)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)

	var capturedOpts *transport.TransportOptions
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Continuing from previous conversation",
					},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}

		mockT.Close()
	}()

	ctx := context.Background()
	opts := &ClaudeAgentOptions{
		ContinueConversation: true,
	}

	// We just need to trigger the query to check captured options
	// We can use QuerySync for convenience here
	_, _ = QuerySync(ctx, "Continue", opts)

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}
	if !capturedOpts.ContinueConversation {
		t.Error("Expected ContinueConversation to be true")
	}
}

// TestMaxBudgetUSDOption ports test_max_budget_usd_option from test_integration.py
func TestMaxBudgetUSDOption(t *testing.T) {
	originalMakeTransport := makeTransport
	defer cleanupMakeTransport(originalMakeTransport)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)

	var capturedOpts *transport.TransportOptions
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Starting to read..."},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}

		// Send error_max_budget_usd result
		// Note: The Python test expects "subtype": "error_max_budget_usd" inside a "result" type message
		// But usually errors might come as actual errors or ResultMessage with specific subtype.
		// Looking at python code: yield {"type": "result", "subtype": "error_max_budget_usd", ...}
		// So it parses as a ResultMessage.
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "error_max_budget_usd",
			"duration_ms":     float64(500),
			"duration_api_ms": float64(400),
			"is_error":        false, // Python test asserts is_error: False
			"num_turns":       float64(1),
			"session_id":      "test-session-budget",
			"total_cost_usd":  0.0002,
			"usage": map[string]interface{}{
				"input_tokens":  float64(100),
				"output_tokens": float64(50),
			},
		}

		mockT.Close()
	}()

	ctx := context.Background()
	opts := &ClaudeAgentOptions{
		MaxBudgetUSD: 0.0001,
	}
	messagesChan, errChan := Query(ctx, "Read the readme", opts)

	var messages []Message
	timeout := time.After(1 * time.Second)

loop:
	for {
		select {
		case msg, ok := <-messagesChan:
			if !ok {
				break loop
			}
			messages = append(messages, msg)
		case err := <-errChan:
			if err != nil {
				t.Fatalf("Query received error: %v", err)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for messages")
		}
	}

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	resultMsg, ok := messages[1].(*ResultMessage)
	if !ok {
		t.Fatalf("Expected ResultMessage at index 1, got %T", messages[1])
	}

	if resultMsg.Subtype != "error_max_budget_usd" {
		t.Errorf("Expected subtype 'error_max_budget_usd', got '%s'", resultMsg.Subtype)
	}
	if resultMsg.IsError {
		t.Error("Expected IsError to be false")
	}
	if resultMsg.TotalCostUSD != 0.0002 {
		t.Errorf("Expected cost 0.0002, got %f", resultMsg.TotalCostUSD)
	}
	if resultMsg.TotalCostUSD <= 0 {
		t.Error("Expected positive total cost")
	}

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}
	if capturedOpts.MaxBudgetUSD != 0.0001 {
		t.Errorf("Expected MaxBudgetUSD 0.0001, got %f", capturedOpts.MaxBudgetUSD)
	}
}

// TestCLINotFound ports test_cli_not_found from test_integration.py
func TestCLINotFound(t *testing.T) {
	originalMakeTransport := makeTransport
	defer cleanupMakeTransport(originalMakeTransport)

	// We mock makeTransport to return an error that simulates CLI not found
	// In the real implementation, NewSubprocessTransport would return transport.CLINotFoundError.
	// We verify that Query wraps this into claude.CLINotFoundError.

	mockErr := &transport.CLINotFoundError{
		CLIPath: "/path/to/claude",
	}

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return nil, mockErr
	}

	ctx := context.Background()
	_, errChan := Query(ctx, "test", nil)

	select {
	case err := <-errChan:
		if err == nil {
			t.Fatal("Expected error, got nil")
		}
		// Verify it's the error we expect (claude.CLINotFoundError, NOT transport.CLINotFoundError)
		if _, ok := err.(*CLINotFoundError); !ok {
			t.Errorf("Expected claude.CLINotFoundError, got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), "Claude Code not found") {
			t.Errorf("Expected error message containing 'Claude Code not found', got '%s'", err.Error())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for error")
	}
}

// Minimal CLINotFoundError for testing purposes if not already defined in errors.go
// Checking errors.go... if it exists there, we use that.
// If it doesn't exist, we should define it or use the one from the package.
// For now, assuming it might be defined or we need to define it to match Python's CLINotFoundError.
// Let's check errors.go first.
