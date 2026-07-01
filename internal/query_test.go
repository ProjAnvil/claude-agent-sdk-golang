package internal

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
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

// TestQueryInitializeSendsExcludeDynamicSections tests that excludeDynamicSections
// is included in the initialize request when configured (v0.1.57).
func TestQueryInitializeSendsExcludeDynamicSections(t *testing.T) {
	mockTrans := newMockTransport()
	trueVal := true

	query := NewQuery(QueryConfig{
		Transport:              mockTrans,
		IsStreamingMode:        true,
		ExcludeDynamicSections: &trueVal,
	})

	query.Start()

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

	_, err := query.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	written := mockTrans.getWritten()
	if len(written) == 0 {
		t.Fatal("Expected a written message")
	}

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(written[0]), &req); err != nil {
		t.Fatalf("Failed to parse written message: %v", err)
	}

	// The control request is wrapped: {"type":"control_request","request_id":"...","request":{...}}
	inner, ok := req["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected request wrapper, got: %v", req)
	}
	if inner["subtype"] != "initialize" {
		t.Errorf("Expected subtype=initialize, got %v", inner["subtype"])
	}
	if val, ok := inner["excludeDynamicSections"]; !ok || val != true {
		t.Errorf("Expected excludeDynamicSections=true in initialize request, got %v (ok=%v)", val, ok)
	}
}

// TestQueryInitializeOmitsExcludeDynamicSectionsWhenUnset tests that
// excludeDynamicSections is absent when not configured (v0.1.57).
func TestQueryInitializeOmitsExcludeDynamicSectionsWhenUnset(t *testing.T) {
	mockTrans := newMockTransport()

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	query.Start()

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

	_, err := query.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	written := mockTrans.getWritten()
	if len(written) == 0 {
		t.Fatal("Expected a written message")
	}

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(written[0]), &req); err != nil {
		t.Fatalf("Failed to parse written message: %v", err)
	}

	inner, ok := req["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected request wrapper, got: %v", req)
	}
	if _, ok := inner["excludeDynamicSections"]; ok {
		t.Errorf("Expected excludeDynamicSections to be absent in initialize request, but it was present")
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

// ---------------------------------------------------------------------------
// Tests for Skills and transcript_mirror — added in v0.1.65
// ---------------------------------------------------------------------------

// mockMirrorBatcher is a test double for TranscriptMirrorBatcher.
type mockMirrorBatcher struct {
	enqueued []string
	flushed  int
	closed   int
	mu       sync.Mutex
}

func (m *mockMirrorBatcher) Enqueue(filePath string, entries []map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enqueued = append(m.enqueued, filePath)
}

func (m *mockMirrorBatcher) Flush(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushed++
	return nil
}

func (m *mockMirrorBatcher) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed++
	return nil
}

// sendInitializeResponse is a helper that reads the first control_request
// (Initialize) from transport.written and replies with a success response.
func sendInitializeResponse(t *testing.T, mockTrans *mockTransport) {
	t.Helper()
	go func() {
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			written := mockTrans.getWritten()
			if len(written) == 0 {
				continue
			}
			var req map[string]interface{}
			if err := json.Unmarshal([]byte(written[0]), &req); err != nil {
				continue
			}
			reqID, _ := req["request_id"].(string)
			if reqID == "" {
				continue
			}
			mockTrans.messages <- map[string]interface{}{
				"type": "control_response",
				"response": map[string]interface{}{
					"subtype":    "success",
					"request_id": reqID,
					"response":   map[string]interface{}{"status": "initialized"},
				},
			}
			return
		}
	}()
}

// TestInitializeSendsSkillsListWhenSlice verifies that when Skills is a []string,
// the "skills" field is included in the initialize request body.
func TestInitializeSendsSkillsListWhenSlice(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		Skills:          []string{"skill-a", "skill-b"},
	})
	query.Start()
	sendInitializeResponse(t, mockTrans)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := query.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	written := mockTrans.getWritten()
	if len(written) == 0 {
		t.Fatal("No messages written")
	}

	var wrapper map[string]interface{}
	if err := json.Unmarshal([]byte(written[0]), &wrapper); err != nil {
		t.Fatalf("Parse wrapper: %v", err)
	}
	inner, ok := wrapper["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected request wrapper, got: %v", wrapper)
	}

	skillsRaw, found := inner["skills"]
	if !found {
		t.Fatalf("Expected 'skills' in initialize request inner body, got keys: %v", inner)
	}
	skills, ok := skillsRaw.([]interface{})
	if !ok {
		t.Fatalf("Expected skills to be []interface{}, got %T", skillsRaw)
	}
	if len(skills) != 2 {
		t.Errorf("Expected 2 skills, got %d: %v", len(skills), skills)
	}
}

