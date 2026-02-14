package internal

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mockTransport is a simple mock for testing Query.
type mockTransport struct {
	messages chan map[string]interface{}
	errors   chan error
	written  []string
	ready    bool
	mu       sync.Mutex
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		messages: make(chan map[string]interface{}, 10),
		errors:   make(chan error, 10),
		written:  []string{},
		ready:    true,
	}
}

func (m *mockTransport) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready = true
	return nil
}

func (m *mockTransport) Write(data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written = append(m.written, data)
	return nil
}

func (m *mockTransport) ReadMessages() <-chan map[string]interface{} {
	return m.messages
}

func (m *mockTransport) Errors() <-chan error {
	return m.errors
}

func (m *mockTransport) EndInput() error {
	return nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready = false
	close(m.messages)
	close(m.errors)
	return nil
}

func (m *mockTransport) IsReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready
}

func (m *mockTransport) getWritten() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.written))
	copy(result, m.written)
	return result
}

// TestNewQuery tests Query creation.
func TestNewQuery(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	if query == nil {
		t.Fatal("Expected non-nil Query")
	}

	if query.transport != mockTrans {
		t.Error("Transport not set correctly")
	}

	if !query.isStreamingMode {
		t.Error("IsStreamingMode not set correctly")
	}

	if query.initializeTimeout == 0 {
		t.Error("Expected default initialize timeout to be set")
	}
}

// TestQueryInitialize tests the initialize handshake.
func TestQueryInitialize(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Need to extract request ID from written data
	go func() {
		time.Sleep(20 * time.Millisecond)
		written := mockTrans.getWritten()
		if len(written) > 0 {
			var req map[string]interface{}
			json.Unmarshal([]byte(written[0]), &req)
			if reqID, ok := req["request_id"].(string); ok {
				mockTrans.messages <- map[string]interface{}{
					"type": "control_response",
					"response": map[string]interface{}{
						"subtype":    "success",
						"request_id": reqID,
						"response": map[string]interface{}{
							"status": "initialized",
						},
					},
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := query.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if result == nil {
		t.Error("Expected non-nil result")
	}

	if !query.initialized {
		t.Error("Query should be marked as initialized")
	}
}

// TestQueryInitializeNonStreaming tests initialize in non-streaming mode.
func TestQueryInitializeNonStreaming(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: false,
	})

	ctx := context.Background()
	result, err := query.Initialize(ctx)

	if err != nil {
		t.Errorf("Initialize should not error in non-streaming mode: %v", err)
	}

	if result != nil {
		t.Error("Expected nil result in non-streaming mode")
	}
}

// TestQueryWrite tests writing to transport.
func TestQueryWrite(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport: mockTrans,
	})

	err := query.Write("test data\n")
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}

	if len(mockTrans.written) != 1 {
		t.Errorf("Expected 1 write, got %d", len(mockTrans.written))
	}

	if mockTrans.written[0] != "test data\n" {
		t.Errorf("Expected 'test data\\n', got %q", mockTrans.written[0])
	}
}

// TestQueryEndInput tests ending input.
func TestQueryEndInput(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport: mockTrans,
	})

	err := query.EndInput()
	if err != nil {
		t.Errorf("EndInput failed: %v", err)
	}
}

// TestQueryClose tests closing the query.
func TestQueryClose(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport: mockTrans,
	})

	err := query.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	if !query.closed {
		t.Error("Query should be marked as closed")
	}
}

// TestQueryChannels tests message and error channels.
func TestQueryChannels(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport: mockTrans,
	})

	messages := query.RawMessages()
	if messages == nil {
		t.Error("Expected non-nil messages channel")
	}

	errors := query.Errors()
	if errors == nil {
		t.Error("Expected non-nil errors channel")
	}
}

