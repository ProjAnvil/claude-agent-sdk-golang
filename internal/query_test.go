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

	response, err := query.handleCanUseTool(context.Background(), request)
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

	response, err = query.handleCanUseTool(context.Background(), request)
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

	response, err := query.handleHookCallback(context.Background(), request)
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

	response, err := query.handleHookCallback(context.Background(), request)
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

	response, err := query.handleCanUseTool(context.Background(), request)
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

// ---------------------------------------------------------------------------
// control_cancel_request tests (ported from Python SDK v0.1.52 #751)
// ---------------------------------------------------------------------------

// TestCancelRequestCancelsInflightHook tests that a control_cancel_request
// message cancels an in-flight hook callback, matching Python test
// test_cancel_request_cancels_inflight_hook.
func TestCancelRequestCancelsInflightHook(t *testing.T) {
	mockTrans := newMockTransport()

	hookStarted := make(chan struct{})
	hookDone := make(chan error, 1)

	// A slow hook that blocks until its context is cancelled
	slowHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		close(hookStarted)
		// Simulate a long-running hook — the caller (handleControlRequest)
		// doesn't pass the context to the HookCallback signature directly,
		// but the spawnControlRequestHandler will stop waiting for the
		// result once the cancel fires by checking ctx.Err() after return.
		time.Sleep(5 * time.Second)
		return HookOutput{}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	// Register hook callback
	callbackID := "cancel_test_hook"
	query.hookCallbacks[callbackID] = slowHook

	query.Start()

	requestID := "req_cancel_001"

	// Simulate CLI sending a hook_callback control_request
	go func() {
		mockTrans.messages <- map[string]interface{}{
			"type":       "control_request",
			"request_id": requestID,
			"request": map[string]interface{}{
				"subtype":     "hook_callback",
				"callback_id": callbackID,
				"tool_use_id": "tool_123",
				"input": map[string]interface{}{
					"hook_event_name": "PreToolUse",
					"tool_name":       "Bash",
				},
			},
		}
	}()

	// Wait for the hook to be picked up
	select {
	case <-hookStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for hook to start")
	}

	// Verify the request is tracked in inflightRequests
	query.mu.Lock()
	_, tracked := query.inflightRequests[requestID]
	query.mu.Unlock()
	if !tracked {
		t.Fatal("Expected request to be tracked in inflightRequests")
	}

	// Simulate CLI sending control_cancel_request
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_cancel_request",
		"request_id": requestID,
	}

	// Give a moment for the cancel to propagate
	time.Sleep(100 * time.Millisecond)

	// Verify the request was removed from inflightRequests
	query.mu.Lock()
	_, stillTracked := query.inflightRequests[requestID]
	query.mu.Unlock()
	if stillTracked {
		t.Error("Expected request to be removed from inflightRequests after cancel")
	}

	select {
	case err := <-hookDone:
		if err != nil {
			t.Errorf("Unexpected hook error: %v", err)
		}
	default:
		// Hook may still be sleeping — that's fine, the important thing
		// is that inflightRequests was cleaned up and no response is sent
	}

	// Clean up
	mockTrans.Close()
}

// TestCancelRequestForUnknownIDIsNoop tests that cancelling a non-existent
// request_id is a graceful no-op, matching Python test
// test_cancel_request_for_unknown_id_is_noop.
func TestCancelRequestForUnknownIDIsNoop(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

	// Send cancel for a request that never existed
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_cancel_request",
		"request_id": "nonexistent_req_999",
	}

	// Give time for the message to be processed
	time.Sleep(50 * time.Millisecond)

	// Should not panic or error — verify query is still functional
	query.mu.Lock()
	inflightCount := len(query.inflightRequests)
	query.mu.Unlock()

	if inflightCount != 0 {
		t.Errorf("Expected 0 inflight requests, got %d", inflightCount)
	}

	// Clean up
	mockTrans.Close()
}