// TestInitializeOmitsSkillsForNil verifies that nil Skills omits the "skills"
// field from the initialize request.
func TestInitializeOmitsSkillsForNil(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		Skills:          nil,
	})
	query.Start()
	sendInitializeResponse(t, mockTrans)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := query.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	written := mockTrans.getWritten()
	if len(written) == 0 {
		t.Fatal("No messages written")
	}

	var wrapper map[string]interface{}
	json.Unmarshal([]byte(written[0]), &wrapper) //nolint:errcheck
	inner, _ := wrapper["request"].(map[string]interface{})
	if _, found := inner["skills"]; found {
		t.Error("Did not expect 'skills' in initialize request when Skills is nil")
	}
}

// TestInitializeOmitsSkillsForAll verifies that Skills="all" omits the "skills"
// field from the initialize request (transport handles it via --allowedTools).
func TestInitializeOmitsSkillsForAll(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		Skills:          "all",
	})
	query.Start()
	sendInitializeResponse(t, mockTrans)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := query.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	written := mockTrans.getWritten()
	if len(written) == 0 {
		t.Fatal("No messages written")
	}

	var wrapper map[string]interface{}
	json.Unmarshal([]byte(written[0]), &wrapper) //nolint:errcheck
	inner, _ := wrapper["request"].(map[string]interface{})
	if _, found := inner["skills"]; found {
		t.Error("Did not expect 'skills' in initialize request when Skills=\"all\"")
	}
}

// TestTranscriptMirrorFramePeeled verifies that a transcript_mirror frame is
// forwarded to the MirrorBatcher and NOT surfaced to callers via RawMessages.
func TestTranscriptMirrorFramePeeled(t *testing.T) {
	mockTrans := newMockTransport()
	batcher := &mockMirrorBatcher{}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		MirrorBatcher:   batcher,
	})
	query.Start()

	// Send a transcript_mirror frame (with non-empty entries) followed by a
	// regular assistant message so we have something to drain from rawMessages.
	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.messages <- map[string]interface{}{
			"type":      "transcript_mirror",
			"file_path": "/some/session.jsonl",
			"entries": []interface{}{
				map[string]interface{}{"type": "user"},
			},
		}
		mockTrans.messages <- map[string]interface{}{
			"type":    "assistant",
			"content": "hello",
		}
		mockTrans.Close()
	}()

	// Drain rawMessages; transcript_mirror must NOT appear.
	var received []map[string]interface{}
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg, ok := <-query.RawMessages():
			if !ok {
				goto done
			}
			received = append(received, msg)
		case <-timeout:
			goto done
		}
	}
done:
	for _, m := range received {
		if m["type"] == "transcript_mirror" {
			t.Error("transcript_mirror frame should have been peeled off and not returned to caller")
		}
	}

	// Batcher must have been called with the file_path.
	batcher.mu.Lock()
	enqueued := append([]string(nil), batcher.enqueued...)
	batcher.mu.Unlock()
	if len(enqueued) == 0 {
		t.Error("Expected MirrorBatcher.Enqueue to be called for transcript_mirror frame")
	}
}

// ---------------------------------------------------------------------------
// Tests ported from Python SDK v0.1.73..v0.1.76
// ---------------------------------------------------------------------------

