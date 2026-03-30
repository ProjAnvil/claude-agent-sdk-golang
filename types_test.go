package claude

import (
	"encoding/json"
	"testing"
)

// TestTextBlockJSONRoundTrip tests JSON marshaling and unmarshaling for TextBlock.
func TestTextBlockJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"simple text", "Hello, world!"},
		{"empty text", ""},
		{"unicode text", "你好世界 🌍"},
		{"multiline text", "Line 1\nLine 2\nLine 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &TextBlock{Text: tt.text}

			// Marshal
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// Verify type field
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("Unmarshal to map failed: %v", err)
			}
			if raw["type"] != "text" {
				t.Errorf("Expected type='text', got %v", raw["type"])
			}
			if raw["text"] != tt.text {
				t.Errorf("Expected text=%q, got %v", tt.text, raw["text"])
			}

			// Parse back
			parsed, err := ParseContentBlock(raw)
			if err != nil {
				t.Fatalf("ParseContentBlock failed: %v", err)
			}

			result, ok := parsed.(*TextBlock)
			if !ok {
				t.Fatalf("Expected *TextBlock, got %T", parsed)
			}
			if result.Text != original.Text {
				t.Errorf("Expected text=%q, got %q", original.Text, result.Text)
			}
		})
	}
}

// TestThinkingBlockJSONRoundTrip tests JSON marshaling and unmarshaling for ThinkingBlock.
func TestThinkingBlockJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		thinking  string
		signature string
	}{
		{"simple thinking", "Let me think...", "sig123"},
		{"empty fields", "", ""},
		{"long thinking", "This is a very long thinking process that spans multiple lines and contains various thoughts.", "signature-abc-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &ThinkingBlock{
				Thinking:  tt.thinking,
				Signature: tt.signature,
			}

			// Marshal
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// Verify fields
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("Unmarshal to map failed: %v", err)
			}
			if raw["type"] != "thinking" {
				t.Errorf("Expected type='thinking', got %v", raw["type"])
			}

			// Parse back
			parsed, err := ParseContentBlock(raw)
			if err != nil {
				t.Fatalf("ParseContentBlock failed: %v", err)
			}

			result, ok := parsed.(*ThinkingBlock)
			if !ok {
				t.Fatalf("Expected *ThinkingBlock, got %T", parsed)
			}
			if result.Thinking != original.Thinking {
				t.Errorf("Expected thinking=%q, got %q", original.Thinking, result.Thinking)
			}
			if result.Signature != original.Signature {
				t.Errorf("Expected signature=%q, got %q", original.Signature, result.Signature)
			}
		})
	}
}

// TestToolUseBlockJSONRoundTrip tests JSON marshaling and unmarshaling for ToolUseBlock.
func TestToolUseBlockJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		tname string
		input map[string]interface{}
	}{
		{
			"simple tool use",
			"tool_123",
			"calculator",
			map[string]interface{}{"a": 1.0, "b": 2.0},
		},
		{
			"empty input",
			"tool_456",
			"no_args_tool",
			map[string]interface{}{},
		},
		{
			"complex input",
			"tool_789",
			"complex_tool",
			map[string]interface{}{
				"nested": map[string]interface{}{
					"key": "value",
				},
				"array": []interface{}{1.0, 2.0, 3.0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &ToolUseBlock{
				ID:    tt.id,
				Name:  tt.tname,
				Input: tt.input,
			}

			// Marshal
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// Verify fields
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("Unmarshal to map failed: %v", err)
			}
			if raw["type"] != "tool_use" {
				t.Errorf("Expected type='tool_use', got %v", raw["type"])
			}

			// Parse back
			parsed, err := ParseContentBlock(raw)
			if err != nil {
				t.Fatalf("ParseContentBlock failed: %v", err)
			}

			result, ok := parsed.(*ToolUseBlock)
			if !ok {
				t.Fatalf("Expected *ToolUseBlock, got %T", parsed)
			}
			if result.ID != original.ID {
				t.Errorf("Expected id=%q, got %q", original.ID, result.ID)
			}
			if result.Name != original.Name {
				t.Errorf("Expected name=%q, got %q", original.Name, result.Name)
			}
		})
	}
}

