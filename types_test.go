package claude

import (
	"encoding/json"
	"strings"
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

// TestParseContentBlocks_NonDictBlockRaises tests that a content block that
// is not a dict raises MessageParseError rather than being silently skipped.
// Ported from Python SDK #1058 (Go previously `continue`d past the block).
func TestParseContentBlocks_NonDictBlockRaises(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
	}{
		{"string", "oops"},
		{"number", float64(42)},
		{"bool", true},
		{"nil", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseContentBlocks([]interface{}{tc.raw})
			if err == nil {
				t.Fatalf("Expected error for %s content block, got nil", tc.name)
			}
			if !IsMessageParseError(err) {
				t.Errorf("Expected MessageParseError for %s block, got %T: %v", tc.name, err, err)
			}
		})
	}
}

// TestParseContentBlocks_NonDictBlockAbortsParse tests that a non-dict block
// aborts parsing even when a valid block precedes it (no silent partial skip).
func TestParseContentBlocks_NonDictBlockAbortsParse(t *testing.T) {
	rawBlocks := []interface{}{
		map[string]interface{}{"type": "text", "text": "ok"},
		"not-a-dict",
	}
	_, err := ParseContentBlocks(rawBlocks)
	if err == nil {
		t.Fatal("Expected error when a non-dict block follows a valid one, got nil")
	}
	if !IsMessageParseError(err) {
		t.Errorf("Expected MessageParseError, got %T", err)
	}
}

// TestParseAssistantMessage_StringContentRaises tests that an assistant
// message whose content is a bare string raises MessageParseError (not a
// runtime panic). Ported from Python SDK #1058.
func TestParseAssistantMessage_StringContentRaises(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"model":   "m",
			"content": "hi",
		},
	}
	_, err := ParseMessage(data)
	if err == nil {
		t.Fatal("Expected error for assistant message with string content, got nil")
	}
	if !IsMessageParseError(err) {
		t.Errorf("Expected MessageParseError, got %T: %v", err, err)
	}
}