// TestHandleCanUseToolReceivesDecisionReason tests that the new permission context
// fields are forwarded to the canUseTool callback.
func TestHandleCanUseToolReceivesDecisionReason(t *testing.T) {
	mockTrans := newMockTransport()

	var receivedCtx ToolPermissionContext
	canUseTool := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		receivedCtx = ctx
		return &PermissionResultAllow{
			UpdatedInput: input,
		}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})

	request := map[string]interface{}{
		"tool_name":       "Write",
		"input":           map[string]interface{}{"file_path": "/etc/hosts"},
		"tool_use_id":     "toolu_001",
		"agent_id":        "agent_001",
		"blocked_path":    "/etc/hosts",
		"decision_reason": "PreToolUse hook returned permissionDecision=ask",
		"title":           "Claude wants to write to /etc/hosts",
		"display_name":    "Write file",
		"description":     "Write content to a system file",
		"permission_suggestions": []interface{}{
			map[string]interface{}{
				"type":     "addRules",
				"behavior": "allow",
				"rules": []interface{}{
					map[string]interface{}{
						"toolName":    "Write",
						"ruleContent": "/etc/hosts",
					},
				},
			},
		},
	}

	_, err := query.handleCanUseTool(context.Background(), request)
	if err != nil {
		t.Fatalf("handleCanUseTool failed: %v", err)
	}

	// Verify new fields
	if receivedCtx.BlockedPath != "/etc/hosts" {
		t.Errorf("Expected BlockedPath '/etc/hosts', got '%s'", receivedCtx.BlockedPath)
	}
	if receivedCtx.DecisionReason != "PreToolUse hook returned permissionDecision=ask" {
		t.Errorf("Expected DecisionReason, got '%s'", receivedCtx.DecisionReason)
	}
	if receivedCtx.Title != "Claude wants to write to /etc/hosts" {
		t.Errorf("Expected Title, got '%s'", receivedCtx.Title)
	}
	if receivedCtx.DisplayName != "Write file" {
		t.Errorf("Expected DisplayName 'Write file', got '%s'", receivedCtx.DisplayName)
	}
	if receivedCtx.Description != "Write content to a system file" {
		t.Errorf("Expected Description, got '%s'", receivedCtx.Description)
	}

	// Verify permission suggestions are properly deserialized
	if len(receivedCtx.Suggestions) != 1 {
		t.Fatalf("Expected 1 suggestion, got %d", len(receivedCtx.Suggestions))
	}
	sug := receivedCtx.Suggestions[0]
	if sug.Type != "addRules" {
		t.Errorf("Expected suggestion type 'addRules', got '%s'", sug.Type)
	}
	if sug.Behavior != "allow" {
		t.Errorf("Expected suggestion behavior 'allow', got '%s'", sug.Behavior)
	}
	if len(sug.Rules) != 1 {
		t.Fatalf("Expected 1 rule, got %d", len(sug.Rules))
	}
	if sug.Rules[0].ToolName != "Write" {
		t.Errorf("Expected rule ToolName 'Write', got '%s'", sug.Rules[0].ToolName)
	}
	if sug.Rules[0].RuleContent != "/etc/hosts" {
		t.Errorf("Expected rule RuleContent '/etc/hosts', got '%s'", sug.Rules[0].RuleContent)
	}
}

// TestPermissionUpdateFromMap tests the permissionUpdateFromMap helper.
func TestPermissionUpdateFromMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected PermissionUpdate
	}{
		{
			name: "addRules with rules",
			input: map[string]interface{}{
				"type":     "addRules",
				"behavior": "allow",
				"rules": []interface{}{
					map[string]interface{}{
						"toolName":    "Bash",
						"ruleContent": "npm test",
					},
				},
			},
			expected: PermissionUpdate{
				Type:     "addRules",
				Behavior: "allow",
				Rules: []PermissionRuleValue{
					{ToolName: "Bash", RuleContent: "npm test"},
				},
			},
		},
		{
			name: "setMode",
			input: map[string]interface{}{
				"type": "setMode",
				"mode": "bypassPermissions",
			},
			expected: PermissionUpdate{
				Type: "setMode",
				Mode: "bypassPermissions",
			},
		},
		{
			name: "addDirectories",
			input: map[string]interface{}{
				"type":        "addDirectories",
				"directories": []interface{}{"/tmp", "/var"},
				"destination": "localSettings",
			},
			expected: PermissionUpdate{
				Type:        "addDirectories",
				Directories: []string{"/tmp", "/var"},
				Destination: "localSettings",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := permissionUpdateFromMap(tt.input)
			if result.Type != tt.expected.Type {
				t.Errorf("Expected Type '%s', got '%s'", tt.expected.Type, result.Type)
			}
			if result.Behavior != tt.expected.Behavior {
				t.Errorf("Expected Behavior '%s', got '%s'", tt.expected.Behavior, result.Behavior)
			}
			if result.Mode != tt.expected.Mode {
				t.Errorf("Expected Mode '%s', got '%s'", tt.expected.Mode, result.Mode)
			}
			if result.Destination != tt.expected.Destination {
				t.Errorf("Expected Destination '%s', got '%s'", tt.expected.Destination, result.Destination)
			}
			if len(result.Rules) != len(tt.expected.Rules) {
				t.Fatalf("Expected %d rules, got %d", len(tt.expected.Rules), len(result.Rules))
			}
			for i, r := range result.Rules {
				if r.ToolName != tt.expected.Rules[i].ToolName {
					t.Errorf("Rule %d: Expected ToolName '%s', got '%s'", i, tt.expected.Rules[i].ToolName, r.ToolName)
				}
				if r.RuleContent != tt.expected.Rules[i].RuleContent {
					t.Errorf("Rule %d: Expected RuleContent '%s', got '%s'", i, tt.expected.Rules[i].RuleContent, r.RuleContent)
				}
			}
			if len(result.Directories) != len(tt.expected.Directories) {
				t.Fatalf("Expected %d directories, got %d", len(tt.expected.Directories), len(result.Directories))
			}
			for i, d := range result.Directories {
				if d != tt.expected.Directories[i] {
					t.Errorf("Dir %d: Expected '%s', got '%s'", i, tt.expected.Directories[i], d)
				}
			}
		})
	}
}

