package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// Helper to create a mock transport with auto-initialization
func createMockTransport() *MockTransport {
	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	return mockT
}

func TestManualConnectDisconnect(t *testing.T) {
	mockT := createMockTransport()

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if !client.connected {
		t.Error("Client should be connected")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if client.connected {
		t.Error("Client should be disconnected")
	}
}

func TestConnectWithStringPrompt(t *testing.T) {
	mockT := newMockTransport()

	var capturedPrompt interface{}
	var writtenMessages []string
	handleInitialization(mockT, &writtenMessages)

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedPrompt = prompt
		return mockT, nil
	}

	ctx := context.Background()
	prompt := "Hello Claude"
	if err := client.Connect(ctx, prompt); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// String prompts create a channel for the transport; the actual text
	// is written via transport.Write() after connect.
	if _, ok := capturedPrompt.(chan map[string]interface{}); !ok {
		t.Errorf("Expected channel prompt for string input, got %T", capturedPrompt)
	}

	// Verify the string was written as a user message (writtenMessages captures all writes)
	found := false
	for _, msg := range writtenMessages {
		if strings.Contains(msg, "Hello Claude") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected string prompt to be written to transport, got %v", writtenMessages)
	}
}

func TestConnectWithChannel(t *testing.T) {
	mockT := createMockTransport()

	var capturedPrompt interface{}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedPrompt = prompt
		return mockT, nil
	}

	ctx := context.Background()
	ch := make(chan map[string]interface{})
	if err := client.Connect(ctx, ch); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if capturedPrompt != ch {
		t.Errorf("Expected prompt channel to be passed")
	}
}

func TestQueryWithChannel(t *testing.T) {
	mockT := createMockTransport()

	receivedMessages := make(chan map[string]interface{}, 10)
	done := make(chan bool)

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		if ch, ok := prompt.(chan map[string]interface{}); ok {
			go func() {
				defer close(done)
				for msg := range ch {
					receivedMessages <- msg
				}
			}()
		}
		return mockT, nil
	}

	ctx := context.Background()
	ch := make(chan map[string]interface{})

	if err := client.Connect(ctx, ch); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	go func() {
		ch <- map[string]interface{}{"role": "user", "content": "First"}
		ch <- map[string]interface{}{"role": "user", "content": "Second"}
		close(ch)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for channel processing")
	}

	close(receivedMessages)
	var msgs []map[string]interface{}
	for msg := range receivedMessages {
		msgs = append(msgs, msg)
	}

	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	} else {
		if content, ok := msgs[0]["content"].(string); !ok || content != "First" {
			t.Errorf("Expected content 'First', got '%v'", msgs[0]["content"])
		}
		if content, ok := msgs[1]["content"].(string); !ok || content != "Second" {
			t.Errorf("Expected content 'Second', got '%v'", msgs[1]["content"])
		}
	}
}

func TestReceiveResponseNotConnected(t *testing.T) {
	client := NewClient(nil)
	ctx := context.Background()

	_, err := client.ReceiveResponse(ctx)
	if err == nil {
		t.Error("Expected error when not connected")
	}
}

func TestDoubleConnect(t *testing.T) {
	mockT := createMockTransport()
	factoryCallCount := 0

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		factoryCallCount++
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Second Connect failed: %v", err)
	}

	if factoryCallCount != 1 {
		t.Errorf("Expected factory to be called once, got %d", factoryCallCount)
	}
}

func TestCloseCleanup(t *testing.T) {
	mockT := createMockTransport()
	closed := false
	mockT.CloseFunc = func() error {
		closed = true
		return nil
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	func() {
		defer client.Close()
	}()

	if !closed {
		t.Error("Transport should be closed")
	}
	if client.connected {
		t.Error("Client should be disconnected")
	}
}

func TestClientQuery(t *testing.T) {
	mockT := createMockTransport()
	var writtenData []string

	// Capture writes
	originalWrite := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		writtenData = append(writtenData, data)
		if originalWrite != nil {
			return originalWrite(data)
		}
		return nil
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Send query
	msgChan, err := client.Query(ctx, "Test message")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Drain channel
	go func() {
		for range msgChan {
		}
	}()

	// Verify write was called
	found := false
	for _, data := range writtenData {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["type"] == "user" {
				content := msg["message"].(map[string]interface{})["content"]
				if content == "Test message" {
					found = true
					break
				}
			}
		}
	}

	if !found {
		t.Error("User message not found in writes")
	}
}