// TestParseMessage_NonDictBlockRaisesForBothRoles mirrors Python #1058's
// parametrized test: a non-dict block inside content raises for both the
// user and assistant branches.
func TestParseMessage_NonDictBlockRaisesForBothRoles(t *testing.T) {
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			message := map[string]interface{}{"content": []interface{}{"oops"}}
			if role == "assistant" {
				message["model"] = "m"
			}
			data := map[string]interface{}{
				"type":    role,
				"message": message,
			}
			_, err := ParseMessage(data)
			if err == nil {
				t.Fatalf("Expected error for %s message with non-dict block, got nil", role)
			}
			if !IsMessageParseError(err) {
				t.Errorf("Expected MessageParseError for %s, got %T: %v", role, err, err)
			}
		})
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

	// updatedToolOutput (Python SDK #911) — PostToolUse hooks can replace tool output.
	postToolWithUpdated := HookOutput{
		HookSpecificOutput: map[string]interface{}{
			"hookEventName": "PostToolUse",
			"updatedToolOutput": map[string]interface{}{
				"stdout":      "replaced",
				"stderr":      "",
				"interrupted": false,
			},
		},
	}
	uto, ok := postToolWithUpdated.HookSpecificOutput["updatedToolOutput"].(map[string]interface{})
	if !ok || uto["stdout"] != "replaced" {
		t.Errorf("Expected updatedToolOutput stdout='replaced', got %v", postToolWithUpdated.HookSpecificOutput["updatedToolOutput"])
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

// TestSystemPromptPresetSnapshotMarshal tests that SystemPromptPreset
// marshals its snapshot field when set and omits it when nil (v0.2.165).
func TestSystemPromptPresetSnapshotMarshal(t *testing.T) {
	snapshot := true
	data, err := json.Marshal(&SystemPromptPreset{
		Type:     "preset",
		Preset:   "claude_code",
		Append:   "Be concise.",
		Snapshot: &snapshot,
	})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want := `{"type":"preset","preset":"claude_code","append":"Be concise.","snapshot":true}`
	if string(data) != want {
		t.Errorf("Expected %s, got %s", want, data)
	}

	// Nil snapshot must be omitted entirely.
	data, err = json.Marshal(&SystemPromptPreset{Type: "preset", Preset: "claude_code"})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want = `{"type":"preset","preset":"claude_code"}`
	if string(data) != want {
		t.Errorf("Expected %s, got %s", want, data)
	}
}

// TestSystemPromptCustomMarshal tests SystemPromptCustom serialization:
// snapshot is included when set and omitted when nil (v0.2.165).
func TestSystemPromptCustomMarshal(t *testing.T) {
	snapshot := false
	data, err := json.Marshal(&SystemPromptCustom{
		Type:     "custom",
		Prompt:   "Be helpful",
		Snapshot: &snapshot,
	})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want := `{"type":"custom","prompt":"Be helpful","snapshot":false}`
	if string(data) != want {
		t.Errorf("Expected %s, got %s", want, data)
	}

	data, err = json.Marshal(&SystemPromptCustom{Type: "custom", Prompt: "Be helpful"})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want = `{"type":"custom","prompt":"Be helpful"}`
	if string(data) != want {
		t.Errorf("Expected %s, got %s", want, data)
	}
}

// TestClaudeAgentOptionsWithSystemPromptCustom tests SystemPromptCustom on options.
func TestClaudeAgentOptionsWithSystemPromptCustom(t *testing.T) {
	snapshot := true
	opts := ClaudeAgentOptions{
		SystemPromptCustom: &SystemPromptCustom{
			Type:     "custom",
			Prompt:   "Be helpful",
			Snapshot: &snapshot,
		},
	}

	if opts.SystemPromptCustom == nil {
		t.Fatal("Expected SystemPromptCustom to be set")
	}
	if opts.SystemPromptCustom.Type != "custom" {
		t.Errorf("Expected type 'custom', got %v", opts.SystemPromptCustom.Type)
	}
	if opts.SystemPromptCustom.Prompt != "Be helpful" {
		t.Errorf("Expected prompt, got %v", opts.SystemPromptCustom.Prompt)
	}
	if opts.SystemPromptCustom.Snapshot == nil || *opts.SystemPromptCustom.Snapshot != true {
		t.Errorf("Expected snapshot=true, got %v", opts.SystemPromptCustom.Snapshot)
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
		PermissionModeAuto,
	}
	expected := []string{"default", "acceptEdits", "dontAsk", "plan", "bypassPermissions", "auto"}
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

// ---- Tests for new types added in v0.1.58–v0.1.65 ----

func TestServerToolUseBlockJSON(t *testing.T) {
	block := &ServerToolUseBlock{
		ID:    "stu_abc123",
		Name:  ServerToolNameWebSearch,
		Input: map[string]interface{}{"query": "Go generics"},
	}

	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if raw["type"] != "server_tool_use" {
		t.Errorf("Expected type=server_tool_use, got %v", raw["type"])
	}
	if raw["id"] != "stu_abc123" {
		t.Errorf("Unexpected id: %v", raw["id"])
	}
	if raw["name"] != string(ServerToolNameWebSearch) {
		t.Errorf("Unexpected name: %v", raw["name"])
	}
}

func TestServerToolUseBlockContentBlockMarker(t *testing.T) {
	block := &ServerToolUseBlock{ID: "x", Name: ServerToolNameBashCodeExecution}
	// Just confirm it implements ContentBlock interface.
	var _ ContentBlock = block
}

func TestServerToolResultBlockJSON(t *testing.T) {
	block := &ServerToolResultBlock{
		ToolUseID: "stu_abc123",
		Content:   map[string]interface{}{"result": "42"},
	}

	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if raw["type"] != "advisor_tool_result" {
		t.Errorf("Expected type=advisor_tool_result, got %v", raw["type"])
	}
	if raw["tool_use_id"] != "stu_abc123" {
		t.Errorf("Unexpected tool_use_id: %v", raw["tool_use_id"])
	}
}

func TestServerToolResultBlockContentBlockMarker(t *testing.T) {
	block := &ServerToolResultBlock{ToolUseID: "x"}
	var _ ContentBlock = block
}

func TestMirrorErrorMessageMarker(t *testing.T) {
	msg := &MirrorErrorMessage{
		Subtype:   "mirror_error",
		Error:     "store unavailable",
		UUID:      "uuid-1",
		SessionID: "sess-1",
		Key:       &SessionKey{ProjectKey: "pk", SessionID: "sid"},
	}

	// Confirm it implements Message.
	var _ Message = msg

	if msg.Subtype != "mirror_error" {
		t.Error("Subtype mismatch")
	}
	if msg.Key.ProjectKey != "pk" {
		t.Error("Key.ProjectKey mismatch")
	}
}

func TestServerToolNameConstants(t *testing.T) {
	// Verify a sample of the defined constants.
	constants := []ServerToolName{
		ServerToolNameWebSearch,
		ServerToolNameWebFetch,
		ServerToolNameCodeExecution,
		ServerToolNameBashCodeExecution,
		ServerToolNameTextEditorCodeExecution,
	}
	for _, c := range constants {
		if c == "" {
			t.Errorf("ServerToolName constant is empty")
		}
	}
}

func TestParseContentBlockServerToolUse(t *testing.T) {
	data := map[string]interface{}{
		"type":  "server_tool_use",
		"id":    "stu_xyz",
		"name":  "web_search",
		"input": map[string]interface{}{"q": "hello"},
	}
	block, err := ParseContentBlock(data)
	if err != nil {
		t.Fatalf("ParseContentBlock error: %v", err)
	}
	stu, ok := block.(*ServerToolUseBlock)
	if !ok {
		t.Fatalf("Expected *ServerToolUseBlock, got %T", block)
	}
	if stu.ID != "stu_xyz" {
		t.Errorf("ID mismatch: %s", stu.ID)
	}
}

func TestParseContentBlockAdvisorToolResult(t *testing.T) {
	data := map[string]interface{}{
		"type":        "advisor_tool_result",
		"tool_use_id": "stu_abc",
		"content":     map[string]interface{}{"answer": "42"},
	}
	block, err := ParseContentBlock(data)
	if err != nil {
		t.Fatalf("ParseContentBlock error: %v", err)
	}
	str, ok := block.(*ServerToolResultBlock)
	if !ok {
		t.Fatalf("Expected *ServerToolResultBlock, got %T", block)
	}
	if str.ToolUseID != "stu_abc" {
		t.Errorf("ToolUseID mismatch: %s", str.ToolUseID)
	}
}

func TestThinkingConfigDisplay(t *testing.T) {
	cfg := &ThinkingConfig{
		Type:         "adaptive",
		BudgetTokens: 0,
		Display:      "summarized",
	}
	if cfg.Display != "summarized" {
		t.Error("Display field mismatch")
	}
}

func TestSessionKeyStruct(t *testing.T) {
	key := SessionKey{
		ProjectKey: "my-project",
		SessionID:  "sess-1",
		Subpath:    "subagent/1",
	}
	if key.ProjectKey != "my-project" {
		t.Error("ProjectKey mismatch")
	}
	if key.SessionID != "sess-1" {
		t.Error("SessionID mismatch")
	}
	if key.Subpath != "subagent/1" {
		t.Error("Subpath mismatch")
	}
}

func TestSessionStoreListEntry(t *testing.T) {
	entry := SessionStoreListEntry{SessionID: "s1", Mtime: 12345}
	if entry.SessionID != "s1" || entry.Mtime != 12345 {
		t.Error("SessionStoreListEntry field mismatch")
	}
}

func TestSessionSummaryEntry(t *testing.T) {
	e := SessionSummaryEntry{
		SessionID: "s1",
		Mtime:     9999,
		Data:      map[string]interface{}{"custom_title": "My Session"},
	}
	if e.Data["custom_title"] != "My Session" {
		t.Error("Data field mismatch")
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

// ---------------------------------------------------------------------------
// AgentDefinition new fields: Background, Effort, PermissionMode (v0.1.54)
// ---------------------------------------------------------------------------

func TestAgentDefinition_BackgroundField(t *testing.T) {
	bg := true
	agent := AgentDefinition{
		Description: "bg agent",
		Prompt:      "Do work in background",
		Background:  &bg,
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["background"] != true {
		t.Errorf("Expected background=true, got %v", m["background"])
	}
}

func TestAgentDefinition_BackgroundOmittedWhenNil(t *testing.T) {
	agent := AgentDefinition{
		Description: "test",
		Prompt:      "p",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if _, ok := m["background"]; ok {
		t.Error("Expected background to be omitted when nil")
	}
}

func TestAgentDefinition_EffortField(t *testing.T) {
	agent := AgentDefinition{
		Description: "high effort agent",
		Prompt:      "Think hard",
		Effort:      "high",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["effort"] != "high" {
		t.Errorf("Expected effort='high', got %v", m["effort"])
	}
}

func TestAgentDefinition_EffortXhigh(t *testing.T) {
	agent := AgentDefinition{
		Description: "xhigh effort agent",
		Prompt:      "p",
		Effort:      "xhigh",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["effort"] != "xhigh" {
		t.Errorf("Expected effort='xhigh', got %v", m["effort"])
	}
}

func TestAgentDefinition_EffortOmittedWhenEmpty(t *testing.T) {
	agent := AgentDefinition{
		Description: "test",
		Prompt:      "p",
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if _, ok := m["effort"]; ok {
		t.Error("Expected effort to be omitted when empty")
	}
}

func TestAgentDefinition_PermissionModeField(t *testing.T) {
	agent := AgentDefinition{
		Description:    "safe agent",
		Prompt:         "Be safe",
		PermissionMode: PermissionModeAcceptEdits,
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["permissionMode"] != string(PermissionModeAcceptEdits) {
		t.Errorf("Expected permissionMode='acceptEdits', got %v", m["permissionMode"])
	}
	if _, ok := m["permission_mode"]; ok {
		t.Error("Should not have snake_case permission_mode")
	}
}

func TestAgentDefinition_AllNewFieldsCombined(t *testing.T) {
	bg := false
	maxTurns := 5
	agent := AgentDefinition{
		Description:    "full agent",
		Prompt:         "Do everything",
		MaxTurns:       &maxTurns,
		Background:     &bg,
		Effort:         "max",
		PermissionMode: PermissionModeBypassPermissions,
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["background"] != false {
		t.Errorf("Expected background=false, got %v", m["background"])
	}
	if m["effort"] != "max" {
		t.Errorf("Expected effort='max', got %v", m["effort"])
	}
	if m["permissionMode"] != string(PermissionModeBypassPermissions) {
		t.Errorf("Expected permissionMode='bypassPermissions', got %v", m["permissionMode"])
	}
	if m["maxTurns"] != float64(5) {
		t.Errorf("Expected maxTurns=5, got %v", m["maxTurns"])
	}
}

// TestAgentDefinition_EffortAsInt tests that AgentDefinition.Effort accepts
// integer values in addition to string literals — ported from Python SDK
// where effort: Literal["low","medium","high","max"] | int | None.
func TestAgentDefinition_EffortAsInt(t *testing.T) {
	agent := AgentDefinition{
		Description: "numeric effort agent",
		Prompt:      "Think with numeric budget",
		Effort:      1000,
	}
	data, _ := json.Marshal(agent)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	if m["effort"] != float64(1000) {
		t.Errorf("Expected effort=1000 (numeric), got %v", m["effort"])
	}
}

// TestVersion tests that the SDK version constant is defined.
func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version constant must not be empty")
	}
	// Verify it matches semver-like pattern
	if len(Version) < 5 {
		t.Errorf("Version %q too short to be a valid semver", Version)
	}
}

// ---------------------------------------------------------------------------
// SandboxNetworkConfig new fields (#893)
// ---------------------------------------------------------------------------

func TestSandboxNetworkConfig_NewFieldsRoundTrip(t *testing.T) {
	cfg := SandboxNetworkConfig{
		AllowedDomains:          []string{"example.com", "api.example.com"},
		DeniedDomains:           []string{"evil.com"},
		AllowManagedDomainsOnly: true,
		AllowUnixSockets:        []string{"/var/run/app.sock"},
		AllowAllUnixSockets:     false,
		AllowLocalBinding:       true,
		AllowMachLookup:         []string{"com.apple.locationd"},
		HTTPProxyPort:           8080,
		SOCKSProxyPort:          1080,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got SandboxNetworkConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.AllowedDomains) != 2 || got.AllowedDomains[0] != "example.com" {
		t.Errorf("AllowedDomains mismatch: %v", got.AllowedDomains)
	}
	if len(got.DeniedDomains) != 1 || got.DeniedDomains[0] != "evil.com" {
		t.Errorf("DeniedDomains mismatch: %v", got.DeniedDomains)
	}
	if !got.AllowManagedDomainsOnly {
		t.Error("AllowManagedDomainsOnly should be true")
	}
	if len(got.AllowMachLookup) != 1 || got.AllowMachLookup[0] != "com.apple.locationd" {
		t.Errorf("AllowMachLookup mismatch: %v", got.AllowMachLookup)
	}
}

func TestSandboxNetworkConfig_OmitemptyOnNilSlices(t *testing.T) {
	cfg := SandboxNetworkConfig{
		AllowLocalBinding: true,
		HTTPProxyPort:     9090,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["allowedDomains"]; ok {
		t.Error("allowedDomains should be omitted when nil")
	}
	if _, ok := m["deniedDomains"]; ok {
		t.Error("deniedDomains should be omitted when nil")
	}
	if _, ok := m["allowMachLookup"]; ok {
		t.Error("allowMachLookup should be omitted when nil")
	}
	if _, ok := m["allowManagedDomainsOnly"]; ok {
		t.Error("allowManagedDomainsOnly should be omitted when false")
	}
}

// ---------------------------------------------------------------------------
// SessionStoreFlushMode constants (#905)
// ---------------------------------------------------------------------------

func TestSessionStoreFlushMode_Constants(t *testing.T) {
	if SessionStoreFlushModeBatched != "batched" {
		t.Errorf("SessionStoreFlushModeBatched=%q, want 'batched'", SessionStoreFlushModeBatched)
	}
	if SessionStoreFlushModeEager != "eager" {
		t.Errorf("SessionStoreFlushModeEager=%q, want 'eager'", SessionStoreFlushModeEager)
	}
}

// ---------------------------------------------------------------------------
// Tests ported from Python SDK v0.1.73..v0.1.76
// ---------------------------------------------------------------------------

func TestDeferredToolUseStruct(t *testing.T) {
	dt := &DeferredToolUse{
		ID:   "toolu_abc",
		Name: "Bash",
		Input: map[string]interface{}{
			"command": "ls -la",
		},
	}
	if dt.ID != "toolu_abc" {
		t.Errorf("Expected ID 'toolu_abc', got '%s'", dt.ID)
	}
	if dt.Name != "Bash" {
		t.Errorf("Expected Name 'Bash', got '%s'", dt.Name)
	}
	if dt.Input["command"] != "ls -la" {
		t.Errorf("Expected command 'ls -la', got '%v'", dt.Input["command"])
	}
}

func TestHookEventMessageImplementsMessage(t *testing.T) {
	msg := &HookEventMessage{
		Subtype:       "hook_started",
		HookEventName: "PreToolUse",
		Data:          map[string]interface{}{"tool_name": "Bash"},
		SessionID:     "session_123",
		UUID:          "uuid_456",
	}
	var _ Message = msg
	if msg.Subtype != "hook_started" {
		t.Errorf("Expected subtype 'hook_started', got '%s'", msg.Subtype)
	}
	if msg.HookEventName != "PreToolUse" {
		t.Errorf("Expected HookEventName 'PreToolUse', got '%s'", msg.HookEventName)
	}
}

func TestToolPermissionContextNewFields(t *testing.T) {
	ctx := ToolPermissionContext{
		ToolUseID:      "toolu_001",
		AgentID:        "agent_001",
		BlockedPath:    "/etc/hosts",
		DecisionReason: "PreToolUse hook returned ask",
		Title:          "Claude wants to write to /etc/hosts",
		DisplayName:    "Write file",
		Description:    "Write content to a system file",
	}
	if ctx.BlockedPath != "/etc/hosts" {
		t.Errorf("Expected BlockedPath '/etc/hosts', got '%s'", ctx.BlockedPath)
	}
	if ctx.DecisionReason != "PreToolUse hook returned ask" {
		t.Errorf("Expected DecisionReason, got '%s'", ctx.DecisionReason)
	}
	if ctx.Title != "Claude wants to write to /etc/hosts" {
		t.Errorf("Expected Title, got '%s'", ctx.Title)
	}
	if ctx.DisplayName != "Write file" {
		t.Errorf("Expected DisplayName 'Write file', got '%s'", ctx.DisplayName)
	}
	if ctx.Description != "Write content to a system file" {
		t.Errorf("Expected Description, got '%s'", ctx.Description)
	}
}

func TestResultMessageDeferredToolUseNil(t *testing.T) {
	rm := &ResultMessage{
		Subtype:   "success",
		SessionID: "session_1",
	}
	if rm.DeferredToolUse != nil {
		t.Error("Expected DeferredToolUse to be nil by default")
	}
	if rm.APIErrorStatus != nil {
		t.Error("Expected APIErrorStatus to be nil by default")
	}
}

func TestResultMessageAPIErrorStatus(t *testing.T) {
	status := 529
	rm := &ResultMessage{
		Subtype:        "success",
		SessionID:      "session_1",
		IsError:        true,
		APIErrorStatus: &status,
	}
	if rm.APIErrorStatus == nil || *rm.APIErrorStatus != 529 {
		t.Errorf("Expected APIErrorStatus=529, got %v", rm.APIErrorStatus)
	}
}

// TestEffortLevelValues verifies that all EffortLevel constants are defined.
func TestEffortLevelValues(t *testing.T) {
	expected := map[EffortLevel]bool{
		EffortLevelLow:    true,
		EffortLevelMedium: true,
		EffortLevelHigh:   true,
		EffortLevelXHigh:  true,
		EffortLevelMax:    true,
	}
	if len(expected) != 5 {
		t.Errorf("Expected 5 distinct EffortLevel values, got %d", len(expected))
	}
	// Verify string values
	tests := []struct {
		constant EffortLevel
		value    string
	}{
		{EffortLevelLow, "low"},
		{EffortLevelMedium, "medium"},
		{EffortLevelHigh, "high"},
		{EffortLevelXHigh, "xhigh"},
		{EffortLevelMax, "max"},
	}
	for _, tt := range tests {
		if string(tt.constant) != tt.value {
			t.Errorf("EffortLevel constant = %q, want %q", tt.constant, tt.value)
		}
	}
}

// TestHookInputPermissionSuggestionsType verifies the PermissionSuggestions field type.
func TestHookInputPermissionSuggestionsType(t *testing.T) {
	hi := HookInput{
		PermissionSuggestions: []map[string]interface{}{
			{"type": "addRules", "behavior": "allow"},
		},
	}
	if len(hi.PermissionSuggestions) != 1 {
		t.Fatalf("Expected 1 suggestion, got %d", len(hi.PermissionSuggestions))
	}
	if hi.PermissionSuggestions[0]["type"] != "addRules" {
		t.Errorf("Expected type=addRules, got %v", hi.PermissionSuggestions[0]["type"])
	}
}

// ---------------------------------------------------------------------------
// Tests ported from Python SDK v0.2.133..v0.2.143 (#1199, #1207)
// ---------------------------------------------------------------------------

// TestMessageOriginAccessors tests the MessageOrigin Kind/Subkind accessors
// and the origin kind/subkind constants (ported from Python SDK #1199,
// commit d48fa33).
func TestMessageOriginAccessors(t *testing.T) {
	// Kind returns the discriminator; Subkind returns "" when absent.
	human := MessageOrigin{"kind": "human"}
	if human.Kind() != MessageOriginKindHuman {
		t.Errorf("Expected kind 'human', got '%s'", human.Kind())
	}
	if human.Subkind() != "" {
		t.Errorf("Expected empty subkind, got '%s'", human.Subkind())
	}

	tn := MessageOrigin{"kind": "task-notification", "subkind": "scheduled-trigger"}
	if tn.Kind() != MessageOriginKindTaskNotification {
		t.Errorf("Expected kind 'task-notification', got '%s'", tn.Kind())
	}
	if tn.Subkind() != TaskNotificationOriginSubkindScheduledTrigger {
		t.Errorf("Expected subkind 'scheduled-trigger', got '%s'", tn.Subkind())
	}

	// Non-string kind/subkind values degrade to "".
	malformed := MessageOrigin{"kind": 42, "subkind": true}
	if malformed.Kind() != "" {
		t.Errorf("Expected empty kind for non-string, got '%s'", malformed.Kind())
	}
	if malformed.Subkind() != "" {
		t.Errorf("Expected empty subkind for non-string, got '%s'", malformed.Subkind())
	}

	// Nil origin is safe to interrogate (unattributed turn).
	var none MessageOrigin
	if none.Kind() != "" || none.Subkind() != "" {
		t.Errorf("Expected empty kind/subkind for nil origin, got '%s'/'%s'", none.Kind(), none.Subkind())
	}

	// Spot-check the full set of known kind constants against the wire values.
	known := map[MessageOriginKind]string{
		MessageOriginKindHuman:            "human",
		MessageOriginKindChannel:          "channel",
		MessageOriginKindPeer:             "peer",
		MessageOriginKindTaskNotification: "task-notification",
		MessageOriginKindCoordinator:      "coordinator",
		MessageOriginKindUnclassified:     "unclassified",
		MessageOriginKindObserver:         "observer",
		MessageOriginKindAutoContinuation: "auto-continuation",
		MessageOriginKindObserverActivity: "observer-activity",
	}
	for kind, wire := range known {
		if string(kind) != wire {
			t.Errorf("Expected wire value %q, got %q", wire, string(kind))
		}
	}
	if string(TaskNotificationOriginSubkindPeerSendMessage) != "peer-send-message" {
		t.Errorf("Unexpected peer-send-message wire value: %q", string(TaskNotificationOriginSubkindPeerSendMessage))
	}
}

// TestSessionMessageParentAgentID tests the parent_agent_id field on
// SessionMessage (ported from Python SDK #1207, commit 2bbdce6).
func TestSessionMessageParentAgentID(t *testing.T) {
	agentID := "agent-abc123"
	msg := SessionMessage{
		Type:          SessionMessageTypeAssistant,
		UUID:          "uuid-1",
		SessionID:     "session-1",
		Message:       map[string]interface{}{"role": "assistant"},
		ParentAgentID: &agentID,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var roundTrip SessionMessage
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if roundTrip.ParentAgentID == nil || *roundTrip.ParentAgentID != agentID {
		t.Errorf("Expected ParentAgentID %q after round-trip, got %v", agentID, roundTrip.ParentAgentID)
	}

	// Wire format uses snake_case.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}
	if raw["parent_agent_id"] != agentID {
		t.Errorf("Expected wire key parent_agent_id=%q, got %v", agentID, raw["parent_agent_id"])
	}

	// Nil ParentAgentID is omitted from the wire and round-trips to nil.
	top := SessionMessage{
		Type:      SessionMessageTypeUser,
		UUID:      "uuid-2",
		SessionID: "session-1",
		Message:   map[string]interface{}{"role": "user"},
	}
	data, err = json.Marshal(top)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(data), "parent_agent_id") {
		t.Errorf("Expected parent_agent_id to be omitted, got %s", data)
	}
	var roundTripNil SessionMessage
	if err := json.Unmarshal(data, &roundTripNil); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if roundTripNil.ParentAgentID != nil {
		t.Errorf("Expected nil ParentAgentID, got %v", *roundTripNil.ParentAgentID)
	}
}

// ---------------------------------------------------------------------------
// v0.2.166 types ports (Python SDK #1331, #1332, #1380)
// ---------------------------------------------------------------------------

// jsonKeys marshals v and returns the set of object keys it produced.
func jsonKeys(t *testing.T, v interface{}) map[string]bool {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}
	keys := make(map[string]bool, len(raw))
	for k := range raw {
		keys[k] = true
	}
	return keys
}

// keySet builds a set from a slice of keys, for comparisons against jsonKeys.
func keySet(keys ...string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// assertKeysEqual fails unless got equals want, naming every difference.
func assertKeysEqual(t *testing.T, context string, got, want map[string]bool) {
	t.Helper()
	for k := range want {
		if !got[k] {
			t.Errorf("%s: missing key %q (got %v)", context, k, got)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("%s: unexpected key %q (want %v)", context, k, want)
		}
	}
}

// TestSystemInitDataKeySets pins the required/optional key sets of
// SystemInitData, mirroring python test_key_sets_match_typescript
// (Python SDK #1332): the keys must match the TypeScript SDK's
// SDKSystemMessage fields.
func TestSystemInitDataKeySets(t *testing.T) {
	full := SystemInitData{
		Type:                   "system",
		Subtype:                "init",
		UUID:                   "u1",
		SessionID:              "s1",
		Agents:                 []string{"reviewer"},
		APIKeySource:           "none",
		Betas:                  []string{"context-1m-2025-08-07"},
		ClaudeCodeVersion:      "2.1.284",
		CWD:                    "/repo",
		Tools:                  []string{"Bash"},
		MCPServers:             []SystemInitMcpServer{{Name: "docs", Status: "connected"}},
		Model:                  "claude-sonnet-5",
		PermissionMode:         PermissionModeDefault,
		SlashCommands:          []string{"compact"},
		TerminalSlashCommands:  []string{"exit"},
		OutputStyle:            "default",
		Skills:                 []string{},
		Plugins:                []SystemInitPlugin{{Name: "p", Path: "/p"}},
		PluginErrors:           []SystemInitPluginError{{Plugin: "p", Type: "path-not-found", Message: "gone"}},
		FastModeState:          FastModeStateOn,
		FastModeDisabledReason: FastModeDisabledReasonSdkOptInRequired,
		Capabilities:           []string{"interrupt_receipt_v1"},
	}
	assertKeysEqual(t, "SystemInitData full", jsonKeys(t, full), keySet(
		"type", "subtype", "uuid", "session_id", "agents", "apiKeySource",
		"betas", "claude_code_version", "cwd", "tools", "mcp_servers",
		"model", "permissionMode", "slash_commands",
		"terminal_slash_commands", "output_style", "skills", "plugins",
		"plugin_errors", "fast_mode_state", "fast_mode_disabled_reason",
		"capabilities",
	))

	minimal := SystemInitData{
		Type:              "system",
		Subtype:           "init",
		UUID:              "u1",
		SessionID:         "s1",
		APIKeySource:      "none",
		ClaudeCodeVersion: "2.1.284",
		CWD:               "/repo",
		Tools:             []string{"Bash"},
		MCPServers:        []SystemInitMcpServer{},
		Model:             "claude-sonnet-5",
		PermissionMode:    PermissionModeDefault,
		SlashCommands:     []string{},
		OutputStyle:       "default",
		Skills:            []string{},
		Plugins:           []SystemInitPlugin{},
	}
	assertKeysEqual(t, "SystemInitData minimal", jsonKeys(t, minimal), keySet(
		"type", "subtype", "uuid", "session_id", "apiKeySource",
		"claude_code_version", "cwd", "tools", "mcp_servers", "model",
		"permissionMode", "slash_commands", "output_style", "skills",
		"plugins",
	))
}

// TestSystemInitDataNestedKeySets pins the nested SystemInit* key sets,
// mirroring python test_nested_key_sets (Python SDK #1332).
func TestSystemInitDataNestedKeySets(t *testing.T) {
	assertKeysEqual(t, "SystemInitMcpServer full", jsonKeys(t, SystemInitMcpServer{
		Name: "docs", Status: "connected", Source: "sdk",
	}), keySet("name", "status", "source"))
	assertKeysEqual(t, "SystemInitMcpServer minimal", jsonKeys(t, SystemInitMcpServer{
		Name: "docs", Status: "connected",
	}), keySet("name", "status"))

	assertKeysEqual(t, "SystemInitPlugin full", jsonKeys(t, SystemInitPlugin{
		Name: "p", Path: "/p", Version: "1.2.3",
	}), keySet("name", "path", "version"))
	assertKeysEqual(t, "SystemInitPlugin minimal", jsonKeys(t, SystemInitPlugin{
		Name: "p", Path: "/p",
	}), keySet("name", "path"))

	assertKeysEqual(t, "SystemInitPluginError full", jsonKeys(t, SystemInitPluginError{
		Plugin: "inline[0]", Type: "path-not-found", Message: "gone", Path: "/p",
	}), keySet("plugin", "type", "message", "path"))
	assertKeysEqual(t, "SystemInitPluginError minimal", jsonKeys(t, SystemInitPluginError{
		Plugin: "inline[0]", Type: "path-not-found", Message: "gone",
	}), keySet("plugin", "type", "message"))
}

// TestFastModeLiterals pins the FastModeState and FastModeDisabledReason
// values, mirroring python test_fast_mode_literals (Python SDK #1332). The
// disabled-reason set is open: the CLI may send values not listed here, so
// unknown values must stay representable.
func TestFastModeLiterals(t *testing.T) {
	states := map[FastModeState]bool{
		FastModeStateOff:      true,
		FastModeStateCooldown: true,
		FastModeStateOn:       true,
	}
	if len(states) != 3 {
		t.Errorf("FastModeState constants: got %v", states)
	}
	reasons := map[FastModeDisabledReason]bool{
		FastModeDisabledReasonFree:               true,
		FastModeDisabledReasonPreference:         true,
		FastModeDisabledReasonExtraUsageDisabled: true,
		FastModeDisabledReasonNetworkError:       true,
		FastModeDisabledReasonUnknown:            true,
		FastModeDisabledReasonNotFirstParty:      true,
		FastModeDisabledReasonDisabledByEnv:      true,
		FastModeDisabledReasonModelNotAllowed:    true,
		FastModeDisabledReasonSdkOptInRequired:   true,
		FastModeDisabledReasonPending:            true,
	}
	if len(reasons) != 10 {
		t.Errorf("FastModeDisabledReason constants: got %v", reasons)
	}
	// Open set: an unknown CLI value round-trips.
	var unknown FastModeDisabledReason = "brand_new_check"
	data, err := json.Marshal(SystemInitData{FastModeDisabledReason: unknown, MCPServers: []SystemInitMcpServer{}, Tools: []string{}, SlashCommands: []string{}, Skills: []string{}, Plugins: []SystemInitPlugin{}})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if !strings.Contains(string(data), `"brand_new_check"`) {
		t.Errorf("unknown FastModeDisabledReason not on the wire: %s", data)
	}
}

// TestSystemInitDataFromInitFrame pins that typing the payload does not
// change what the parser returns, mirroring python
// test_init_frame_still_parses_to_plain_system_message (Python SDK #1332):
// ParseMessage still yields a plain *SystemMessage whose Data is the frame
// verbatim, and ParseSystemInitData narrows it.
func TestSystemInitDataFromInitFrame(t *testing.T) {
	data := map[string]interface{}{
		"type":                "system",
		"subtype":             "init",
		"uuid":                "u1",
		"session_id":          "s1",
		"apiKeySource":        "none",
		"claude_code_version": "2.1.284",
		"cwd":                 "/repo",
		"tools":               []interface{}{"Bash"},
		"mcp_servers":         []interface{}{map[string]interface{}{"name": "docs", "status": "connected"}},
		"model":               "claude-sonnet-5",
		"permissionMode":      "default",
		"slash_commands":      []interface{}{"compact"},
		"output_style":        "default",
		"skills":              []interface{}{},
		"plugins":             []interface{}{},
	}
	message, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	sys, ok := message.(*SystemMessage)
	if !ok {
		t.Fatalf("expected *SystemMessage, got %T", message)
	}
	if sys.Subtype != "init" {
		t.Errorf("subtype: got %q", sys.Subtype)
	}
	if len(sys.Data) != len(data) {
		t.Errorf("Data not verbatim: got %v", sys.Data)
	}

	init, err := ParseSystemInitData(sys.Data)
	if err != nil {
		t.Fatalf("ParseSystemInitData failed: %v", err)
	}
	if len(init.MCPServers) != 1 || init.MCPServers[0].Status != "connected" {
		t.Errorf("mcp_servers: got %+v", init.MCPServers)
	}
	if init.Capabilities != nil {
		t.Errorf("capabilities: got %v, want nil", init.Capabilities)
	}
	if init.ClaudeCodeVersion != "2.1.284" || init.APIKeySource != "none" {
		t.Errorf("scalar fields: got %+v", init)
	}
}

// TestParseSystemInitDataErrors covers the marshal/unmarshal error paths.
func TestParseSystemInitDataErrors(t *testing.T) {
	if _, err := ParseSystemInitData(map[string]interface{}{"bad": make(chan int)}); err == nil {
		t.Error("expected marshal error for unmarshalable value")
	}
	if _, err := ParseSystemInitData(map[string]interface{}{"tools": "not-an-array"}); err == nil {
		t.Error("expected unmarshal error for type mismatch")
	}
}

// hookOutputShape describes one hook-specific output type for the key-set
// test below: the zero-value keys (required) and the keys present when every
// field is set (required + optional). Mirrors python _OUTPUT_KEYS /
// test_output_key_sets_match_typescript (Python SDK #1331), checked against
// the TypeScript SDK's *HookSpecificOutput types.
type hookOutputShape struct {
	name      string
	eventName string
	zero      interface{}
	full      interface{}
	required  []string
	optional  []string
}

// hookSpecificOutputShapes lists every member of the Python SDK's
// HookSpecificOutput union at v0.2.166. The table doubles as the
// distinct-event-name check of python
// test_hook_specific_output_members_have_distinct_event_names.
func hookSpecificOutputShapes() []hookOutputShape {
	suppress := true
	retry := true
	reload := true
	return []hookOutputShape{
		{
			name:      "PreToolUseHookSpecificOutput",
			eventName: "PreToolUse",
			zero:      PreToolUseHookSpecificOutput{},
			full: PreToolUseHookSpecificOutput{
				PermissionDecision:       HookPermissionDecisionDeny,
				PermissionDecisionReason: "dangerous",
				UpdatedInput:             map[string]interface{}{"command": "ls"},
				AdditionalContext:        "ctx",
			},
			required: []string{"hookEventName"},
			optional: []string{"permissionDecision", "permissionDecisionReason", "updatedInput", "additionalContext"},
		},
		{
			name:      "PostToolUseHookSpecificOutput",
			eventName: "PostToolUse",
			zero:      PostToolUseHookSpecificOutput{},
			full: PostToolUseHookSpecificOutput{
				AdditionalContext:    "ctx",
				UpdatedToolOutput:    map[string]interface{}{"stdout": ""},
				UpdatedMCPToolOutput: map[string]interface{}{"result": 1},
			},
			required: []string{"hookEventName"},
			optional: []string{"additionalContext", "updatedToolOutput", "updatedMCPToolOutput"},
		},
		{
			name:      "PostToolUseFailureHookSpecificOutput",
			eventName: "PostToolUseFailure",
			zero:      PostToolUseFailureHookSpecificOutput{},
			full:      PostToolUseFailureHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "UserPromptSubmitHookSpecificOutput",
			eventName: "UserPromptSubmit",
			zero:      UserPromptSubmitHookSpecificOutput{},
			full: UserPromptSubmitHookSpecificOutput{
				AdditionalContext:      "ctx",
				SessionTitle:           "Refactor auth",
				SuppressOriginalPrompt: &suppress,
			},
			required: []string{"hookEventName"},
			optional: []string{"additionalContext", "sessionTitle", "suppressOriginalPrompt"},
		},
		{
			name:      "UserPromptExpansionHookSpecificOutput",
			eventName: "UserPromptExpansion",
			zero:      UserPromptExpansionHookSpecificOutput{},
			full: UserPromptExpansionHookSpecificOutput{
				AdditionalContext:      "ctx",
				SuppressOriginalPrompt: &suppress,
			},
			required: []string{"hookEventName"},
			optional: []string{"additionalContext", "suppressOriginalPrompt"},
		},
		{
			name:      "SessionStartHookSpecificOutput",
			eventName: "SessionStart",
			zero:      SessionStartHookSpecificOutput{},
			full: SessionStartHookSpecificOutput{
				AdditionalContext:  "ctx",
				InitialUserMessage: "Run the test suite",
				SessionTitle:       "auth-refactor",
				WatchPaths:         []string{"/repo/.envrc"},
				ReloadSkills:       &reload,
			},
			required: []string{"hookEventName"},
			optional: []string{"additionalContext", "initialUserMessage", "sessionTitle", "watchPaths", "reloadSkills"},
		},
		{
			name:      "NotificationHookSpecificOutput",
			eventName: "Notification",
			zero:      NotificationHookSpecificOutput{},
			full:      NotificationHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "SubagentStartHookSpecificOutput",
			eventName: "SubagentStart",
			zero:      SubagentStartHookSpecificOutput{},
			full:      SubagentStartHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "PermissionRequestHookSpecificOutput",
			eventName: "PermissionRequest",
			zero:      PermissionRequestHookSpecificOutput{},
			full:      PermissionRequestHookSpecificOutput{Decision: map[string]interface{}{"type": "allow"}},
			required:  []string{"hookEventName", "decision"},
			optional:  []string{},
		},
		{
			name:      "PostToolBatchHookSpecificOutput",
			eventName: "PostToolBatch",
			zero:      PostToolBatchHookSpecificOutput{},
			full:      PostToolBatchHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "StopHookSpecificOutput",
			eventName: "Stop",
			zero:      StopHookSpecificOutput{},
			full:      StopHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "SubagentStopHookSpecificOutput",
			eventName: "SubagentStop",
			zero:      SubagentStopHookSpecificOutput{},
			full:      SubagentStopHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "PermissionDeniedHookSpecificOutput",
			eventName: "PermissionDenied",
			zero:      PermissionDeniedHookSpecificOutput{},
			full:      PermissionDeniedHookSpecificOutput{Retry: &retry},
			required:  []string{"hookEventName"},
			optional:  []string{"retry"},
		},
		{
			name:      "PreModelSwitchHookSpecificOutput",
			eventName: "PreModelSwitch",
			zero:      PreModelSwitchHookSpecificOutput{},
			full: PreModelSwitchHookSpecificOutput{
				PermissionDecision:       ModelSwitchPermissionDecisionAllow,
				PermissionDecisionReason: "opus is fine",
			},
			required: []string{"hookEventName"},
			optional: []string{"permissionDecision", "permissionDecisionReason"},
		},
		{
			name:      "PostModelSwitchHookSpecificOutput",
			eventName: "PostModelSwitch",
			zero:      PostModelSwitchHookSpecificOutput{},
			full:      PostModelSwitchHookSpecificOutput{AdditionalContext: "ctx"},
			required:  []string{"hookEventName"},
			optional:  []string{"additionalContext"},
		},
		{
			name:      "ElicitationHookSpecificOutput",
			eventName: "Elicitation",
			zero:      ElicitationHookSpecificOutput{},
			full: ElicitationHookSpecificOutput{
				Action:  ElicitationActionAccept,
				Content: map[string]interface{}{"field": "value"},
			},
			required: []string{"hookEventName"},
			optional: []string{"action", "content"},
		},
		{
			name:      "ElicitationResultHookSpecificOutput",
			eventName: "ElicitationResult",
			zero:      ElicitationResultHookSpecificOutput{},
			full: ElicitationResultHookSpecificOutput{
				Action:  ElicitationActionDecline,
				Content: map[string]interface{}{"field": "value"},
			},
			required: []string{"hookEventName"},
			optional: []string{"action", "content"},
		},
		{
			name:      "CwdChangedHookSpecificOutput",
			eventName: "CwdChanged",
			zero:      CwdChangedHookSpecificOutput{},
			full:      CwdChangedHookSpecificOutput{WatchPaths: []string{"/repo"}},
			required:  []string{"hookEventName"},
			optional:  []string{"watchPaths"},
		},
		{
			name:      "FileChangedHookSpecificOutput",
			eventName: "FileChanged",
			zero:      FileChangedHookSpecificOutput{},
			full:      FileChangedHookSpecificOutput{WatchPaths: []string{"/repo"}},
			required:  []string{"hookEventName"},
			optional:  []string{"watchPaths"},
		},
		{
			name:      "WorktreeCreateHookSpecificOutput",
			eventName: "WorktreeCreate",
			zero:      WorktreeCreateHookSpecificOutput{},
			full:      WorktreeCreateHookSpecificOutput{WorktreePath: "/repo/.worktrees/abc"},
			required:  []string{"hookEventName", "worktreePath"},
			optional:  []string{},
		},
		{
			name:      "MessageDisplayHookSpecificOutput",
			eventName: "MessageDisplay",
			zero:      MessageDisplayHookSpecificOutput{},
			full:      MessageDisplayHookSpecificOutput{DisplayContent: "(hidden)"},
			required:  []string{"hookEventName"},
			optional:  []string{"displayContent"},
		},
	}
}

// TestHookSpecificOutputKeySets pins the key set of every hook-specific
// output type, mirroring python test_output_key_sets_match_typescript and
// test_hook_specific_output_members_have_distinct_event_names (Python SDK
// #1331): required keys are present on the zero value, optional keys only
// when set, and each member is discriminated by its own event name.
func TestHookSpecificOutputKeySets(t *testing.T) {
	eventNames := map[string]bool{}
	for _, shape := range hookSpecificOutputShapes() {
		fullKeys := append(append([]string{}, shape.required...), shape.optional...)
		assertKeysEqual(t, shape.name+" full", jsonKeys(t, shape.full), keySet(fullKeys...))
		assertKeysEqual(t, shape.name+" zero", jsonKeys(t, shape.zero), keySet(shape.required...))

		// The hookEventName discriminator has the member's own event value
		// and is distinct across the union.
		raw := HookSpecificOutputMap(shape.full)
		if raw["hookEventName"] != shape.eventName {
			t.Errorf("%s: hookEventName: got %v, want %q", shape.name, raw["hookEventName"], shape.eventName)
		}
		if eventNames[shape.eventName] {
			t.Errorf("duplicate event name %q across hook-specific outputs", shape.eventName)
		}
		eventNames[shape.eventName] = true
	}
	if len(eventNames) != len(hookSpecificOutputShapes()) {
		t.Errorf("expected %d distinct event names, got %d", len(hookSpecificOutputShapes()), len(eventNames))
	}
}

// TestHookSpecificOutputLiterals pins the decision/action literal sets,
// mirroring python test_output_literals (Python SDK #1331).
func TestHookSpecificOutputLiterals(t *testing.T) {
	preModelSwitchDecisions := map[ModelSwitchPermissionDecision]bool{
		ModelSwitchPermissionDecisionAllow: true,
		ModelSwitchPermissionDecisionDeny:  true,
		ModelSwitchPermissionDecisionAsk:   true,
	}
	if len(preModelSwitchDecisions) != 3 {
		t.Errorf("ModelSwitchPermissionDecision: got %v", preModelSwitchDecisions)
	}
	preToolUseDecisions := map[HookPermissionDecision]bool{
		HookPermissionDecisionAllow: true,
		HookPermissionDecisionDeny:  true,
		HookPermissionDecisionAsk:   true,
		HookPermissionDecisionDefer: true,
	}
	if len(preToolUseDecisions) != 4 {
		t.Errorf("HookPermissionDecision: got %v", preToolUseDecisions)
	}
	actions := map[ElicitationAction]bool{
		ElicitationActionAccept:  true,
		ElicitationActionDecline: true,
		ElicitationActionCancel:  true,
	}
	if len(actions) != 3 {
		t.Errorf("ElicitationAction: got %v", actions)
	}
}

// TestUserPromptSubmitOutputSessionTitleAndSuppress mirrors the python test
// of the same name (Python SDK #1331): sessionTitle and
// suppressOriginalPrompt reach the wire.
func TestUserPromptSubmitOutputSessionTitleAndSuppress(t *testing.T) {
	suppress := true
	output := UserPromptSubmitHookSpecificOutput{
		SessionTitle:           "Refactor auth",
		SuppressOriginalPrompt: &suppress,
	}
	m := HookSpecificOutputMap(output)
	if m["sessionTitle"] != "Refactor auth" {
		t.Errorf("sessionTitle: got %v", m["sessionTitle"])
	}
	if m["suppressOriginalPrompt"] != true {
		t.Errorf("suppressOriginalPrompt: got %v", m["suppressOriginalPrompt"])
	}
	if m["hookEventName"] != "UserPromptSubmit" {
		t.Errorf("hookEventName: got %v", m["hookEventName"])
	}
}

// TestSessionStartOutputFields mirrors the python test of the same name
// (Python SDK #1331).
func TestSessionStartOutputFields(t *testing.T) {
	reload := true
	output := SessionStartHookSpecificOutput{
		AdditionalContext:  "ctx",
		InitialUserMessage: "Run the test suite",
		SessionTitle:       "auth-refactor",
		WatchPaths:         []string{"/repo/.envrc"},
		ReloadSkills:       &reload,
	}
	m := HookSpecificOutputMap(output)
	watch, ok := m["watchPaths"].([]interface{})
	if !ok || len(watch) != 1 || watch[0] != "/repo/.envrc" {
		t.Errorf("watchPaths: got %v", m["watchPaths"])
	}
	if m["reloadSkills"] != true {
		t.Errorf("reloadSkills: got %v", m["reloadSkills"])
	}
	if m["initialUserMessage"] != "Run the test suite" {
		t.Errorf("initialUserMessage: got %v", m["initialUserMessage"])
	}
	if m["sessionTitle"] != "auth-refactor" {
		t.Errorf("sessionTitle: got %v", m["sessionTitle"])
	}
}

// TestHookSpecificOutputMapInHookOutput drives a typed output through the
// HookOutput bridge end-to-end: HookSpecificOutputMap is the Go adaptation
// of Python putting its TypedDicts straight into HookJSONOutput.
func TestHookSpecificOutputMapInHookOutput(t *testing.T) {
	output := HookOutput{
		Continue:           func() *bool { v := false; return &v }(),
		HookSpecificOutput: HookSpecificOutputMap(StopHookSpecificOutput{AdditionalContext: "one more turn"}),
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want := `{"continue":false,"hookSpecificOutput":{"additionalContext":"one more turn","hookEventName":"Stop"}}`
	if string(data) != want {
		t.Errorf("wire JSON: got %s, want %s", data, want)
	}
}

// TestHookInputPromptIDAndEffort mirrors python
// test_base_hook_input_prompt_id_and_effort (Python SDK #1331).
func TestHookInputPromptIDAndEffort(t *testing.T) {
	input := HookInput{
		SessionID:      "sess-1",
		TranscriptPath: "/tmp/transcript",
		CWD:            "/home/user",
		PromptID:       "550e8400-e29b-41d4-a716-446655440000",
		Effort:         &HookEffort{Level: EffortLevelHigh},
		HookEventName:  "PreToolUse",
		ToolName:       "Bash",
		ToolInput:      map[string]interface{}{"command": "ls"},
		ToolUseID:      "toolu_1",
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if raw["prompt_id"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("prompt_id: got %v", raw["prompt_id"])
	}
	effort, ok := raw["effort"].(map[string]interface{})
	if !ok || effort["level"] != "high" {
		t.Errorf("effort: got %v", raw["effort"])
	}

	// The new fields are optional: a minimal input omits them.
	minimal, err := json.Marshal(HookInput{HookEventName: "PreToolUse"})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	for _, key := range []string{"prompt_id", "effort", "duration_ms", "last_assistant_message", "background_tasks", "session_crons"} {
		if strings.Contains(string(minimal), key) {
			t.Errorf("minimal input should omit %s: %s", key, minimal)
		}
	}
}

// TestHookInputDurationMS mirrors python test_post_tool_use_duration_ms
// (Python SDK #1331): duration_ms is an optional field of the tool-use
// lifecycle inputs.
func TestHookInputDurationMS(t *testing.T) {
	input := HookInput{
		SessionID:      "sess-1",
		TranscriptPath: "/tmp/transcript",
		CWD:            "/home/user",
		HookEventName:  "PostToolUse",
		ToolName:       "Bash",
		ToolInput:      map[string]interface{}{"command": "ls"},
		ToolResponse:   map[string]interface{}{"stdout": ""},
		ToolUseID:      "toolu_1",
		DurationMS:     12,
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if raw["duration_ms"] != float64(12) {
		t.Errorf("duration_ms: got %v", raw["duration_ms"])
	}
}

// TestHookInputBackgroundWork mirrors python
// test_stop_hook_input_background_work (Python SDK #1331): Stop inputs carry
// last_assistant_message, background_tasks, and session_crons.
func TestHookInputBackgroundWork(t *testing.T) {
	input := HookInput{
		SessionID:            "sess-1",
		TranscriptPath:       "/tmp/transcript",
		CWD:                  "/home/user",
		HookEventName:        "Stop",
		StopHookActive:       false,
		LastAssistantMessage: "Done.",
		BackgroundTasks: []BackgroundTaskSummary{{
			ID:          "task-1",
			Type:        "shell",
			Status:      "running",
			Description: "npm test",
			Command:     "npm test",
		}},
		SessionCrons: []SessionCronSummary{{
			ID:        "cron-1",
			Schedule:  "0 9 * * 1-5",
			Recurring: true,
			Prompt:    "check CI",
		}},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var roundTrip HookInput
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if roundTrip.LastAssistantMessage != "Done." {
		t.Errorf("last_assistant_message: got %q", roundTrip.LastAssistantMessage)
	}
	if len(roundTrip.BackgroundTasks) != 1 || roundTrip.BackgroundTasks[0].Command != "npm test" {
		t.Errorf("background_tasks: got %+v", roundTrip.BackgroundTasks)
	}
	if len(roundTrip.SessionCrons) != 1 || !roundTrip.SessionCrons[0].Recurring {
		t.Errorf("session_crons: got %+v", roundTrip.SessionCrons)
	}
}

// TestBackgroundWorkSummaryKeySets mirrors python
// test_background_work_summary_key_sets (Python SDK #1331).
func TestBackgroundWorkSummaryKeySets(t *testing.T) {
	assertKeysEqual(t, "BackgroundTaskSummary full", jsonKeys(t, BackgroundTaskSummary{
		ID:          "task-1",
		Type:        "shell",
		Status:      "running",
		Description: "npm test",
		Command:     "npm test",
		AgentType:   "reviewer",
		Server:      "docs",
		Tool:        "search",
		Name:        "audit",
	}), keySet("id", "type", "status", "description", "command", "agent_type", "server", "tool", "name"))
	assertKeysEqual(t, "BackgroundTaskSummary minimal", jsonKeys(t, BackgroundTaskSummary{
		ID: "task-1", Type: "shell", Status: "running", Description: "npm test",
	}), keySet("id", "type", "status", "description"))

	assertKeysEqual(t, "SessionCronSummary", jsonKeys(t, SessionCronSummary{
		ID: "cron-1", Schedule: "0 9 * * 1-5", Recurring: true, Prompt: "check CI",
	}), keySet("id", "schedule", "recurring", "prompt"))

	assertKeysEqual(t, "HookEffort", jsonKeys(t, HookEffort{Level: "high"}), keySet("level"))
}

// TestSandboxIgnoreViolationsIsCommandPatternMap mirrors python
// test_sandbox_ignore_violations_is_command_pattern_map (Python SDK #1380):
// ignoreViolations matches the CLI schema — command pattern -> substrings.
func TestSandboxIgnoreViolationsIsCommandPatternMap(t *testing.T) {
	settings := SandboxSettings{
		Enabled:          true,
		IgnoreViolations: SandboxIgnoreViolations{"*": []string{"/etc/hosts"}, "npm test": []string{"registry.npmjs.org"}},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	ignore, ok := raw["ignoreViolations"].(map[string]interface{})
	if !ok {
		t.Fatalf("ignoreViolations should be a map, got %T", raw["ignoreViolations"])
	}
	if _, ok := ignore["file"]; ok {
		t.Errorf("legacy file key should not exist: %v", ignore)
	}
	if _, ok := ignore["network"]; ok {
		t.Errorf("legacy network key should not exist: %v", ignore)
	}
	want := map[string][]string{
		"*":        {"/etc/hosts"},
		"npm test": {"registry.npmjs.org"},
	}
	for pattern, substrings := range want {
		got, ok := ignore[pattern].([]interface{})
		if !ok || len(got) != len(substrings) {
			t.Errorf("pattern %q: got %v", pattern, ignore[pattern])
			continue
		}
		for i, s := range substrings {
			if got[i] != s {
				t.Errorf("pattern %q[%d]: got %v, want %q", pattern, i, got[i], s)
			}
		}
	}

	// Round-trip preserves arbitrary command-pattern keys.
	var roundTrip SandboxSettings
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if roundTrip.IgnoreViolations["npm test"][0] != "registry.npmjs.org" {
		t.Errorf("round-trip: got %v", roundTrip.IgnoreViolations)
	}

	// The zero value omits ignoreViolations entirely.
	empty, err := json.Marshal(SandboxSettings{Enabled: true})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(empty), "ignoreViolations") {
		t.Errorf("empty IgnoreViolations should be omitted: %s", empty)
	}
}