// TestToolResultBlockJSONRoundTrip tests JSON marshaling and unmarshaling for ToolResultBlock.
func TestToolResultBlockJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		toolUseID string
		content   interface{}
		isError   bool
	}{
		{
			"success result",
			"tool_123",
			"Operation completed successfully",
			false,
		},
		{
			"error result",
			"tool_456",
			"Operation failed",
			true,
		},
		{
			"nil content",
			"tool_789",
			nil,
			false,
		},
		{
			"complex content",
			"tool_abc",
			[]interface{}{
				map[string]interface{}{"type": "text", "text": "Result"},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &ToolResultBlock{
				ToolUseID: tt.toolUseID,
				Content:   tt.content,
				IsError:   tt.isError,
			}

			// Marshal
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// Verify fields
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("Unmarshal to map failed: %v", err)
			}
			if raw["type"] != "tool_result" {
				t.Errorf("Expected type='tool_result', got %v", raw["type"])
			}

			// Parse back
			parsed, err := ParseContentBlock(raw)
			if err != nil {
				t.Fatalf("ParseContentBlock failed: %v", err)
			}

			result, ok := parsed.(*ToolResultBlock)
			if !ok {
				t.Fatalf("Expected *ToolResultBlock, got %T", parsed)
			}
			if result.ToolUseID != original.ToolUseID {
				t.Errorf("Expected tool_use_id=%q, got %q", original.ToolUseID, result.ToolUseID)
			}
			if result.IsError != original.IsError {
				t.Errorf("Expected is_error=%v, got %v", original.IsError, result.IsError)
			}
		})
	}
}

// TestParseContentBlockUnknownType tests error handling for unknown content block types.
func TestParseContentBlockUnknownType(t *testing.T) {
	raw := map[string]interface{}{
		"type": "unknown_type",
		"data": "some data",
	}

	_, err := ParseContentBlock(raw)
	if err == nil {
		t.Fatal("Expected error for unknown type, got nil")
	}

	if !IsMessageParseError(err) {
		t.Errorf("Expected MessageParseError, got %T", err)
	}
}

// TestParseContentBlockMissingType tests error handling for missing type field.
func TestParseContentBlockMissingType(t *testing.T) {
	raw := map[string]interface{}{
		"text": "some text",
	}

	_, err := ParseContentBlock(raw)
	if err == nil {
		t.Fatal("Expected error for missing type, got nil")
	}

	if !IsMessageParseError(err) {
		t.Errorf("Expected MessageParseError, got %T", err)
	}
}

// TestParseContentBlocks tests parsing multiple content blocks.
func TestParseContentBlocks(t *testing.T) {
	rawBlocks := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "Hello",
		},
		map[string]interface{}{
			"type":      "thinking",
			"thinking":  "Let me think",
			"signature": "sig",
		},
		map[string]interface{}{
			"type":  "tool_use",
			"id":    "tool_1",
			"name":  "calculator",
			"input": map[string]interface{}{"a": 1.0},
		},
	}

	blocks, err := ParseContentBlocks(rawBlocks)
	if err != nil {
		t.Fatalf("ParseContentBlocks failed: %v", err)
	}

	if len(blocks) != 3 {
		t.Fatalf("Expected 3 blocks, got %d", len(blocks))
	}

	if _, ok := blocks[0].(*TextBlock); !ok {
		t.Errorf("Expected TextBlock at index 0, got %T", blocks[0])
	}
	if _, ok := blocks[1].(*ThinkingBlock); !ok {
		t.Errorf("Expected ThinkingBlock at index 1, got %T", blocks[1])
	}
	if _, ok := blocks[2].(*ToolUseBlock); !ok {
		t.Errorf("Expected ToolUseBlock at index 2, got %T", blocks[2])
	}
}

