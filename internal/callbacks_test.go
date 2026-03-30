package internal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Uses mockTransport from query_test.go

func TestToolPermissionCallback_Allow(t *testing.T) {
	callbackInvoked := false
	allowCallback := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		callbackInvoked = true
		if toolName != "TestTool" {
			t.Errorf("Expected toolName 'TestTool', got '%s'", toolName)
		}
		if val, ok := input["param"].(string); !ok || val != "value" {
			t.Errorf("Expected input param 'value', got %v", input["param"])
		}
		return &PermissionResultAllow{}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		CanUseTool:      allowCallback,
	})

	request := map[string]interface{}{
		"request_id": "test-1",
		"request": map[string]interface{}{
			"subtype":                "can_use_tool",
			"tool_name":              "TestTool",
			"input":                  map[string]interface{}{"param": "value"},
			"permission_suggestions": []interface{}{},
		},
	}

	// Direct call to private handler
	q.handleControlRequest(context.Background(), request)

	if !callbackInvoked {
		t.Error("Callback was not invoked")
	}

	written := mt.getWritten()
	if len(written) != 1 {
		t.Fatalf("Expected 1 written message, got %d", len(written))
	}

	var response map[string]interface{}
	if err := json.Unmarshal([]byte(written[0]), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})
	if respBody["behavior"] != "allow" {
		t.Errorf("Expected behavior 'allow', got '%v'", respBody["behavior"])
	}
}

func TestToolPermissionCallback_Deny(t *testing.T) {
	denyCallback := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultDeny{Message: "Security policy violation"}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		CanUseTool:      denyCallback,
	})

	request := map[string]interface{}{
		"request_id": "test-2",
		"request": map[string]interface{}{
			"subtype":                "can_use_tool",
			"tool_name":              "DangerousTool",
			"input":                  map[string]interface{}{"command": "rm -rf /"},
			"permission_suggestions": []interface{}{"deny"},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	if len(written) != 1 {
		t.Fatalf("Expected 1 written message, got %d", len(written))
	}

	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})

	if respBody["behavior"] != "deny" {
		t.Errorf("Expected behavior 'deny', got '%v'", respBody["behavior"])
	}
	if respBody["message"] != "Security policy violation" {
		t.Errorf("Expected message 'Security policy violation', got '%v'", respBody["message"])
	}
}

func TestToolPermissionCallback_ModifyInput(t *testing.T) {
	modifyCallback := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		newInput := make(map[string]interface{})
		for k, v := range input {
			newInput[k] = v
		}
		newInput["safe_mode"] = true
		return &PermissionResultAllow{UpdatedInput: newInput}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		CanUseTool:      modifyCallback,
	})

	request := map[string]interface{}{
		"request_id": "test-3",
		"request": map[string]interface{}{
			"subtype":   "can_use_tool",
			"tool_name": "WriteTool",
			"input":     map[string]interface{}{"file_path": "/etc/passwd"},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})

	if respBody["behavior"] != "allow" {
		t.Errorf("Expected behavior 'allow', got '%v'", respBody["behavior"])
	}
	updatedInput := respBody["updatedInput"].(map[string]interface{})
	if updatedInput["safe_mode"] != true {
		t.Error("Expected safe_mode: true in updatedInput")
	}
}

func TestToolPermissionCallback_Exception(t *testing.T) {
	errorCallback := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return nil, errors.New("Callback error")
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		CanUseTool:      errorCallback,
	})

	request := map[string]interface{}{
		"request_id": "test-5",
		"request": map[string]interface{}{
			"subtype":   "can_use_tool",
			"tool_name": "TestTool",
			"input":     map[string]interface{}{},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	resp := response["response"].(map[string]interface{})

	if resp["subtype"] != "error" {
		t.Errorf("Expected subtype 'error', got '%v'", resp["subtype"])
	}
	if resp["error"] != "Callback error" {
		t.Errorf("Expected error 'Callback error', got '%v'", resp["error"])
	}
}

func TestHookExecution(t *testing.T) {
	var hookCalls []map[string]interface{}
	testHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		hookCalls = append(hookCalls, map[string]interface{}{
			"input":       input,
			"tool_use_id": toolUseID,
		})
		return HookOutput{
			HookSpecificOutput: map[string]interface{}{"processed": true},
		}, nil
	}

	mt := newMockTransport()

	// Create hooks map
	hooks := map[string][]HookMatcherInternal{
		"tool_use_start": {{
			Matcher: "{\"tool\":\"TestTool\"}",
			Hooks:   []HookCallback{testHook},
		}},
	}

	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		Hooks:           hooks,
	})

	// Manually register for test because Initialize isn't called
	callbackID := "test_hook_0"
	q.hookCallbacks[callbackID] = testHook

	request := map[string]interface{}{
		"request_id": "test-hook-1",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input":       map[string]interface{}{"test": "data"},
			"tool_use_id": "tool-123",
		},
	}

	q.handleControlRequest(context.Background(), request)

	if len(hookCalls) != 1 {
		t.Fatalf("Expected 1 hook call, got %d", len(hookCalls))
	}
	// Verify input (checking map content would require type assertion/loop)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})

	hookOutput := respBody["hookSpecificOutput"].(map[string]interface{})
	if hookOutput["processed"] != true {
		t.Error("Expected processed: true")
	}
}