// TestHandleCanUseToolSuggestionsRoundtrip tests that permission suggestions are
// deserialized and can be echoed back in the response.
func TestHandleCanUseToolSuggestionsRoundtrip(t *testing.T) {
	mockTrans := newMockTransport()

	var receivedSuggestions []PermissionUpdate
	canUseTool := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		receivedSuggestions = ctx.Suggestions
		// Echo back the suggestions as updated_permissions
		return &PermissionResultAllow{
			UpdatedInput:       input,
			UpdatedPermissions: ctx.Suggestions,
		}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
	})

	request := map[string]interface{}{
		"tool_name": "Bash",
		"input":     map[string]interface{}{"command": "ls"},
		"permission_suggestions": []interface{}{
			map[string]interface{}{
				"type":     "addRules",
				"behavior": "allow",
				"rules": []interface{}{
					map[string]interface{}{
						"toolName":    "Bash",
						"ruleContent": "ls",
					},
				},
				"destination": "localSettings",
			},
		},
	}

	response, err := query.handleCanUseTool(context.Background(), request)
	if err != nil {
		t.Fatalf("handleCanUseTool failed: %v", err)
	}

	// Verify suggestions were properly deserialized
	if len(receivedSuggestions) != 1 {
		t.Fatalf("Expected 1 suggestion, got %d", len(receivedSuggestions))
	}
	if receivedSuggestions[0].Destination != "localSettings" {
		t.Errorf("Expected Destination 'localSettings', got '%s'", receivedSuggestions[0].Destination)
	}

	// Verify response includes updatedPermissions
	updatedPerms, ok := response["updatedPermissions"].([]PermissionUpdate)
	if !ok {
		t.Fatalf("Expected updatedPermissions to be []PermissionUpdate, got %T", response["updatedPermissions"])
	}
	if len(updatedPerms) != 1 {
		t.Fatalf("Expected 1 updated permission, got %d", len(updatedPerms))
	}
	if updatedPerms[0].Type != "addRules" {
		t.Errorf("Expected type 'addRules', got '%s'", updatedPerms[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Tests for ProcessError after error result (Python SDK v0.1.77, PR #918)
// ---------------------------------------------------------------------------

// collectErrors reads errors from the channel with a timeout to avoid blocking
// indefinitely when q.errors is never closed (by design).
func collectErrors(ch <-chan error, timeout time.Duration) []error {
	var errs []error
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case err, ok := <-ch:
			if !ok {
				return errs
			}
			errs = append(errs, err)
		case <-timer.C:
			return errs
		}
	}
}

// TestProcessErrorAfterErrorResultUsesResultErrorText verifies that when the
// CLI emits a result with is_error=true followed by a ProcessError, the error
// text is replaced with the structured error from the result.
func TestProcessErrorAfterErrorResultUsesResultErrorText(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	// Send error result
	tr.messages <- map[string]interface{}{
		"type":      "result",
		"subtype":   "error_max_turns",
		"is_error":  true,
		"num_turns": float64(60),
		"errors":    []interface{}{"Reached maximum number of turns (60)"},
	}
	// Close messages channel to end the read loop's message phase
	close(tr.messages)
	// Send ProcessError (will be read after messages close)
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	// Read messages
	msgCount := 0
	for msg := range q.RawMessages() {
		msgCount++
		if msg["subtype"] != "error_max_turns" {
			t.Errorf("Expected subtype error_max_turns, got %v", msg["subtype"])
		}
	}
	if msgCount != 1 {
		t.Errorf("Expected 1 message, got %d", msgCount)
	}

	// Read errors (with timeout since q.errors is never closed by design)
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Errorf("Expected 1 error, got %d", len(errs))
	}
	if len(errs) > 0 {
		if !strings.Contains(errs[0].Error(), "Claude Code returned an error result") {
			t.Errorf("Expected actionable error message, got: %v", errs[0])
		}
		if !strings.Contains(errs[0].Error(), "Reached maximum number of turns (60)") {
			t.Errorf("Expected error to contain turn count, got: %v", errs[0])
		}
	}
}

// TestProcessErrorAfterErrorResultFallsBackToSubtype verifies that when the
// result has no errors array, the improved message falls back to the subtype.
func TestProcessErrorAfterErrorResultFallsBackToSubtype(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	tr.messages <- map[string]interface{}{
		"type":     "result",
		"subtype":  "error_during_execution",
		"is_error": true,
	}
	close(tr.messages)
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	for range q.RawMessages() {
	}
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "error_during_execution") {
		t.Errorf("Expected fallback to subtype, got: %v", errs[0])
	}
}

