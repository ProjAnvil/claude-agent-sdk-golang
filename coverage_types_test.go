package claude

import "testing"

// TestMessageMarkerMethods exercises every messageMarker implementation
// through the Message interface. The markers are empty methods that exist so
// only the intended types satisfy Message; this verifies each concrete type
// is wired into the interface and the method set resolves without panicking.
func TestMessageMarkerMethods(t *testing.T) {
	messages := []Message{
		&UserMessage{},
		&AssistantMessage{},
		&SystemMessage{},
		&ResultMessage{},
		&StreamEvent{},
		&RateLimitEvent{},
		&ConversationResetMessage{},
		&TaskStartedMessage{},
		&TaskProgressMessage{},
		&TaskNotificationMessage{},
		&TaskUpdatedMessage{},
		&MirrorErrorMessage{},
		&HookEventMessage{},
	}

	for i, m := range messages {
		if m == nil {
			t.Errorf("message %d: interface value is nil", i)
			continue
		}
		// Calls the marker through the interface; a missing implementation
		// would fail to compile, a bad receiver would panic here.
		m.messageMarker()
	}
}

// TestContentBlockMarkerMethods exercises every contentBlockMarker
// implementation through the ContentBlock interface.
func TestContentBlockMarkerMethods(t *testing.T) {
	blocks := []ContentBlock{
		&TextBlock{},
		&ThinkingBlock{},
		&ToolUseBlock{},
		&ToolResultBlock{},
		&ServerToolUseBlock{},
		&ServerToolResultBlock{},
	}

	for i, b := range blocks {
		if b == nil {
			t.Errorf("content block %d: interface value is nil", i)
			continue
		}
		b.contentBlockMarker()
	}
}

// TestMCPServerConfigMarkerMethods exercises every mcpServerConfigMarker
// implementation through the MCPServerConfig interface.
func TestMCPServerConfigMarkerMethods(t *testing.T) {
	configs := []MCPServerConfig{
		&MCPStdioServerConfig{Command: "srv"},
		&MCPSSEServerConfig{Type: "sse", URL: "http://localhost/sse"},
		&MCPHTTPServerConfig{Type: "http", URL: "http://localhost/http"},
		&MCPSdkServerConfig{Type: "sdk", Name: "inproc"},
	}

	for i, c := range configs {
		if c == nil {
			t.Errorf("mcp server config %d: interface value is nil", i)
			continue
		}
		c.mcpServerConfigMarker()
	}
}

// TestPermissionResultMarkerMethods exercises both permissionResultMarker
// implementations through the PermissionResult interface.
func TestPermissionResultMarkerMethods(t *testing.T) {
	results := []PermissionResult{
		&PermissionResultAllow{Behavior: "allow"},
		&PermissionResultDeny{Behavior: "deny"},
	}

	for i, r := range results {
		if r == nil {
			t.Errorf("permission result %d: interface value is nil", i)
			continue
		}
		r.permissionResultMarker()
	}
}

// TestMcpServerStatusConfigMarkerMethods exercises both
// mcpServerStatusConfigMarker implementations through the
// McpServerStatusConfig interface.
func TestMcpServerStatusConfigMarkerMethods(t *testing.T) {
	configs := []McpServerStatusConfig{
		&McpSdkServerConfigStatus{Type: "sdk", Name: "inproc"},
		&McpClaudeAIProxyServerConfig{Type: "claudeai-proxy", URL: "http://localhost", ID: "id-1"},
	}

	for i, c := range configs {
		if c == nil {
			t.Errorf("mcp server status config %d: interface value is nil", i)
			continue
		}
		c.mcpServerStatusConfigMarker()
	}
}
