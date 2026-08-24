package internal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestHandleCanUseToolWithoutCallback verifies a tool permission request
// fails cleanly when no canUseTool callback is configured.
func TestHandleCanUseToolWithoutCallback(t *testing.T) {
	query := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
	})

	_, err := query.handleCanUseTool(context.Background(), map[string]interface{}{
		"tool_name": "Bash",
		"input":     map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error when canUseTool callback is not provided")
	}
	if !strings.Contains(err.Error(), "canUseTool callback is not provided") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestHandleCanUseToolDenyInterrupt verifies a deny result with Interrupt set
// carries the interrupt flag in the wire response.
func TestHandleCanUseToolDenyInterrupt(t *testing.T) {
	query := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			return &PermissionResultDeny{Message: "stop now", Interrupt: true}, nil
		},
	})

	response, err := query.handleCanUseTool(context.Background(), map[string]interface{}{
		"tool_name": "Bash",
		"input":     map[string]interface{}{"command": "rm -rf /"},
	})
	if err != nil {
		t.Fatalf("handleCanUseTool failed: %v", err)
	}
	if response["behavior"] != "deny" {
		t.Errorf("behavior: got %v, want deny", response["behavior"])
	}
	if response["message"] != "stop now" {
		t.Errorf("message: got %v, want 'stop now'", response["message"])
	}
	if response["interrupt"] != true {
		t.Errorf("interrupt: got %v, want true", response["interrupt"])
	}
}