func TestHookOutputFields(t *testing.T) {
	testHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		cont := true
		return HookOutput{
			Continue:       &cont,
			SuppressOutput: false,
			StopReason:     "Test stop reason",
			Decision:       "block",
			SystemMessage:  "Test system message",
			Reason:         "Test reason",
			HookSpecificOutput: map[string]interface{}{
				"hookEventName": "PreToolUse",
				"updatedInput":  map[string]interface{}{"modified": "input"},
			},
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_comprehensive_hook"
	q.hookCallbacks[callbackID] = testHook

	request := map[string]interface{}{
		"request_id": "test-comprehensive",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input":       map[string]interface{}{},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})

	if respBody["continue"] != true {
		t.Error("Expected continue: true")
	}
	if respBody["stopReason"] != "Test stop reason" {
		t.Errorf("Expected stopReason, got %v", respBody["stopReason"])
	}
	if respBody["decision"] != "block" {
		t.Errorf("Expected decision block, got %v", respBody["decision"])
	}

	hs := respBody["hookSpecificOutput"].(map[string]interface{})
	if hs["hookEventName"] != "PreToolUse" {
		t.Errorf("Expected hookEventName PreToolUse")
	}
}

func TestAsyncHookOutput(t *testing.T) {
	testHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{
			Async:        true,
			AsyncTimeout: 5000,
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_async_hook"
	q.hookCallbacks[callbackID] = testHook

	request := map[string]interface{}{
		"request_id": "test-async",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input":       map[string]interface{}{},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})

	if respBody["async"] != true {
		t.Error("Expected async: true")
	}
}

func TestHookEventCallbacks(t *testing.T) {
	// Notification
	notifHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		if input.HookEventName != "Notification" {
			t.Errorf("Expected HookEventName Notification, got %s", input.HookEventName)
		}
		return HookOutput{
			HookSpecificOutput: map[string]interface{}{
				"hookEventName":     "Notification",
				"additionalContext": "Notification processed",
			},
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_notif"
	q.hookCallbacks[callbackID] = notifHook

	request := map[string]interface{}{
		"request_id": "req-1",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input": map[string]interface{}{
				"hook_event_name": "Notification",
				"message":         "Task completed",
			},
		},
	}

	q.handleControlRequest(context.Background(), request)
	// We verify output at least
	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})
	hs := respBody["hookSpecificOutput"].(map[string]interface{})
	if hs["hookEventName"] != "Notification" {
		t.Errorf("Expected Notification")
	}
}

func TestHookEventCallbacks_PermissionRequest(t *testing.T) {
	permHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{
			HookSpecificOutput: map[string]interface{}{
				"hookEventName": "PermissionRequest",
				"decision":      map[string]interface{}{"type": "allow"},
			},
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_perm_req"
	q.hookCallbacks[callbackID] = permHook

	request := map[string]interface{}{
		"request_id": "req-2",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input": map[string]interface{}{
				"hook_event_name": "PermissionRequest",
				"tool_name":       "Bash",
				"tool_input":      map[string]interface{}{"command": "ls"},
			},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})
	hs := respBody["hookSpecificOutput"].(map[string]interface{})

	if hs["hookEventName"] != "PermissionRequest" {
		t.Errorf("Expected PermissionRequest")
	}
	decision := hs["decision"].(map[string]interface{})
	if decision["type"] != "allow" {
		t.Errorf("Expected decision allow")
	}
}

func TestHookEventCallbacks_SubagentStart(t *testing.T) {
	subHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{
			HookSpecificOutput: map[string]interface{}{
				"hookEventName":     "SubagentStart",
				"additionalContext": "Subagent approved",
			},
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_subagent"
	q.hookCallbacks[callbackID] = subHook

	request := map[string]interface{}{
		"request_id": "req-3",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input": map[string]interface{}{
				"hook_event_name": "SubagentStart",
				"agent_id":        "agent-42",
			},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})
	hs := respBody["hookSpecificOutput"].(map[string]interface{})

	if hs["hookEventName"] != "SubagentStart" {
		t.Errorf("Expected SubagentStart")
	}
	if hs["additionalContext"] != "Subagent approved" {
		t.Errorf("Expected additionalContext 'Subagent approved'")
	}
}

