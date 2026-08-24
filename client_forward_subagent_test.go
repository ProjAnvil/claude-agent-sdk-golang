package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// TestConnectCanUseToolMutuallyExclusive verifies Connect rejects the
// combination of CanUseTool and PermissionPromptToolName before any
// transport is created, mirroring the Python _configure_can_use_tool
// validation (#1204).
func TestConnectCanUseToolMutuallyExclusive(t *testing.T) {
	noop := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{}, nil
	}
	client := NewClient(&ClaudeAgentOptions{
		CanUseTool:               noop,
		PermissionPromptToolName: "CustomTool",
	})
	factoryCalled := false
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		factoryCalled = true
		return newMockTransport(), nil
	}

	err := client.Connect(context.Background(), "Hello")
	if err == nil {
		t.Fatal("Expected Connect to reject can_use_tool + permission_prompt_tool_name")
	}
	if !strings.Contains(err.Error(), "can_use_tool callback cannot be used with permission_prompt_tool_name") {
		t.Errorf("Expected mutual-exclusion error, got: %v", err)
	}
	if factoryCalled {
		t.Error("Transport must not be created when option validation fails")
	}
}

// TestConnectCanUseToolSetsStdioPermissionPrompt verifies Connect routes
// permission prompts over the control protocol when CanUseTool is set
// (permission_prompt_tool_name="stdio"), mirroring the Python
// _configure_can_use_tool (#1204).
func TestConnectCanUseToolSetsStdioPermissionPrompt(t *testing.T) {
	noop := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{}, nil
	}
	client := NewClient(&ClaudeAgentOptions{CanUseTool: noop})

	mockT := createMockTransport()
	var capturedOpts *transport.TransportOptions
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if capturedOpts == nil || capturedOpts.PermissionPromptToolName != "stdio" {
		t.Errorf("Expected PermissionPromptToolName=stdio, got %v", capturedOpts)
	}
}

// TestForwardSubagentTextSentInInitialize mirrors the Python streaming-client
// test_forward_subagent_text_sent_in_initialize (#1206):
// ClaudeAgentOptions.ForwardSubagentText is sent as the forwardSubagentText
// initialize capability; omitted when false.
func TestForwardSubagentTextSentInInitialize(t *testing.T) {
	initializeRequestFor := func(t *testing.T, options *ClaudeAgentOptions) map[string]interface{} {
		t.Helper()
		var writes []string
		mockT := newMockTransport()
		handleInitialization(mockT, &writes)

		client := NewClient(options)
		client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
			return mockT, nil
		}
		if err := client.Connect(context.Background()); err != nil {
			t.Fatalf("Connect failed: %v", err)
		}
		client.Close()

		var requests []map[string]interface{}
		for _, w := range writes {
			if strings.Contains(w, `"initialize"`) {
				var payload map[string]interface{}
				if err := json.Unmarshal([]byte(w), &payload); err == nil {
					requests = append(requests, payload)
				}
			}
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 initialize request, got %d", len(requests))
		}
		inner, _ := requests[0]["request"].(map[string]interface{})
		return inner
	}

	enabled := initializeRequestFor(t, &ClaudeAgentOptions{ForwardSubagentText: true})
	if val, ok := enabled["forwardSubagentText"]; !ok || val != true {
		t.Errorf("Expected forwardSubagentText=true, got %v (ok=%v)", val, ok)
	}

	def := initializeRequestFor(t, nil)
	if _, ok := def["forwardSubagentText"]; ok {
		t.Error("Expected forwardSubagentText to be absent by default")
	}
}