// TestQueryMessageRouting tests message routing.
func TestQueryMessageRouting(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Send a regular message
	go func() {
		mockTrans.messages <- map[string]interface{}{
			"type": "assistant",
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "Hello",
				},
			},
		}
		mockTrans.Close()
	}()

	// Should receive the message
	select {
	case msg := <-query.RawMessages():
		if msg["type"] != "assistant" {
			t.Errorf("Expected assistant message, got %v", msg["type"])
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for message")
	}
}

// TestQueryControlResponseRouting tests control response routing.
func TestQueryControlResponseRouting(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Register a pending response
	requestID := "test_req_123"
	responseCh := make(chan controlResult, 1)
	query.mu.Lock()
	query.pendingResponses[requestID] = responseCh
	query.mu.Unlock()

	// Send control response
	go func() {
		mockTrans.messages <- map[string]interface{}{
			"type": "control_response",
			"response": map[string]interface{}{
				"subtype":    "success",
				"request_id": requestID,
				"response": map[string]interface{}{
					"data": "test",
				},
			},
		}
	}()

	// Should receive the response
	select {
	case result := <-responseCh:
		if result.err != nil {
			t.Errorf("Expected no error, got %v", result.err)
		}
		if result.response == nil {
			t.Error("Expected non-nil response")
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for control response")
	}
}

// TestQueryControlErrorResponse tests error control responses.
func TestQueryControlErrorResponse(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Register a pending response
	requestID := "test_req_456"
	responseCh := make(chan controlResult, 1)
	query.mu.Lock()
	query.pendingResponses[requestID] = responseCh
	query.mu.Unlock()

	// Send error response
	go func() {
		mockTrans.messages <- map[string]interface{}{
			"type": "control_response",
			"response": map[string]interface{}{
				"subtype":    "error",
				"request_id": requestID,
				"error":      "something went wrong",
			},
		}
	}()

	// Should receive the error
	select {
	case result := <-responseCh:
		if result.err == nil {
			t.Error("Expected error, got nil")
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for error response")
	}
}

// TestHandleCanUseTool tests tool permission handling.
func TestHandleCanUseTool(t *testing.T) {
	mockTrans := newMockTransport()

	canUseToolCalled := false
	canUseTool := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		canUseToolCalled = true
		if toolName == "dangerous_tool" {
			return &PermissionResultDeny{
				Message:   "Tool not allowed",
				Interrupt: false,
			}, nil
		}
		return &PermissionResultAllow{
			UpdatedInput: input,
		}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})

	// Test allow
	request := map[string]interface{}{
		"tool_name": "safe_tool",
		"input": map[string]interface{}{
			"arg": "value",
		},
	}

	response, err := query.handleCanUseTool(request)
	if err != nil {
		t.Errorf("handleCanUseTool failed: %v", err)
	}

	if !canUseToolCalled {
		t.Error("canUseTool callback was not called")
	}

	if response["behavior"] != "allow" {
		t.Errorf("Expected behavior='allow', got %v", response["behavior"])
	}

	// Test deny
	canUseToolCalled = false
	request = map[string]interface{}{
		"tool_name": "dangerous_tool",
		"input":     map[string]interface{}{},
	}

	response, err = query.handleCanUseTool(request)
	if err != nil {
		t.Errorf("handleCanUseTool failed: %v", err)
	}

	if !canUseToolCalled {
		t.Error("canUseTool callback was not called")
	}

	if response["behavior"] != "deny" {
		t.Errorf("Expected behavior='deny', got %v", response["behavior"])
	}
}