// TestProcessErrorAfterErrorResultJoinsMultipleErrors verifies that multiple
// errors in the errors array are joined with semicolons.
func TestProcessErrorAfterErrorResultJoinsMultipleErrors(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	tr.messages <- map[string]interface{}{
		"type":     "result",
		"subtype":  "error_during_execution",
		"is_error": true,
		"errors":   []interface{}{"Error one", "Error two"},
	}
	close(tr.messages)
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	for range q.RawMessages() {
	}
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "Error one; Error two") {
		t.Errorf("Expected joined errors, got: %v", errs[0])
	}
}

// TestProcessErrorWithoutResultStillSurfaces verifies that a ProcessError
// without any prior result still surfaces as-is.
func TestProcessErrorWithoutResultStillSurfaces(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	close(tr.messages)
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	for range q.RawMessages() {
	}
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs))
	}
	if strings.Contains(errs[0].Error(), "Claude Code returned an error result") {
		t.Errorf("Unexpected error replacement without prior result: %v", errs[0])
	}
}

// TestProcessErrorAfterSuccessResultStillSurfaces verifies that a ProcessError
// after a successful result (is_error=false) still surfaces as-is.
func TestProcessErrorAfterSuccessResultStillSurfaces(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	tr.messages <- map[string]interface{}{
		"type":     "result",
		"subtype":  "success",
		"is_error": false,
	}
	close(tr.messages)
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	for range q.RawMessages() {
	}
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs))
	}
	if strings.Contains(errs[0].Error(), "Claude Code returned an error result") {
		t.Errorf("Unexpected error replacement after success result: %v", errs[0])
	}
}

// TestNonResultMessageResetsErrorResultText verifies that non-result messages
// (other than session_state_changed) reset the error result text tracker.
func TestNonResultMessageResetsErrorResultText(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	// Send error result first
	tr.messages <- map[string]interface{}{
		"type":     "result",
		"subtype":  "error_max_turns",
		"is_error": true,
		"errors":   []interface{}{"Turn limit reached"},
	}
	// Send an assistant message (resets tracker)
	tr.messages <- map[string]interface{}{
		"type": "assistant",
	}
	close(tr.messages)
	// Send ProcessError -- should NOT be replaced since tracker was reset
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	for range q.RawMessages() {
	}
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs))
	}
	if strings.Contains(errs[0].Error(), "Claude Code returned an error result") {
		t.Errorf("Error should not have been replaced after non-result message: %v", errs[0])
	}
}

// TestSessionStateChangedDoesNotResetErrorResultText verifies that
// system messages with subtype session_state_changed do NOT reset the
// error result text tracker.
func TestSessionStateChangedDoesNotResetErrorResultText(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: false,
	})
	q.Start()

	// Send error result
	tr.messages <- map[string]interface{}{
		"type":     "result",
		"subtype":  "error_max_turns",
		"is_error": true,
		"errors":   []interface{}{"Turn limit"},
	}
	// session_state_changed should NOT reset
	tr.messages <- map[string]interface{}{
		"type":    "system",
		"subtype": "session_state_changed",
	}
	close(tr.messages)
	tr.errors <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
	close(tr.errors)

	for range q.RawMessages() {
	}
	errs := collectErrors(q.Errors(), 200*time.Millisecond)
	if len(errs) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "Claude Code returned an error result") {
		t.Errorf("Expected error replacement after session_state_changed: %v", errs[0])
	}
}