// TestHandleHookCallbackUnknownID verifies a hook callback request for an
// unregistered callback ID is rejected.
func TestHandleHookCallbackUnknownID(t *testing.T) {
	query := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
	})

	_, err := query.handleHookCallback(context.Background(), map[string]interface{}{
		"callback_id": "hook_missing",
		"input":       map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for unknown callback ID")
	}
	if !strings.Contains(err.Error(), "no hook callback found for ID: hook_missing") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestHandleHookCallbackInputMapping verifies every supported input field is
// mapped onto the HookInput handed to the callback.
func TestHandleHookCallbackInputMapping(t *testing.T) {
	query := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
	})

	var got HookInput
	var gotToolUseID string
	query.hookCallbacks["hook_full"] = func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		got = input
		gotToolUseID = toolUseID
		return HookOutput{}, nil
	}

	_, err := query.handleHookCallback(context.Background(), map[string]interface{}{
		"callback_id": "hook_full",
		"tool_use_id": "tool-outer",
		"input": map[string]interface{}{
			"hook_event_name":       "PostToolUse",
			"session_id":            "sess-1",
			"transcript_path":       "/tmp/t.jsonl",
			"cwd":                   "/work",
			"tool_name":             "Bash",
			"tool_input":            map[string]interface{}{"command": "ls"},
			"tool_response":         map[string]interface{}{"stdout": "ok"},
			"prompt":                "do it",
			"permission_mode":       "acceptEdits",
			"tool_use_id":           "tool-inner",
			"error":                 "boom",
			"is_interrupt":          true,
			"stop_hook_active":      true,
			"agent_id":              "agent-9",
			"agent_transcript_path": "/tmp/agent.jsonl",
			"agent_type":            "reviewer",
			"trigger":               "user",
			"custom_instructions":   "be nice",
			"message":               "hello",
			"title":                 "Greeting",
			"notification_type":     "info",
			"permission_suggestions": []interface{}{
				map[string]interface{}{"type": "addRules"},
				"not-a-map",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleHookCallback failed: %v", err)
	}

	if gotToolUseID != "tool-outer" {
		t.Errorf("toolUseID: got %q, want tool-outer", gotToolUseID)
	}
	checks := map[string]struct {
		got  interface{}
		want interface{}
	}{
		"HookEventName":       {got.HookEventName, "PostToolUse"},
		"SessionID":           {got.SessionID, "sess-1"},
		"TranscriptPath":      {got.TranscriptPath, "/tmp/t.jsonl"},
		"CWD":                 {got.CWD, "/work"},
		"ToolName":            {got.ToolName, "Bash"},
		"Prompt":              {got.Prompt, "do it"},
		"PermissionMode":      {got.PermissionMode, "acceptEdits"},
		"ToolUseID":           {got.ToolUseID, "tool-inner"},
		"Error":               {got.Error, "boom"},
		"IsInterrupt":         {got.IsInterrupt, true},
		"StopHookActive":      {got.StopHookActive, true},
		"AgentID":             {got.AgentID, "agent-9"},
		"AgentTranscriptPath": {got.AgentTranscriptPath, "/tmp/agent.jsonl"},
		"AgentType":           {got.AgentType, "reviewer"},
		"Trigger":             {got.Trigger, "user"},
		"CustomInstructions":  {got.CustomInstructions, "be nice"},
		"Message":             {got.Message, "hello"},
		"Title":               {got.Title, "Greeting"},
		"NotificationType":    {got.NotificationType, "info"},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", field, c.got, c.want)
		}
	}

	if got.ToolInput == nil || got.ToolInput["command"] != "ls" {
		t.Errorf("ToolInput: got %v", got.ToolInput)
	}
	toolResponse, ok := got.ToolResponse.(map[string]interface{})
	if !ok || toolResponse["stdout"] != "ok" {
		t.Errorf("ToolResponse: got %v", got.ToolResponse)
	}
	if len(got.PermissionSuggestions) != 1 || got.PermissionSuggestions[0]["type"] != "addRules" {
		t.Errorf("PermissionSuggestions: got %v", got.PermissionSuggestions)
	}
}

// TestHandleHookCallbackErrorPropagates verifies an error returned by the
// hook callback propagates out of the handler.
func TestHandleHookCallbackErrorPropagates(t *testing.T) {
	query := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
	})
	query.hookCallbacks["hook_failing"] = func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{}, errors.New("hook exploded")
	}

	_, err := query.handleHookCallback(context.Background(), map[string]interface{}{
		"callback_id": "hook_failing",
		"input":       map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected hook error to propagate")
	}
	if !strings.Contains(err.Error(), "hook exploded") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestHandleMCPMessageMissingFields verifies MCP requests without a server
// name or message payload are rejected.
func TestHandleMCPMessageMissingFields(t *testing.T) {
	query := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
	})

	if _, err := query.handleMCPMessage(map[string]interface{}{
		"message": map[string]interface{}{"jsonrpc": "2.0", "id": 1},
	}); err == nil {
		t.Error("expected error for missing server_name")
	}

	if _, err := query.handleMCPMessage(map[string]interface{}{
		"server_name": "srv",
	}); err == nil {
		t.Error("expected error for missing message")
	}
}

// TestBridgeHandleFrameWithoutMethodOrID verifies a JSON-RPC frame carrying
// neither a method nor a valid request ID is dropped without a reply.
func TestBridgeHandleFrameWithoutMethodOrID(t *testing.T) {
	server := &MCPServer{Name: "s", Version: "1.0.0"}
	bridge := newTestBridge(t, "s", server)

	if resp := bridge.Handle(map[string]interface{}{"foo": "bar"}); resp != nil {
		t.Errorf("expected nil reply for methodless, id-less frame, got %v", resp)
	}
}

// TestJSONDeepEqualArrayMismatch verifies array comparison reports inequality
// when elements differ and equality when numeric forms match.
func TestJSONDeepEqualArrayMismatch(t *testing.T) {
	if jsonDeepEqual([]interface{}{1.0, 2.0}, []interface{}{1.0, 3.0}) {
		t.Error("expected arrays with differing elements to be unequal")
	}
	if !jsonDeepEqual([]interface{}{1, "a"}, []interface{}{1.0, "a"}) {
		t.Error("expected numerically equal arrays to be equal")
	}
}