// TestHandleMCPMessage tests MCP message handling.
func TestHandleMCPMessage(t *testing.T) {
	mockTrans := newMockTransport()

	// Create a test MCP server
	server := &MCPServer{
		Name:    "test_server",
		Version: "1.0.0",
		Tools: []MCPTool{
			{
				Name:        "test_tool",
				Description: "A test tool",
				InputSchema: map[string]interface{}{
					"type": "object",
				},
				Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
					return map[string]interface{}{
						"content": []map[string]interface{}{
							{"type": "text", "text": "success"},
						},
					}, nil
				},
			},
		},
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		SdkMCPServers: map[string]*MCPServer{
			"test_server": server,
		},
	})

	// Test tools/list
	request := map[string]interface{}{
		"server_name": "test_server",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		},
	}

	response, err := query.handleMCPMessage(request)
	if err != nil {
		t.Errorf("handleMCPMessage failed: %v", err)
	}

	mcpResp, ok := response["mcp_response"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcp_response in response")
	}

	result, ok := mcpResp["result"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected result in mcp_response")
	}

	tools, ok := result["tools"].([]map[string]interface{})
	if !ok {
		t.Fatal("Expected tools array in result")
	}

	if len(tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(tools))
	}

	// Test tools/call
	request = map[string]interface{}{
		"server_name": "test_server",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      "test_tool",
				"arguments": map[string]interface{}{},
			},
		},
	}

	response, err = query.handleMCPMessage(request)
	if err != nil {
		t.Errorf("handleMCPMessage failed: %v", err)
	}

	mcpResp, ok = response["mcp_response"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected mcp_response in response")
	}

	if mcpResp["error"] != nil {
		t.Errorf("Expected no error, got %v", mcpResp["error"])
	}
}

// TestHandleHookCallback tests hook callback handling.
func TestHandleHookCallback(t *testing.T) {
	mockTrans := newMockTransport()

	hookCalled := false
	hookCallback := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		hookCalled = true
		if input.ToolName == "Bash" {
			return HookOutput{
				HookSpecificOutput: map[string]interface{}{
					"hookEventName":            "PreToolUse",
					"permissionDecision":       "deny",
					"permissionDecisionReason": "Dangerous command",
				},
			}, nil
		}
		return HookOutput{}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	// Register callback
	callbackID := "hook_123"
	query.hookCallbacks[callbackID] = hookCallback

	request := map[string]interface{}{
		"callback_id": callbackID,
		"tool_use_id": "tool_456",
		"input": map[string]interface{}{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Bash",
			"tool_input": map[string]interface{}{
				"command": "rm -rf /",
			},
		},
	}

	response, err := query.handleHookCallback(request)
	if err != nil {
		t.Errorf("handleHookCallback failed: %v", err)
	}

	if !hookCalled {
		t.Error("Hook callback was not called")
	}

	if response == nil {
		t.Fatal("Expected non-nil response")
	}

	hookOutput, ok := response["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected hookSpecificOutput in response")
	}

	if hookOutput["permissionDecision"] != "deny" {
		t.Errorf("Expected permissionDecision='deny', got %v", hookOutput["permissionDecision"])
	}
}

// TestHandleHookCallbackFields tests all hook callback fields mapping.
func TestHandleHookCallbackFields(t *testing.T) {
	mockTrans := newMockTransport()

	hookCallback := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		shouldContinue := true
		return HookOutput{
			Continue:       &shouldContinue,
			Async:          true,
			AsyncTimeout:   120,
			SuppressOutput: true,
			StopReason:     "stop",
			Decision:       "allow",
			SystemMessage:  "sys_msg",
			Reason:         "because",
			HookSpecificOutput: map[string]interface{}{
				"foo": "bar",
			},
		}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	// Register callback
	callbackID := "hook_fields_test"
	query.hookCallbacks[callbackID] = hookCallback

	request := map[string]interface{}{
		"callback_id": callbackID,
		"tool_use_id": "tool_fields",
		"input": map[string]interface{}{
			"hook_event_name": "PreToolUse",
		},
	}

	response, err := query.handleHookCallback(request)
	if err != nil {
		t.Errorf("handleHookCallback failed: %v", err)
	}

	// Verify fields
	if val, ok := response["continue"].(bool); !ok || !val {
		t.Error("Expected continue=true")
	}
	if val, ok := response["async"].(bool); !ok || !val {
		t.Error("Expected async=true")
	}
	if val, ok := response["asyncTimeout"].(int); !ok || val != 120 {
		t.Errorf("Expected asyncTimeout=120, got %v", val)
	}
	if val, ok := response["suppressOutput"].(bool); !ok || !val {
		t.Error("Expected suppressOutput=true")
	}
	if val, ok := response["stopReason"].(string); !ok || val != "stop" {
		t.Errorf("Expected stopReason='stop', got %v", val)
	}
	if val, ok := response["decision"].(string); !ok || val != "allow" {
		t.Errorf("Expected decision='allow', got %v", val)
	}
	if val, ok := response["systemMessage"].(string); !ok || val != "sys_msg" {
		t.Errorf("Expected systemMessage='sys_msg', got %v", val)
	}
	if val, ok := response["reason"].(string); !ok || val != "because" {
		t.Errorf("Expected reason='because', got %v", val)
	}

	hookSpecific, ok := response["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected hookSpecificOutput")
	}
	if hookSpecific["foo"] != "bar" {
		t.Error("Expected hookSpecificOutput.foo='bar'")
	}
}

