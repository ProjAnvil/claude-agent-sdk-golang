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