func TestSendMessageNotConnected(t *testing.T) {
	client := NewClient(nil)
	ctx := context.Background()

	_, err := client.Query(ctx, "Test")
	if err == nil {
		t.Error("Expected error when not connected")
	}
}

func TestReceiveMessages(t *testing.T) {
	mockT := createMockTransport()

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Inject messages
	go func() {
		time.Sleep(10 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello!"},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}
		mockT.readCh <- map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"role":    "user",
				"content": "Hi there",
			},
		}
	}()

	// We can use a direct access to internal query messages if available,
	// but client.ReceiveMessages() is not exposed directly like in Python.
	// Wait, client.ReceiveResponse() is exposed.
	// client.Query returns a channel of messages.
	// But ReceiveMessages() iterator in Python iterates over ALL messages.
	// In Go, client.Query() returns a channel for that specific query response?
	// No, client.Query() implementation:
	// func (c *ClaudeSDKClient) Query(ctx context.Context, prompt string) (<-chan Message, error) {
	//    ...
	//    go func() {
	//        for rawMsg := range c.query.RawMessages() { ... }
	//    }()
	// }
	// It consumes c.query.RawMessages().

	// If we want to test "ReceiveMessages", we can use Query("") or just consume messages if we had a way.
	// Python's receive_messages() iterates over client.messages.

	// Let's test ReceiveResponse instead, as that's what we have.

	msgs, err := client.ReceiveResponse(ctx)
	if err != nil {
		t.Fatalf("ReceiveResponse failed: %v", err)
	}

	var received []Message
	timeout := time.After(1 * time.Second)

loop:
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				break loop
			}
			received = append(received, msg)
			if len(received) == 2 {
				break loop
			}
		case <-timeout:
			t.Fatal("Timeout waiting for messages")
		}
	}

	if len(received) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(received))
	}
}

func TestReceiveResponseStopsAtResult(t *testing.T) {
	mockT := createMockTransport()

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		// Assistant message
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Answer"},
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
			"session_id":      "test",
			"total_cost_usd":  0.001,
		}
		// Extra message (should be ignored)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Should not see this"},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}
	}()

	msgs, err := client.ReceiveResponse(ctx)
	if err != nil {
		t.Fatalf("ReceiveResponse failed: %v", err)
	}

	var received []Message
	for msg := range msgs {
		received = append(received, msg)
	}

	if len(received) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(received))
	}

	if _, ok := received[1].(*ResultMessage); !ok {
		t.Errorf("Expected ResultMessage as last message, got %T", received[1])
	}
}