// TestHandleCanUseToolUpdatedInput tests UpdatedInput in permission results.
func TestHandleCanUseToolUpdatedInput(t *testing.T) {
	mockTrans := newMockTransport()

	canUseTool := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{
			UpdatedInput: map[string]interface{}{
				"arg": "updated_value",
			},
			UpdatedPermissions: []PermissionUpdate{
				{Type: "permanent"},
			},
		}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})

	request := map[string]interface{}{
		"tool_name": "some_tool",
		"input": map[string]interface{}{
			"arg": "original_value",
		},
	}

	response, err := query.handleCanUseTool(request)
	if err != nil {
		t.Errorf("handleCanUseTool failed: %v", err)
	}

	if response["behavior"] != "allow" {
		t.Errorf("Expected behavior='allow', got %v", response["behavior"])
	}

	updatedInput, ok := response["updatedInput"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected updatedInput in response")
	}
	if updatedInput["arg"] != "updated_value" {
		t.Errorf("Expected updatedInput.arg='updated_value', got %v", updatedInput["arg"])
	}

	updatedPerms, ok := response["updatedPermissions"].([]PermissionUpdate)
	if !ok {
		t.Fatal("Expected updatedPermissions in response")
	}
	if len(updatedPerms) != 1 || updatedPerms[0].Type != "permanent" {
		t.Error("Expected updatedPermissions to contain permanent update")
	}
}

// TestSendControlRequest tests sending control requests.
func TestSendControlRequest(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Simulate response
	go func() {
		time.Sleep(20 * time.Millisecond)
		// Extract request ID from written data
		written := mockTrans.getWritten()
		if len(written) > 0 {
			var req map[string]interface{}
			json.Unmarshal([]byte(written[0]), &req)
			if reqID, ok := req["request_id"].(string); ok {
				mockTrans.messages <- map[string]interface{}{
					"type": "control_response",
					"response": map[string]interface{}{
						"subtype":    "success",
						"request_id": reqID,
						"response": map[string]interface{}{
							"status": "ok",
						},
					},
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	request := map[string]interface{}{
		"subtype": "test_request",
	}

	response, err := query.sendControlRequest(ctx, request)
	if err != nil {
		t.Errorf("sendControlRequest failed: %v", err)
	}

	if response == nil {
		t.Error("Expected non-nil response")
	}
}

// TestSendControlRequestTimeout tests timeout handling.
func TestSendControlRequestTimeout(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Don't send any response - should timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	request := map[string]interface{}{
		"subtype": "test_request",
	}

	_, err := query.sendControlRequest(ctx, request)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// TestSendControlRequestNonStreaming tests error in non-streaming mode.
func TestSendControlRequestNonStreaming(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: false,
	})

	ctx := context.Background()
	request := map[string]interface{}{
		"subtype": "test_request",
	}

	_, err := query.sendControlRequest(ctx, request)
	if err == nil {
		t.Error("Expected error in non-streaming mode, got nil")
	}
}
