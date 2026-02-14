package claude

import (
	"context"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// TestIntegration_SimpleQueryResponse matches python's test_simple_query_response
func TestIntegration_SimpleQueryResponse(t *testing.T) {
	mockT := createMockTransport()

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Mock responses
	go func() {
		// Ensure Connect happens first
		time.Sleep(100 * time.Millisecond)
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
		close(mockT.readCh) // Close channel to finish the Query loop
		close(mockT.errCh)
	}()

	msgs, err := client.Query(ctx, "What is 2 + 2?")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	var messages []Message
	for msg := range msgs {
		messages = append(messages, msg)
	}

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	// Check assistant message
	assistantMsg, ok := messages[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected AssistantMessage, got %T", messages[0])
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
		t.Fatalf("Expected ResultMessage, got %T", messages[1])
	}
	if resultMsg.TotalCostUSD != 0.001 {
		t.Errorf("Expected cost 0.001, got %f", resultMsg.TotalCostUSD)
	}
	if resultMsg.SessionID != "test-session" {
		t.Errorf("Expected session_id 'test-session', got '%s'", resultMsg.SessionID)
	}
}

// TestIntegration_QueryWithToolUse matches python's test_query_with_tool_use
func TestIntegration_QueryWithToolUse(t *testing.T) {
	mockT := createMockTransport()

	opts := &ClaudeAgentOptions{
		AllowedTools: []string{"Read"},
	}
	client := NewClient(opts)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Mock responses with tool use
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

	msgs, err := client.Query(ctx, "Read /test.txt")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	var messages []Message
	for msg := range msgs {
		messages = append(messages, msg)
	}

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	assistantMsg, ok := messages[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected AssistantMessage, got %T", messages[0])
	}
	if len(assistantMsg.Content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %d", len(assistantMsg.Content))
	}

	// Check text block
	textBlock, ok := assistantMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected TextBlock at index 0, got %T", assistantMsg.Content[0])
	}
	if textBlock.Text != "Let me read that file for you." {
		t.Errorf("Unexpected text: %s", textBlock.Text)
	}

	// Check tool use block
	toolBlock, ok := assistantMsg.Content[1].(*ToolUseBlock)
	if !ok {
		t.Fatalf("Expected ToolUseBlock at index 1, got %T", assistantMsg.Content[1])
	}
	if toolBlock.Name != "Read" {
		t.Errorf("Expected tool name 'Read', got '%s'", toolBlock.Name)
	}
	if toolBlock.ID != "tool-123" {
		t.Errorf("Expected tool ID 'tool-123', got '%s'", toolBlock.ID)
	}
	if path, ok := toolBlock.Input["file_path"].(string); !ok || path != "/test.txt" {
		t.Errorf("Expected file_path '/test.txt', got %v", toolBlock.Input["file_path"])
	}
}

// TestIntegration_ContinuationOption matches python's test_continuation_option
func TestIntegration_ContinuationOption(t *testing.T) {
	mockT := createMockTransport()
	var capturedOpts *transport.TransportOptions

	opts := &ClaudeAgentOptions{
		ContinueConversation: true,
	}
	client := NewClient(opts)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Send a dummy message to trigger responses
	go func() {
		mockT.readCh <- map[string]interface{}{
			"type": "result", "subtype": "success", "session_id": "test",
		}
		mockT.Close()
	}()
	msgs, _ := client.Query(ctx, "Continue")
	for range msgs {
	}

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}
	if !capturedOpts.ContinueConversation {
		t.Error("Expected ContinueConversation to be true")
	}
}

// TestIntegration_MaxBudgetUSDOption matches python's test_max_budget_usd_option
func TestIntegration_MaxBudgetUSDOption(t *testing.T) {
	mockT := createMockTransport()
	var capturedOpts *transport.TransportOptions

	opts := &ClaudeAgentOptions{
		MaxBudgetUSD: 0.0001,
	}
	client := NewClient(opts)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	go func() {
		mockT.readCh <- map[string]interface{}{
			"type":           "result",
			"subtype":        "error_max_budget_usd",
			"total_cost_usd": 0.0002,
			"is_error":       false,
		}
		mockT.Close()
	}()
	msgs, _ := client.Query(ctx, "Read readme")

	var messages []Message
	for msg := range msgs {
		messages = append(messages, msg)
	}

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}
	if capturedOpts.MaxBudgetUSD != 0.0001 {
		t.Errorf("Expected MaxBudgetUSD 0.0001, got %f", capturedOpts.MaxBudgetUSD)
	}

	// Verify result message handling
	if len(messages) > 0 {
		resultMsg, ok := messages[len(messages)-1].(*ResultMessage)
		if ok {
			if resultMsg.Subtype != "error_max_budget_usd" {
				t.Errorf("Expected subtype 'error_max_budget_usd', got '%s'", resultMsg.Subtype)
			}
		}
	}
}

// TestIntegration_CLINotFound matches python's test_cli_not_found
// Note: This tests that the error is propagated correctly from transport factory
func TestIntegration_CLINotFound(t *testing.T) {
	expectedErr := &transport.CLINotFoundError{CLIPath: "bad/path"}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return nil, expectedErr
	}

	ctx := context.Background()
	err := client.Connect(ctx)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Check if error is of correct type/message
	if _, ok := err.(*transport.CLINotFoundError); !ok {
		// It might be wrapped, but let's check if the message contains "not found"
		// or verify exact identity if possible.
		// Since Connect returns the error directly:
		if err != expectedErr {
			t.Errorf("Expected specific CLINotFoundError, got %v", err)
		}
	}
}