func TestDisconnectWithoutConnect(t *testing.T) {
	client := NewClient(nil)
	// Should not panic or return error
	if err := client.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestClientWithOptions(t *testing.T) {
	mockT := createMockTransport()
	var capturedOpts *transport.TransportOptions

	opts := &ClaudeAgentOptions{
		CWD:            "/custom/path",
		AllowedTools:   []string{"Read", "Write"},
		SystemPrompt:   "Be helpful",
		PermissionMode: PermissionModeAcceptEdits,
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

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}

	if capturedOpts.CWD != "/custom/path" {
		t.Errorf("Expected CWD '/custom/path', got '%s'", capturedOpts.CWD)
	}
	if len(capturedOpts.AllowedTools) != 2 {
		t.Errorf("Expected 2 AllowedTools, got %d", len(capturedOpts.AllowedTools))
	}
	if capturedOpts.SystemPrompt != "Be helpful" {
		t.Errorf("Expected SystemPrompt 'Be helpful', got '%s'", capturedOpts.SystemPrompt)
	}
	if capturedOpts.PermissionMode != string(PermissionModeAcceptEdits) {
		t.Errorf("Expected PermissionMode '%s', got '%s'", PermissionModeAcceptEdits, capturedOpts.PermissionMode)
	}
}

func TestInterrupt(t *testing.T) {
	mockT := createMockTransport()
	var writtenData []string

	// Capture writes
	originalWrite := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		writtenData = append(writtenData, data)
		if originalWrite != nil {
			return originalWrite(data)
		}
		return nil
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := client.Interrupt(ctx); err != nil {
		t.Fatalf("Interrupt failed: %v", err)
	}

	// Verify interrupt request was sent
	found := false
	for _, data := range writtenData {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["type"] == "control_request" {
				req := msg["request"].(map[string]interface{})
				if req["subtype"] == "interrupt" {
					found = true
					break
				}
			}
		}
	}

	if !found {
		t.Error("Interrupt control request not found")
	}
}