// TestCompletedRequestRemovedFromInflight tests that completed requests are
// properly removed from inflight tracking, so late cancels become no-ops,
// matching Python test test_completed_request_is_removed_from_inflight.
func TestCompletedRequestRemovedFromInflight(t *testing.T) {
	mockTrans := newMockTransport()

	// A fast hook that returns immediately
	fastHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{
			Decision: "allow",
		}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	callbackID := "fast_hook"
	query.hookCallbacks[callbackID] = fastHook

	query.Start()

	requestID := "req_fast_001"

	// Simulate CLI sending a hook_callback control_request
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": requestID,
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"tool_use_id": "tool_456",
			"input": map[string]interface{}{
				"hook_event_name": "PostToolUse",
				"tool_name":       "Read",
			},
		},
	}

	// Wait for the hook to complete and response to be sent
	time.Sleep(200 * time.Millisecond)

	// Verify the request was cleaned up from inflightRequests after completion
	query.mu.Lock()
	_, stillTracked := query.inflightRequests[requestID]
	query.mu.Unlock()
	if stillTracked {
		t.Error("Expected completed request to be removed from inflightRequests")
	}

	// Now send a late cancel — should be a no-op
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_cancel_request",
		"request_id": requestID,
	}

	// Give time for the cancel to be processed
	time.Sleep(50 * time.Millisecond)

	// Verify no crash or unexpected state
	query.mu.Lock()
	inflightCount := len(query.inflightRequests)
	query.mu.Unlock()
	if inflightCount != 0 {
		t.Errorf("Expected 0 inflight requests after late cancel, got %d", inflightCount)
	}

	// Clean up
	mockTrans.Close()
}

// TestCancelRequestPreventsResponse tests that after a cancel, no
// control_response is sent back to the CLI.
func TestCancelRequestPreventsResponse(t *testing.T) {
	mockTrans := newMockTransport()

	hookStarted := make(chan struct{})

	// Hook that blocks until signalled
	blockingHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		close(hookStarted)
		// Simulate blocking work for a short time
		time.Sleep(500 * time.Millisecond)
		return HookOutput{Decision: "allow"}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	callbackID := "blocking_hook"
	query.hookCallbacks[callbackID] = blockingHook

	query.Start()

	requestID := "req_noresponse_001"

	// Send hook callback request
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": requestID,
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"tool_use_id": "tool_789",
			"input": map[string]interface{}{
				"hook_event_name": "PreToolUse",
				"tool_name":       "Write",
			},
		},
	}

	// Wait for hook to start
	select {
	case <-hookStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for hook to start")
	}

	// Cancel the request before the hook completes
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_cancel_request",
		"request_id": requestID,
	}

	// Wait for the hook to finish and check if a response was sent
	time.Sleep(1 * time.Second)

	// Check written messages — should NOT contain a control_response for this request
	written := mockTrans.getWritten()
	for _, w := range written {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(w), &msg); err != nil {
			continue
		}
		if msg["type"] == "control_response" {
			resp, _ := msg["response"].(map[string]interface{})
			if resp != nil {
				if respID, _ := resp["request_id"].(string); respID == requestID {
					t.Error("Expected no control_response for cancelled request, but found one")
				}
			}
		}
	}

	mockTrans.Close()
}

// TestHandleCanUseToolWithCancelledContext tests that handleCanUseTool returns
// early when its context is already cancelled.
func TestHandleCanUseToolWithCancelledContext(t *testing.T) {
	mockTrans := newMockTransport()

	callbackInvoked := false
	canUseTool := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		callbackInvoked = true
		return &PermissionResultAllow{}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	request := map[string]interface{}{
		"tool_name": "some_tool",
		"input":     map[string]interface{}{},
	}

	_, err := query.handleCanUseTool(ctx, request)
	if err == nil {
		t.Error("Expected error from cancelled context")
	}
	if callbackInvoked {
		t.Error("Callback should not have been invoked with cancelled context")
	}
}

// TestHandleHookCallbackWithCancelledContext tests that handleHookCallback
// returns early when its context is already cancelled.
func TestHandleHookCallbackWithCancelledContext(t *testing.T) {
	mockTrans := newMockTransport()

	callbackInvoked := false
	hookCallback := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		callbackInvoked = true
		return HookOutput{}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	callbackID := "cancelled_ctx_hook"
	query.hookCallbacks[callbackID] = hookCallback

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	request := map[string]interface{}{
		"callback_id": callbackID,
		"tool_use_id": "tool_cancelled",
		"input": map[string]interface{}{
			"hook_event_name": "PreToolUse",
		},
	}

	_, err := query.handleHookCallback(ctx, request)
	if err == nil {
		t.Error("Expected error from cancelled context")
	}
	if callbackInvoked {
		t.Error("Callback should not have been invoked with cancelled context")
	}
}
