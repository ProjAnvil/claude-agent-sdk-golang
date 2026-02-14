package claude

import (
	"context"
	"encoding/json"
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
	mockT := createMockTransport()

	var capturedPrompt interface{}

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

	if capturedPrompt != prompt {
		t.Errorf("Expected prompt '%s', got '%v'", prompt, capturedPrompt)
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