func TestConcurrentSendReceive(t *testing.T) {
	mockT := createMockTransport()

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Prepare responses
	go func() {
		time.Sleep(50 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Response 1"},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}
		time.Sleep(50 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "test",
			"total_cost_usd":  0.001,
		}
	}()

	// Start receiving
	msgs, err := client.ReceiveResponse(ctx)
	if err != nil {
		t.Fatalf("ReceiveResponse failed: %v", err)
	}

	// Send query concurrently
	go func() {
		client.Send(ctx, "Question 1")
	}()

	// Wait for response
	var received []Message
	for msg := range msgs {
		received = append(received, msg)
	}

	if len(received) == 0 {
		t.Error("No messages received")
	}
	if _, ok := received[0].(*AssistantMessage); !ok {
		t.Errorf("Expected AssistantMessage, got %T", received[0])
	}
}

func TestQueryWithSessionID(t *testing.T) {
	mockT := createMockTransport()
	var writtenData []string

	// Capture writes
	originalWrite := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		writtenData = append(writtenData, data)
		if originalWrite != nil {
			return originalWrite(data)
		}
		return nil
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Send query with session ID
	_, err := client.Query(ctx, "Test message", WithSessionID("custom-session-123"))
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Verify write was called with correct session ID
	found := false
	for _, data := range writtenData {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["type"] == "user" && msg["session_id"] == "custom-session-123" {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("User message with custom session ID not found in writes")
	}
}

func TestGetContextUsage(t *testing.T) {
	mockT := newMockTransport()

	contextUsageResponse := map[string]interface{}{
		"categories": []interface{}{
			map[string]interface{}{"name": "System prompt", "tokens": float64(3200), "color": "#abc"},
			map[string]interface{}{"name": "Messages", "tokens": float64(61400), "color": "#def"},
		},
		"totalTokens":          float64(98200),
		"maxTokens":            float64(155000),
		"rawMaxTokens":         float64(200000),
		"percentage":           49.1,
		"model":                "claude-sonnet-4-5",
		"isAutoCompactEnabled": true,
		"memoryFiles":          []interface{}{map[string]interface{}{"path": "CLAUDE.md", "type": "project", "tokens": float64(512)}},
		"mcpTools":             []interface{}{map[string]interface{}{"name": "search", "serverName": "ref", "tokens": float64(164), "isLoaded": true}},
		"agents":               []interface{}{map[string]interface{}{"agentType": "coder", "source": "sdk", "tokens": float64(299)}},
		"gridRows":             []interface{}{},
	}

	// Handle init + get_context_usage control requests
	mockT.WriteFunc = func(data string) error {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return nil
		}
		if msg["type"] == "control_request" {
			reqID, _ := msg["request_id"].(string)
			req, _ := msg["request"].(map[string]interface{})
			subtype, _ := req["subtype"].(string)

			var respPayload map[string]interface{}
			switch subtype {
			case "initialize":
				respPayload = map[string]interface{}{"version": "0.1.0"}
			case "get_context_usage":
				respPayload = contextUsageResponse
			}
			if respPayload != nil {
				mockT.readCh <- map[string]interface{}{
					"type": "control_response",
					"response": map[string]interface{}{
						"subtype":    "success",
						"request_id": reqID,
						"response":   respPayload,
					},
				}
			}
		}
		return nil
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	usage, err := client.GetContextUsage(ctxWithTimeout)
	if err != nil {
		t.Fatalf("GetContextUsage: %v", err)
	}

	if usage.Model != "claude-sonnet-4-5" {
		t.Errorf("Expected model='claude-sonnet-4-5', got %v", usage.Model)
	}
	if usage.TotalTokens != 98200 {
		t.Errorf("Expected totalTokens=98200, got %v", usage.TotalTokens)
	}
	if usage.MaxTokens != 155000 {
		t.Errorf("Expected maxTokens=155000, got %v", usage.MaxTokens)
	}
	if usage.Percentage != 49.1 {
		t.Errorf("Expected percentage=49.1, got %v", usage.Percentage)
	}
	if usage.RawMaxTokens != 200000 {
		t.Errorf("Expected rawMaxTokens=200000, got %v", usage.RawMaxTokens)
	}
	if !usage.IsAutoCompactEnabled {
		t.Error("Expected isAutoCompactEnabled=true")
	}
	if len(usage.Categories) != 2 {
		t.Fatalf("Expected 2 categories, got %d", len(usage.Categories))
	}
	if usage.Categories[0].Name != "System prompt" {
		t.Errorf("Expected first category name='System prompt', got %v", usage.Categories[0].Name)
	}
	if usage.Categories[0].Tokens != 3200 {
		t.Errorf("Expected first category tokens=3200, got %v", usage.Categories[0].Tokens)
	}
	if len(usage.MemoryFiles) != 1 {
		t.Errorf("Expected 1 memory file, got %d", len(usage.MemoryFiles))
	}
	if len(usage.McpTools) != 1 {
		t.Errorf("Expected 1 MCP tool, got %d", len(usage.McpTools))
	}
	if len(usage.Agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(usage.Agents))
	}
}

func TestGetContextUsageNotConnected(t *testing.T) {
	client := NewClient(nil)
	ctx := context.Background()
	_, err := client.GetContextUsage(ctx)
	if err == nil {
		t.Fatal("Expected error when not connected")
	}
	if _, ok := err.(*CLIConnectionError); !ok {
		t.Errorf("Expected CLIConnectionError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// MCP control methods
// ---------------------------------------------------------------------------

// handleControlRequest is a helper that intercepts writes to respond to
// control requests matching a given subtype.
func handleControlRequest(mockT *MockTransport, subtype string, respPayload map[string]interface{}) {
	origWrite := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["type"] == "control_request" {
				req, _ := msg["request"].(map[string]interface{})
				reqSubtype, _ := req["subtype"].(string)
				reqID, _ := msg["request_id"].(string)

				if reqSubtype == subtype && reqID != "" {
					go func() {
						mockT.readCh <- map[string]interface{}{
							"type": "control_response",
							"response": map[string]interface{}{
								"subtype":    "success",
								"request_id": reqID,
								"response":   respPayload,
							},
						}
					}()
				}
			}
		}
		if origWrite != nil {
			return origWrite(data)
		}
		return nil
	}
}

func TestReconnectMCPServer(t *testing.T) {
	mockT := createMockTransport()
	handleControlRequest(mockT, "mcp_reconnect", map[string]interface{}{"ok": true})

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.ReconnectMCPServer(ctxWithTimeout, "test-server"); err != nil {
		t.Fatalf("ReconnectMCPServer: %v", err)
	}
}

func TestReconnectMCPServerNotConnected(t *testing.T) {
	client := NewClient(nil)
	err := client.ReconnectMCPServer(context.Background(), "test")
	if err == nil {
		t.Fatal("Expected error when not connected")
	}
	if _, ok := err.(*CLIConnectionError); !ok {
		t.Errorf("Expected CLIConnectionError, got %T", err)
	}
}

func TestToggleMCPServer_Enable(t *testing.T) {
	mockT := createMockTransport()
	handleControlRequest(mockT, "mcp_toggle", map[string]interface{}{"ok": true})

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.ToggleMCPServer(ctxWithTimeout, "my-server", true); err != nil {
		t.Fatalf("ToggleMCPServer(enable): %v", err)
	}
}

func TestToggleMCPServer_Disable(t *testing.T) {
	mockT := createMockTransport()
	handleControlRequest(mockT, "mcp_toggle", map[string]interface{}{"ok": true})

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.ToggleMCPServer(ctxWithTimeout, "my-server", false); err != nil {
		t.Fatalf("ToggleMCPServer(disable): %v", err)
	}
}

func TestToggleMCPServerNotConnected(t *testing.T) {
	client := NewClient(nil)
	err := client.ToggleMCPServer(context.Background(), "test", true)
	if err == nil {
		t.Fatal("Expected error when not connected")
	}
	if _, ok := err.(*CLIConnectionError); !ok {
		t.Errorf("Expected CLIConnectionError, got %T", err)
	}
}

func TestStopTask(t *testing.T) {
	mockT := createMockTransport()
	handleControlRequest(mockT, "stop_task", map[string]interface{}{"ok": true})

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.StopTask(ctxWithTimeout, "task-123"); err != nil {
		t.Fatalf("StopTask: %v", err)
	}
}

func TestStopTaskNotConnected(t *testing.T) {
	client := NewClient(nil)
	err := client.StopTask(context.Background(), "task-123")
	if err == nil {
		t.Fatal("Expected error when not connected")
	}
	if _, ok := err.(*CLIConnectionError); !ok {
		t.Errorf("Expected CLIConnectionError, got %T", err)
	}
}

func TestGetMCPStatus(t *testing.T) {
	mockT := createMockTransport()
	handleControlRequest(mockT, "mcp_status", map[string]interface{}{
		"mcpServers": []interface{}{
			map[string]interface{}{
				"name":   "test-server",
				"status": "connected",
				"error":  "",
				"scope":  "project",
				"serverInfo": map[string]interface{}{
					"name":    "TestServer",
					"version": "1.0.0",
				},
				"tools": []interface{}{
					map[string]interface{}{
						"name":        "test_tool",
						"description": "A test tool",
					},
				},
			},
		},
	})

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	status, err := client.GetMCPStatus(ctxWithTimeout)
	if err != nil {
		t.Fatalf("GetMCPStatus: %v", err)
	}

	if status == nil {
		t.Fatal("Expected non-nil status")
	}
	if len(status.MCPServers) != 1 {
		t.Fatalf("Expected 1 server, got %d", len(status.MCPServers))
	}

	srv := status.MCPServers[0]
	if srv.Name != "test-server" {
		t.Errorf("Expected name='test-server', got '%s'", srv.Name)
	}
	if srv.Status != McpServerStatusConnected {
		t.Errorf("Expected status='connected', got '%s'", srv.Status)
	}
	if srv.ServerInfo == nil || srv.ServerInfo.Name != "TestServer" {
		t.Error("Expected server info")
	}
	if len(srv.Tools) != 1 || srv.Tools[0].Name != "test_tool" {
		t.Error("Expected 1 tool named 'test_tool'")
	}
}

func TestGetMCPStatusNotConnected(t *testing.T) {
	client := NewClient(nil)
	_, err := client.GetMCPStatus(context.Background())
	if err == nil {
		t.Fatal("Expected error when not connected")
	}
	if _, ok := err.(*CLIConnectionError); !ok {
		t.Errorf("Expected CLIConnectionError, got %T", err)
	}
}