func TestHookEventCallbacks_PostToolUse(t *testing.T) {
	postHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{
			HookSpecificOutput: map[string]interface{}{
				"hookEventName":        "PostToolUse",
				"updatedMCPToolOutput": map[string]interface{}{"result": "modified output"},
			},
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_post_tool"
	q.hookCallbacks[callbackID] = postHook

	request := map[string]interface{}{
		"request_id": "req-4",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input": map[string]interface{}{
				"hook_event_name": "PostToolUse",
				"tool_name":       "mcp_tool",
			},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})
	hs := respBody["hookSpecificOutput"].(map[string]interface{})

	if hs["hookEventName"] != "PostToolUse" {
		t.Errorf("Expected PostToolUse")
	}
	updatedOutput := hs["updatedMCPToolOutput"].(map[string]interface{})
	if updatedOutput["result"] != "modified output" {
		t.Errorf("Expected updated output 'modified output'")
	}
}

func TestHookEventCallbacks_PreToolUse(t *testing.T) {
	preHook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{
			HookSpecificOutput: map[string]interface{}{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
				"additionalContext":  "Extra context",
			},
		}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{Transport: mt, IsStreamingMode: true})
	callbackID := "test_pre_tool"
	q.hookCallbacks[callbackID] = preHook

	request := map[string]interface{}{
		"request_id": "req-5",
		"request": map[string]interface{}{
			"subtype":     "hook_callback",
			"callback_id": callbackID,
			"input": map[string]interface{}{
				"hook_event_name": "PreToolUse",
				"tool_name":       "Bash",
			},
		},
	}

	q.handleControlRequest(context.Background(), request)

	written := mt.getWritten()
	var response map[string]interface{}
	json.Unmarshal([]byte(written[0]), &response)
	respBody := response["response"].(map[string]interface{})["response"].(map[string]interface{})
	hs := respBody["hookSpecificOutput"].(map[string]interface{})

	if hs["hookEventName"] != "PreToolUse" {
		t.Errorf("Expected PreToolUse")
	}
	if hs["additionalContext"] != "Extra context" {
		t.Errorf("Expected 'Extra context'")
	}
}

// TestToolPermissionCallback_ReceivesToolUseIDAndAgentID tests that ToolPermissionContext
// receives tool_use_id and agent_id from the request.
func TestToolPermissionCallback_ReceivesToolUseIDAndAgentID(t *testing.T) {
	var receivedCtx ToolPermissionContext
	callback := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		receivedCtx = ctx
		return &PermissionResultAllow{}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		CanUseTool:      callback,
	})

	request := map[string]interface{}{
		"request_id": "test-tuid-1",
		"request": map[string]interface{}{
			"subtype":                "can_use_tool",
			"tool_name":              "Bash",
			"input":                  map[string]interface{}{"command": "ls"},
			"permission_suggestions": []interface{}{},
			"tool_use_id":            "toolu_abc123",
			"agent_id":               "agent_def456",
		},
	}

	q.handleControlRequest(context.Background(), request)

	if receivedCtx.ToolUseID != "toolu_abc123" {
		t.Errorf("Expected ToolUseID 'toolu_abc123', got '%s'", receivedCtx.ToolUseID)
	}
	if receivedCtx.AgentID != "agent_def456" {
		t.Errorf("Expected AgentID 'agent_def456', got '%s'", receivedCtx.AgentID)
	}
}

// TestToolPermissionCallback_MissingAgentID tests when agent_id is absent.
func TestToolPermissionCallback_MissingAgentID(t *testing.T) {
	var receivedCtx ToolPermissionContext
	callback := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		receivedCtx = ctx
		return &PermissionResultAllow{}, nil
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		CanUseTool:      callback,
	})

	request := map[string]interface{}{
		"request_id": "test-tuid-2",
		"request": map[string]interface{}{
			"subtype":                "can_use_tool",
			"tool_name":              "Bash",
			"input":                  map[string]interface{}{"command": "ls"},
			"permission_suggestions": []interface{}{},
			"tool_use_id":            "toolu_xyz789",
		},
	}

	q.handleControlRequest(context.Background(), request)

	if receivedCtx.ToolUseID != "toolu_xyz789" {
		t.Errorf("Expected ToolUseID 'toolu_xyz789', got '%s'", receivedCtx.ToolUseID)
	}
	if receivedCtx.AgentID != "" {
		t.Errorf("Expected empty AgentID, got '%s'", receivedCtx.AgentID)
	}
}