// TestUserMessageCreation tests creating and marshaling a UserMessage.
func TestUserMessageCreation(t *testing.T) {
	msg := &UserMessage{Content: "Hello, Claude!"}
	if msg.Content != "Hello, Claude!" {
		t.Errorf("Expected content='Hello, Claude!', got %v", msg.Content)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if raw["content"] != "Hello, Claude!" {
		t.Errorf("Expected json content='Hello, Claude!', got %v", raw["content"])
	}
}

// TestAssistantMessageCreation tests creating and marshaling an AssistantMessage.
func TestAssistantMessageCreation(t *testing.T) {
	textBlock := &TextBlock{Text: "Hello, human!"}
	msg := &AssistantMessage{
		Content: []ContentBlock{textBlock},
		Model:   "claude-opus-4-1-20250805",
	}

	if len(msg.Content) != 1 {
		t.Errorf("Expected 1 content block, got %d", len(msg.Content))
	}
	if tb, ok := msg.Content[0].(*TextBlock); !ok || tb.Text != "Hello, human!" {
		t.Errorf("Expected text block with 'Hello, human!', got %v", msg.Content[0])
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if raw["model"] != "claude-opus-4-1-20250805" {
		t.Errorf("Expected model='claude-opus-4-1-20250805', got %v", raw["model"])
	}
}

// TestResultMessageCreation tests creating and marshaling a ResultMessage.
func TestResultMessageCreation(t *testing.T) {
	msg := &ResultMessage{
		Subtype:       "success",
		DurationMS:    1500,
		DurationAPIMS: 1200,
		IsError:       false,
		NumTurns:      1,
		SessionID:     "session-123",
		TotalCostUSD:  0.01,
	}

	if msg.Subtype != "success" {
		t.Errorf("Expected subtype='success', got %v", msg.Subtype)
	}
	if msg.TotalCostUSD != 0.01 {
		t.Errorf("Expected total_cost_usd=0.01, got %v", msg.TotalCostUSD)
	}
	if msg.SessionID != "session-123" {
		t.Errorf("Expected session_id='session-123', got %v", msg.SessionID)
	}
}

// TestHookInputTypes tests hook input type definitions.
func TestHookInputTypes(t *testing.T) {
	// Test NotificationHookInput
	notifInput := HookInput{
		SessionID:        "sess-1",
		TranscriptPath:   "/tmp/transcript",
		CWD:              "/home/user",
		HookEventName:    "Notification",
		Message:          "Task completed",
		NotificationType: "info",
	}
	if notifInput.HookEventName != "Notification" {
		t.Errorf("Expected HookEventName='Notification', got %v", notifInput.HookEventName)
	}

	// Test SubagentStartHookInput
	subInput := HookInput{
		SessionID:     "sess-1",
		HookEventName: "SubagentStart",
		AgentID:       "agent-42",
		AgentType:     "researcher",
	}
	if subInput.HookEventName != "SubagentStart" {
		t.Errorf("Expected HookEventName='SubagentStart', got %v", subInput.HookEventName)
	}

	// Test PermissionRequestHookInput
	permInput := HookInput{
		SessionID:     "sess-1",
		HookEventName: "PermissionRequest",
		ToolName:      "Bash",
		ToolInput:     map[string]interface{}{"command": "ls"},
	}
	if permInput.ToolName != "Bash" {
		t.Errorf("Expected ToolName='Bash', got %v", permInput.ToolName)
	}
}

// TestHookOutputTypes tests hook output type definitions.
func TestHookOutputTypes(t *testing.T) {
	// Test NotificationHookSpecificOutput
	notifOutput := HookOutput{
		HookSpecificOutput: map[string]interface{}{
			"hookEventName":     "Notification",
			"additionalContext": "Extra info",
		},
	}
	if notifOutput.HookSpecificOutput["hookEventName"] != "Notification" {
		t.Errorf("Expected hookEventName='Notification'")
	}

	// Test PermissionRequestHookSpecificOutput
	permOutput := HookOutput{
		HookSpecificOutput: map[string]interface{}{
			"hookEventName": "PermissionRequest",
			"decision":      map[string]interface{}{"type": "allow"},
		},
	}
	if permOutput.HookSpecificOutput["hookEventName"] != "PermissionRequest" {
		t.Errorf("Expected hookEventName='PermissionRequest'")
	}

	subOutput := HookOutput{
		HookSpecificOutput: map[string]interface{}{
			"hookEventName":     "SubagentStart",
			"additionalContext": "Starting subagent for research",
		},
	}
	if subOutput.HookSpecificOutput["hookEventName"] != "SubagentStart" {
		t.Errorf("Expected hookEventName='SubagentStart'")
	}

	preToolOutput := HookOutput{
		HookSpecificOutput: map[string]interface{}{
			"hookEventName":     "PreToolUse",
			"additionalContext": "context for claude",
		},
	}
	if preToolOutput.HookSpecificOutput["additionalContext"] != "context for claude" {
		t.Errorf("Expected additionalContext='context for claude'")
	}

	postToolOutput := HookOutput{
		HookSpecificOutput: map[string]interface{}{
			"hookEventName":        "PostToolUse",
			"updatedMCPToolOutput": map[string]interface{}{"result": "modified"},
		},
	}
	val, ok := postToolOutput.HookSpecificOutput["updatedMCPToolOutput"].(map[string]interface{})
	if !ok || val["result"] != "modified" {
		t.Errorf("Expected updatedMCPToolOutput result='modified'")
	}
}

// TestClaudeAgentOptionsWithSystemPromptFile tests SystemPromptFile on options.
func TestClaudeAgentOptionsWithSystemPromptFile(t *testing.T) {
	opts := ClaudeAgentOptions{
		SystemPromptFile: &SystemPromptFile{
			Type: "file",
			Path: "/path/to/prompt.md",
		},
	}

	if opts.SystemPromptFile == nil {
		t.Fatal("Expected SystemPromptFile to be set")
	}
	if opts.SystemPromptFile.Type != "file" {
		t.Errorf("Expected type 'file', got %v", opts.SystemPromptFile.Type)
	}
	if opts.SystemPromptFile.Path != "/path/to/prompt.md" {
		t.Errorf("Expected path, got %v", opts.SystemPromptFile.Path)
	}
}

// TestAgentDefinitionMinimal tests minimal AgentDefinition serialization.
func TestAgentDefinitionMinimal(t *testing.T) {
	agent := AgentDefinition{
		Description: "research agent",
	}
	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	if result["description"] != "research agent" {
		t.Errorf("Expected description 'research agent', got %v", result["description"])
	}
	// Empty fields should be omitted
	if _, ok := result["disallowedTools"]; ok {
		t.Error("Expected disallowedTools to be omitted when empty")
	}
}

// TestAgentDefinitionFull tests AgentDefinition with all fields.
func TestAgentDefinitionFull(t *testing.T) {
	maxTurns := 5
	agent := AgentDefinition{
		Description:     "coder agent",
		Prompt:          "You are a coder",
		Model:           "claude-sonnet-4-5-20250929",
		DisallowedTools: []string{"Bash"},
		MaxTurns:        &maxTurns,
		InitialPrompt:   "Start coding",
		McpServers:      []interface{}{"server1"},
		Skills:          []string{"coding"},
		Memory:          "user",
	}
	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	if result["model"] != "claude-sonnet-4-5-20250929" {
		t.Errorf("Expected model, got %v", result["model"])
	}
	tools := result["disallowedTools"].([]interface{})
	if len(tools) != 1 || tools[0] != "Bash" {
		t.Errorf("Expected disallowedTools=['Bash'], got %v", tools)
	}
	if result["maxTurns"] != float64(5) {
		t.Errorf("Expected maxTurns=5, got %v", result["maxTurns"])
	}
	if result["initialPrompt"] != "Start coding" {
		t.Errorf("Expected initialPrompt, got %v", result["initialPrompt"])
	}
}

// TestRateLimitEventTypes tests RateLimitEvent type constants.
func TestRateLimitEventTypes(t *testing.T) {
	if RateLimitStatusAllowed != "allowed" {
		t.Errorf("Expected 'allowed', got '%s'", RateLimitStatusAllowed)
	}
	if RateLimitStatusAllowedWarning != "allowed_warning" {
		t.Errorf("Expected 'allowed_warning', got '%s'", RateLimitStatusAllowedWarning)
	}
	if RateLimitStatusRejected != "rejected" {
		t.Errorf("Expected 'rejected', got '%s'", RateLimitStatusRejected)
	}
	if RateLimitTypeFiveHour != "five_hour" {
		t.Errorf("Expected 'five_hour', got '%s'", RateLimitTypeFiveHour)
	}
	if RateLimitTypeSevenDay != "seven_day" {
		t.Errorf("Expected 'seven_day', got '%s'", RateLimitTypeSevenDay)
	}
}

// TestContextUsageResponse tests ContextUsageResponse struct.
func TestContextUsageResponse(t *testing.T) {
	resp := ContextUsageResponse{
		Categories: []ContextUsageCategory{
			{Name: "system_prompt", Tokens: 100, Color: "blue"},
			{Name: "conversation", Tokens: 900, Color: "green"},
		},
		TotalTokens: 1000,
		MaxTokens:   4096,
		Percentage:  24.4,
		Model:       "claude-sonnet-4-5",
	}

	if len(resp.Categories) != 2 {
		t.Errorf("Expected 2 categories, got %d", len(resp.Categories))
	}
	if resp.TotalTokens != 1000 {
		t.Errorf("Expected TotalTokens=1000, got %d", resp.TotalTokens)
	}
	if resp.Model != "claude-sonnet-4-5" {
		t.Errorf("Expected model, got %s", resp.Model)
	}
}

// TestSDKSessionInfoNewFields tests new fields on SDKSessionInfo.
func TestSDKSessionInfoNewFields(t *testing.T) {
	tag := "important"
	createdAt := int64(1700000000)
	customTitle := "AI Generated Title"
	firstPrompt := "Last user message"

	info := SDKSessionInfo{
		SessionID:    "550e8400-e29b-41d4-a716-446655440000",
		Summary:      "Test",
		LastModified: 1700000000,
		Tag:          &tag,
		CreatedAt:    &createdAt,
		CustomTitle:  &customTitle,
		FirstPrompt:  &firstPrompt,
	}

	if info.Tag == nil || *info.Tag != "important" {
		t.Errorf("Tag mismatch")
	}
	if info.CreatedAt == nil || *info.CreatedAt != 1700000000 {
		t.Errorf("CreatedAt mismatch")
	}
	if info.CustomTitle == nil || *info.CustomTitle != "AI Generated Title" {
		t.Errorf("CustomTitle mismatch")
	}
	if info.FirstPrompt == nil || *info.FirstPrompt != "Last user message" {
		t.Errorf("FirstPrompt mismatch")
	}
}

// ---------------------------------------------------------------------------
// TestPermissionModeAllConstants
// ---------------------------------------------------------------------------

func TestPermissionModeAllConstants(t *testing.T) {
	modes := []PermissionMode{
		PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModeDontAsk,
		PermissionModePlan,
		PermissionModeBypassPermissions,
	}
	expected := []string{"default", "acceptEdits", "dontAsk", "plan", "bypassPermissions"}
	for i, m := range modes {
		if string(m) != expected[i] {
			t.Errorf("PermissionMode %d: expected %q, got %q", i, expected[i], m)
		}
	}
}

// ---------------------------------------------------------------------------
// TestMcpServerStatusTypes
// ---------------------------------------------------------------------------

func TestMcpServerStatus_Connected(t *testing.T) {
	status := McpServerStatus{
		Name:   "my-server",
		Status: McpServerStatusConnected,
		ServerInfo: &McpServerInfo{
			Name:    "my-server",
			Version: "1.2.3",
		},
		Scope: "project",
		Tools: []McpToolInfo{
			{
				Name:        "greet",
				Description: "Greet a user",
				Annotations: &McpToolAnnotations{
					ReadOnly:    true,
					Destructive: false,
					OpenWorld:   false,
				},
			},
		},
	}

	if status.Name != "my-server" {
		t.Errorf("Name mismatch")
	}
	if status.Status != McpServerStatusConnected {
		t.Errorf("Status mismatch")
	}
	if status.ServerInfo.Version != "1.2.3" {
		t.Errorf("ServerInfo version mismatch")
	}
	if !status.Tools[0].Annotations.ReadOnly {
		t.Error("Expected ReadOnly=true")
	}
}

func TestMcpServerStatus_Minimal(t *testing.T) {
	status := McpServerStatus{
		Name:   "pending-server",
		Status: McpServerStatusPending,
	}
	if status.Name != "pending-server" {
		t.Error("Name mismatch")
	}
	if status.Error != "" {
		t.Error("Expected empty error")
	}
}

func TestMcpServerStatus_FailedWithError(t *testing.T) {
	status := McpServerStatus{
		Name:   "broken-server",
		Status: McpServerStatusFailed,
		Error:  "Connection refused",
	}
	if status.Status != McpServerStatusFailed {
		t.Error("Status mismatch")
	}
	if status.Error != "Connection refused" {
		t.Error("Error mismatch")
	}
}

func TestMcpServerStatus_ClaudeAIProxy(t *testing.T) {
	status := McpServerStatus{
		Name:   "proxy-server",
		Status: McpServerStatusNeedsAuth,
		Config: map[string]interface{}{
			"type": "claudeai-proxy",
			"url":  "https://claude.ai/proxy",
			"id":   "proxy-abc",
		},
	}
	if status.Config["type"] != "claudeai-proxy" {
		t.Error("Config type mismatch")
	}
	if status.Config["id"] != "proxy-abc" {
		t.Error("Config id mismatch")
	}
}

func TestMcpStatusResponse_WrapsServers(t *testing.T) {
	resp := McpStatusResponse{
		MCPServers: []McpServerStatus{
			{Name: "a", Status: McpServerStatusConnected},
			{Name: "b", Status: McpServerStatusDisabled},
		},
	}
	if len(resp.MCPServers) != 2 {
		t.Fatalf("Expected 2 servers, got %d", len(resp.MCPServers))
	}
	if resp.MCPServers[0].Status != McpServerStatusConnected {
		t.Error("First server status mismatch")
	}
	if resp.MCPServers[1].Status != McpServerStatusDisabled {
		t.Error("Second server status mismatch")
	}
}

func TestMcpStatusResponse_JSONRoundTrip(t *testing.T) {
	resp := McpStatusResponse{
		MCPServers: []McpServerStatus{
			{
				Name:   "srv",
				Status: McpServerStatusConnected,
				Tools:  []McpToolInfo{{Name: "tool1", Description: "desc1"}},
			},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var parsed McpStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed.MCPServers) != 1 || parsed.MCPServers[0].Name != "srv" {
		t.Error("Round-trip mismatch")
	}
}

// ---------------------------------------------------------------------------
// TestAgentDefinition serialization
// ---------------------------------------------------------------------------

func TestAgentDefinition_MinimalOmitsUnset(t *testing.T) {
	agent := AgentDefinition{Description: "test", Prompt: "You are a test"}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["description"] != "test" || m["prompt"] != "You are a test" {
		t.Error("Required fields mismatch")
	}
	// Optional fields should be omitted
	for _, key := range []string{"tools", "disallowedTools", "model", "skills", "memory", "mcpServers", "initialPrompt", "maxTurns"} {
		if _, ok := m[key]; ok {
			t.Errorf("Expected %q to be omitted", key)
		}
	}
}

func TestAgentDefinition_SkillsAndMemory(t *testing.T) {
	agent := AgentDefinition{
		Description: "test",
		Prompt:      "p",
		Skills:      []string{"skill-a", "skill-b"},
		Memory:      "project",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	skills, ok := m["skills"].([]interface{})
	if !ok || len(skills) != 2 {
		t.Fatalf("Expected 2 skills, got %v", m["skills"])
	}
	if m["memory"] != "project" {
		t.Errorf("Expected memory='project', got %v", m["memory"])
	}
}

func TestAgentDefinition_McpServersCamelCase(t *testing.T) {
	agent := AgentDefinition{
		Description: "test",
		Prompt:      "p",
		McpServers: []interface{}{
			"slack",
			map[string]interface{}{
				"local": map[string]interface{}{"command": "python", "args": []string{"server.py"}},
			},
		},
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if _, ok := m["mcpServers"]; !ok {
		t.Error("Expected mcpServers key (camelCase)")
	}
	if _, ok := m["mcp_servers"]; ok {
		t.Error("Should not have snake_case mcp_servers")
	}
}

func TestAgentDefinition_DisallowedToolsAndMaxTurnsCamelCase(t *testing.T) {
	maxTurns := 10
	agent := AgentDefinition{
		Description:     "test",
		Prompt:          "p",
		DisallowedTools: []string{"Bash", "Write"},
		MaxTurns:        &maxTurns,
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if _, ok := m["disallowedTools"]; !ok {
		t.Error("Expected disallowedTools key")
	}
	if _, ok := m["disallowed_tools"]; ok {
		t.Error("Should not have snake_case disallowed_tools")
	}
	if m["maxTurns"] != float64(10) {
		t.Errorf("Expected maxTurns=10, got %v", m["maxTurns"])
	}
	if _, ok := m["max_turns"]; ok {
		t.Error("Should not have snake_case max_turns")
	}
}

func TestAgentDefinition_InitialPromptCamelCase(t *testing.T) {
	agent := AgentDefinition{
		Description:   "test",
		Prompt:        "p",
		InitialPrompt: "/review-pr 123",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["initialPrompt"] != "/review-pr 123" {
		t.Errorf("Expected initialPrompt='/review-pr 123', got %v", m["initialPrompt"])
	}
	if _, ok := m["initial_prompt"]; ok {
		t.Error("Should not have snake_case initial_prompt")
	}
}

func TestAgentDefinition_ModelFullID(t *testing.T) {
	agent := AgentDefinition{
		Description: "test",
		Prompt:      "p",
		Model:       "claude-opus-4-5",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["model"] != "claude-opus-4-5" {
		t.Errorf("Expected model='claude-opus-4-5', got %v", m["model"])
	}
}

// ---------------------------------------------------------------------------
// TestContextUsageResponse serialization with new fields
// ---------------------------------------------------------------------------

func TestContextUsageResponse_WithNewFields(t *testing.T) {
	threshold := 90
	resp := ContextUsageResponse{
		TotalTokens:          5000,
		MaxTokens:            10000,
		Percentage:           50.0,
		Model:                "claude-sonnet-4-5",
		AutoCompactThreshold: &threshold,
		MessageBreakdown:     map[string]interface{}{"user": 2000, "assistant": 3000},
		APIUsage:             map[string]interface{}{"inputTokens": 4000, "outputTokens": 1000},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["autoCompactThreshold"] != float64(90) {
		t.Errorf("Expected autoCompactThreshold=90, got %v", m["autoCompactThreshold"])
	}
	if mb, ok := m["messageBreakdown"].(map[string]interface{}); !ok || mb["user"] != float64(2000) {
		t.Errorf("messageBreakdown mismatch")
	}
}

// ---------------------------------------------------------------------------
// SdkBeta
// ---------------------------------------------------------------------------

func TestSdkBeta_Constants(t *testing.T) {
	// SdkBeta is a type alias for string
	var beta SdkBeta = SdkBetaContext1M
	if beta != "context-1m-2025-08-07" {
		t.Errorf("Expected SdkBetaContext1M='context-1m-2025-08-07', got %q", beta)
	}

	// Should be usable as plain string
	opts := &ClaudeAgentOptions{
		Betas: []SdkBeta{"context-1m", "other-beta"},
	}
	if len(opts.Betas) != 2 {
		t.Errorf("Expected 2 betas, got %d", len(opts.Betas))
	}
}
