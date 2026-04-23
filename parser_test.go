package claude

import (
	"testing"

	"github.com/ProjAnvil/claude-agent-sdk-golang/testutil"
)

func TestParseUserMessage(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleUserMessage)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	if userMsg.UUID != "user-123" {
		t.Errorf("Expected UUID 'user-123', got '%s'", userMsg.UUID)
	}

	content, ok := userMsg.Content.(string)
	if !ok {
		t.Fatalf("Expected string content, got %T", userMsg.Content)
	}

	if content != "Hello, Claude!" {
		t.Errorf("Expected content 'Hello, Claude!', got '%s'", content)
	}
}

func TestParseAssistantMessage(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleAssistantMessage)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if assistantMsg.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("Expected model 'claude-sonnet-4-5-20250929', got '%s'", assistantMsg.Model)
	}

	if len(assistantMsg.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(assistantMsg.Content))
	}

	textBlock, ok := assistantMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected *TextBlock, got %T", assistantMsg.Content[0])
	}

	if textBlock.Text != "Hello! How can I help you today?" {
		t.Errorf("Unexpected text: %s", textBlock.Text)
	}
}

func TestParseToolUseMessage(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleToolUseMessage)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if len(assistantMsg.Content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %d", len(assistantMsg.Content))
	}

	toolUse, ok := assistantMsg.Content[1].(*ToolUseBlock)
	if !ok {
		t.Fatalf("Expected *ToolUseBlock, got %T", assistantMsg.Content[1])
	}

	if toolUse.Name != "Bash" {
		t.Errorf("Expected tool name 'Bash', got '%s'", toolUse.Name)
	}

	if toolUse.ID != "tool-123" {
		t.Errorf("Expected tool ID 'tool-123', got '%s'", toolUse.ID)
	}
}

func TestParseResultMessage(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleResultMessage)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	resultMsg, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("Expected *ResultMessage, got %T", msg)
	}

	if resultMsg.SessionID != "session-123" {
		t.Errorf("Expected session ID 'session-123', got '%s'", resultMsg.SessionID)
	}

	if resultMsg.DurationMS != 1500 {
		t.Errorf("Expected duration 1500ms, got %d", resultMsg.DurationMS)
	}

	if resultMsg.TotalCostUSD != 0.0025 {
		t.Errorf("Expected cost 0.0025, got %f", resultMsg.TotalCostUSD)
	}

	if resultMsg.IsError {
		t.Error("Expected IsError to be false")
	}
}

func TestParseStreamEvent(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleStreamEvent)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	streamEvent, ok := msg.(*StreamEvent)
	if !ok {
		t.Fatalf("Expected *StreamEvent, got %T", msg)
	}

	if streamEvent.UUID != "event-123" {
		t.Errorf("Expected UUID 'event-123', got '%s'", streamEvent.UUID)
	}

	if streamEvent.SessionID != "session-123" {
		t.Errorf("Expected session ID 'session-123', got '%s'", streamEvent.SessionID)
	}
}

func TestParseThinkingBlock(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleThinkingMessage)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if len(assistantMsg.Content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %d", len(assistantMsg.Content))
	}

	thinkingBlock, ok := assistantMsg.Content[0].(*ThinkingBlock)
	if !ok {
		t.Fatalf("Expected *ThinkingBlock, got %T", assistantMsg.Content[0])
	}

	if thinkingBlock.Thinking != "Let me think about this..." {
		t.Errorf("Unexpected thinking: %s", thinkingBlock.Thinking)
	}

	if thinkingBlock.Signature != "sig-123" {
		t.Errorf("Expected signature 'sig-123', got '%s'", thinkingBlock.Signature)
	}
}

func TestParseInvalidMessage(t *testing.T) {
	// Missing type field
	_, err := ParseMessage(map[string]interface{}{
		"content": "test",
	})
	if err == nil {
		t.Error("Expected error for missing type field")
	}

	// Unknown type - should return nil for forward compatibility
	msg, err := ParseMessage(map[string]interface{}{
		"type": "unknown",
	})
	if err != nil {
		t.Errorf("Expected no error for unknown type, got: %v", err)
	}
	if msg != nil {
		t.Error("Expected nil message for unknown type")
	}

	// Nil data
	_, err = ParseMessage(nil)
	if err == nil {
		t.Error("Expected error for nil data")
	}
}

func TestParseSystemMessage(t *testing.T) {
	msg, err := ParseMessage(testutil.SampleSystemMessage)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	systemMsg, ok := msg.(*SystemMessage)
	if !ok {
		t.Fatalf("Expected *SystemMessage, got %T", msg)
	}

	if systemMsg.Subtype != "init" {
		t.Errorf("Expected subtype 'init', got '%s'", systemMsg.Subtype)
	}
}

func TestParseUserMessageWithToolResult(t *testing.T) {
	data := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "tool_789",
					"content":     "File contents here",
				},
			},
		},
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	blocks, ok := userMsg.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("Expected []ContentBlock, got %T", userMsg.Content)
	}

	if len(blocks) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(blocks))
	}

	toolResult, ok := blocks[0].(*ToolResultBlock)
	if !ok {
		t.Fatalf("Expected *ToolResultBlock, got %T", blocks[0])
	}

	if toolResult.ToolUseID != "tool_789" {
		t.Errorf("Expected ToolUseID 'tool_789', got '%s'", toolResult.ToolUseID)
	}

	if toolResult.Content != "File contents here" {
		t.Errorf("Expected content 'File contents here', got '%v'", toolResult.Content)
	}
}

func TestParseUserMessageWithToolResultError(t *testing.T) {
	data := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "tool_error",
					"content":     "File not found",
					"is_error":    true,
				},
			},
		},
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	blocks, ok := userMsg.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("Expected []ContentBlock, got %T", userMsg.Content)
	}

	toolResult, ok := blocks[0].(*ToolResultBlock)
	if !ok {
		t.Fatalf("Expected *ToolResultBlock, got %T", blocks[0])
	}

	if toolResult.ToolUseID != "tool_error" {
		t.Errorf("Expected ToolUseID 'tool_error', got '%s'", toolResult.ToolUseID)
	}

	if toolResult.Content != "File not found" {
		t.Errorf("Expected content 'File not found', got '%v'", toolResult.Content)
	}

	if !toolResult.IsError {
		t.Error("Expected IsError to be true")
	}
}

func TestParseUserMessageWithMixedContent(t *testing.T) {
	data := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Here's what I found:"},
				map[string]interface{}{
					"type":  "tool_use",
					"id":    "use_1",
					"name":  "Search",
					"input": map[string]interface{}{"query": "test"},
				},
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "use_1",
					"content":     "Search results",
				},
				map[string]interface{}{"type": "text", "text": "What do you think?"},
			},
		},
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	blocks, ok := userMsg.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("Expected []ContentBlock, got %T", userMsg.Content)
	}

	if len(blocks) != 4 {
		t.Fatalf("Expected 4 content blocks, got %d", len(blocks))
	}

	if _, ok := blocks[0].(*TextBlock); !ok {
		t.Errorf("Block 0 should be TextBlock, got %T", blocks[0])
	}
	if _, ok := blocks[1].(*ToolUseBlock); !ok {
		t.Errorf("Block 1 should be ToolUseBlock, got %T", blocks[1])
	}
	if _, ok := blocks[2].(*ToolResultBlock); !ok {
		t.Errorf("Block 2 should be ToolResultBlock, got %T", blocks[2])
	}
	if _, ok := blocks[3].(*TextBlock); !ok {
		t.Errorf("Block 3 should be TextBlock, got %T", blocks[3])
	}
}

func TestParseUserMessageInsideSubagent(t *testing.T) {
	data := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Hello"},
			},
		},
		"parent_tool_use_id": "toolu_01Xrwd5Y13sEHtzScxR77So8",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	if userMsg.ParentToolUseID != "toolu_01Xrwd5Y13sEHtzScxR77So8" {
		t.Errorf("Expected ParentToolUseID 'toolu_01Xrwd5Y13sEHtzScxR77So8', got '%s'", userMsg.ParentToolUseID)
	}
}

func TestParseUserMessageWithToolUseResult(t *testing.T) {
	toolResultData := map[string]interface{}{
		"filePath":     "/path/to/file.py",
		"oldString":    "old code",
		"newString":    "new code",
		"originalFile": "full file contents",
		"structuredPatch": []interface{}{
			map[string]interface{}{
				"oldStart": float64(33),
				"oldLines": float64(7),
				"newStart": float64(33),
				"newLines": float64(7),
				"lines": []interface{}{
					"   # comment",
					"-      old line",
					"+      new line",
				},
			},
		},
		"userModified": false,
		"replaceAll":   false,
	}

	data := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"tool_use_id": "toolu_vrtx_01KXWexk3NJdwkjWzPMGQ2F1",
					"type":        "tool_result",
					"content":     "The file has been updated.",
				},
			},
		},
		"parent_tool_use_id": nil,
		"session_id":         "84afb479-17ae-49af-8f2b-666ac2530c3a",
		"uuid":               "2ace3375-1879-48a0-a421-6bce25a9295a",
		"tool_use_result":    toolResultData,
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	if userMsg.UUID != "2ace3375-1879-48a0-a421-6bce25a9295a" {
		t.Errorf("Expected UUID '2ace3375-1879-48a0-a421-6bce25a9295a', got '%s'", userMsg.UUID)
	}

	tur := userMsg.ToolUseResult
	if tur == nil {
		t.Fatal("Expected ToolUseResult to be non-nil")
	}

	if tur["filePath"] != "/path/to/file.py" {
		t.Errorf("Expected filePath '/path/to/file.py', got '%v'", tur["filePath"])
	}

	patch, ok := tur["structuredPatch"].([]interface{})
	if !ok {
		t.Fatalf("Expected structuredPatch to be []interface{}, got %T", tur["structuredPatch"])
	}

	firstPatch := patch[0].(map[string]interface{})
	if firstPatch["oldStart"] != float64(33) {
		t.Errorf("Expected oldStart 33, got %v", firstPatch["oldStart"])
	}
}

func TestParseUserMessageWithStringContentAndToolUseResult(t *testing.T) {
	toolResultData := map[string]interface{}{
		"filePath":     "/path/to/file.py",
		"userModified": true,
	}

	data := map[string]interface{}{
		"type":            "user",
		"message":         map[string]interface{}{"content": "Simple string content"},
		"tool_use_result": toolResultData,
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	userMsg, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}

	content, ok := userMsg.Content.(string)
	if !ok {
		t.Fatalf("Expected string content, got %T", userMsg.Content)
	}
	if content != "Simple string content" {
		t.Errorf("Unexpected content: %s", content)
	}

	tur := userMsg.ToolUseResult
	if tur == nil {
		t.Fatal("Expected ToolUseResult to be non-nil")
	}
	if tur["userModified"] != true {
		t.Errorf("Expected userModified true, got %v", tur["userModified"])
	}
}

func TestParseAssistantMessageInsideSubagent(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Hello"},
			},
			"model": "claude-opus-4-1-20250805",
		},
		"parent_tool_use_id": "toolu_01Xrwd5Y13sEHtzScxR77So8",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if assistantMsg.ParentToolUseID != "toolu_01Xrwd5Y13sEHtzScxR77So8" {
		t.Errorf("Expected ParentToolUseID 'toolu_01Xrwd5Y13sEHtzScxR77So8', got '%s'", assistantMsg.ParentToolUseID)
	}
}

func TestParseAssistantMessageWithAuthenticationError(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Invalid API key"},
			},
			"model": "<synthetic>",
		},
		"session_id": "test-session",
		"error":      "authentication_failed",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if assistantMsg.Error != "authentication_failed" {
		t.Errorf("Expected error 'authentication_failed', got '%s'", assistantMsg.Error)
	}
}

func TestParseAssistantMessageWithUnknownError(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "API Error: 500",
				},
			},
			"model": "<synthetic>",
		},
		"session_id": "test-session",
		"error":      "unknown",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if assistantMsg.Error != "unknown" {
		t.Errorf("Expected error 'unknown', got '%s'", assistantMsg.Error)
	}
}

func TestParseAssistantMessageWithRateLimitError(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Rate limit exceeded"},
			},
			"model": "<synthetic>",
		},
		"error": "rate_limit",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	assistantMsg, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}

	if assistantMsg.Error != "rate_limit" {
		t.Errorf("Expected error 'rate_limit', got '%s'", assistantMsg.Error)
	}
}

func TestMessageParseErrorContainsData(t *testing.T) {
	// Use a malformed known type (missing required fields) to trigger error
	data := map[string]interface{}{"type": "assistant"}

	_, err := ParseMessage(data)
	if err == nil {
		t.Fatal("Expected ParseMessage to fail")
	}

	parseErr, ok := err.(*MessageParseError)
	if !ok {
		t.Fatalf("Expected MessageParseError, got %T", err)
	}

	if parseErr.Data == nil {
		t.Error("Expected Data to be present in error")
	}

	// Verify the data contains the type
	if val, ok := parseErr.Data["type"].(string); !ok || val != "assistant" {
		t.Errorf("Expected Data['type']='assistant', got %v", parseErr.Data["type"])
	}
}

func TestParseSystemMessageMissingFields(t *testing.T) {
	_, err := ParseMessage(map[string]interface{}{
		"type": "system",
	})
	if err == nil {
		t.Error("Expected error for missing subtype in system message")
	}
}

func TestParseResultMessageMissingFields(t *testing.T) {
	_, err := ParseMessage(map[string]interface{}{
		"type": "result",
	})
	if err == nil {
		t.Error("Expected error for missing fields in result message")
	}
}

func TestParseStreamEventMissingFields(t *testing.T) {
	_, err := ParseMessage(map[string]interface{}{
		"type": "stream_event",
	})
	if err == nil {
		t.Error("Expected error for missing fields in stream_event message")
	}
}

func TestParseResultMessageWithStopReason(t *testing.T) {
	// Test parsing a result message with stop_reason field.
	// The stop_reason field mirrors the Anthropic API's stop_reason on the
	// final assistant turn (e.g., "end_turn", "max_tokens", "tool_use").
	data := map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1000),
		"duration_api_ms": float64(500),
		"is_error":        false,
		"num_turns":       float64(2),
		"session_id":      "session_123",
		"stop_reason":     "end_turn",
		"result":          "Done",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	resultMsg, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("Expected *ResultMessage, got %T", msg)
	}

	if resultMsg.StopReason != "end_turn" {
		t.Errorf("Expected StopReason 'end_turn', got '%s'", resultMsg.StopReason)
	}

	if resultMsg.Result != "Done" {
		t.Errorf("Expected Result 'Done', got '%s'", resultMsg.Result)
	}
}

// ---- Tests for new message types added in v0.1.58–v0.1.65 ----

func TestParseMirrorErrorMessage(t *testing.T) {
	data := map[string]interface{}{
		"type":       "system",
		"subtype":    "mirror_error",
		"error":      "store is unavailable",
		"uuid":       "uuid-abc",
		"session_id": "sess-xyz",
		"key": map[string]interface{}{
			"project_key": "my-project",
			"session_id":  "sess-xyz",
			"subpath":     "sub/1",
		},
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	me, ok := msg.(*MirrorErrorMessage)
	if !ok {
		t.Fatalf("Expected *MirrorErrorMessage, got %T", msg)
	}
	if me.Error != "store is unavailable" {
		t.Errorf("Error mismatch: %s", me.Error)
	}
	if me.UUID != "uuid-abc" {
		t.Errorf("UUID mismatch: %s", me.UUID)
	}
	if me.SessionID != "sess-xyz" {
		t.Errorf("SessionID mismatch: %s", me.SessionID)
	}
	if me.Key == nil {
		t.Fatal("Key is nil")
	}
	if me.Key.ProjectKey != "my-project" {
		t.Errorf("Key.ProjectKey mismatch: %s", me.Key.ProjectKey)
	}
	if me.Key.Subpath != "sub/1" {
		t.Errorf("Key.Subpath mismatch: %s", me.Key.Subpath)
	}
}

func TestParseMirrorErrorMessageNoKey(t *testing.T) {
	data := map[string]interface{}{
		"type":    "system",
		"subtype": "mirror_error",
		"error":   "oops",
		"uuid":    "u1",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	me, ok := msg.(*MirrorErrorMessage)
	if !ok {
		t.Fatalf("Expected *MirrorErrorMessage, got %T", msg)
	}
	if me.Key != nil {
		t.Error("Expected Key to be nil when absent from payload")
	}
	if me.Error != "oops" {
		t.Errorf("Error mismatch: %s", me.Error)
	}
}

func TestParseAssistantMessageWithServerToolUse(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"id":   "msg_abc",
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{
					"type":  "server_tool_use",
					"id":    "stu_123",
					"name":  "web_search",
					"input": map[string]interface{}{"query": "Go testing"},
				},
			},
			"model":         "claude-opus-4-5",
			"stop_reason":   "tool_use",
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  float64(100),
				"output_tokens": float64(20),
			},
		},
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	am, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}
	if len(am.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(am.Content))
	}
	stu, ok := am.Content[0].(*ServerToolUseBlock)
	if !ok {
		t.Fatalf("Expected *ServerToolUseBlock, got %T", am.Content[0])
	}
	if stu.ID != "stu_123" {
		t.Errorf("ID mismatch: %s", stu.ID)
	}
	if string(stu.Name) != "web_search" {
		t.Errorf("Name mismatch: %s", stu.Name)
	}
}

func TestParseUserMessageWithAdvisorToolResult(t *testing.T) {
	data := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "advisor_tool_result",
					"tool_use_id": "stu_123",
					"content":     map[string]interface{}{"results": []interface{}{"result1"}},
				},
			},
		},
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	um, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("Expected *UserMessage, got %T", msg)
	}
	contentSlice, ok := um.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("Expected um.Content to be []ContentBlock, got %T", um.Content)
	}
	if len(contentSlice) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(contentSlice))
	}
	str, ok := contentSlice[0].(*ServerToolResultBlock)
	if !ok {
		t.Fatalf("Expected *ServerToolResultBlock, got %T", contentSlice[0])
	}
	if str.ToolUseID != "stu_123" {
		t.Errorf("ToolUseID mismatch: %s", str.ToolUseID)
	}
}

func TestParseResultMessageWithNullStopReason(t *testing.T) {
	// Test parsing a result message with explicit null stop_reason.
	// When stop_reason is null/missing, it should be an empty string.
	data := map[string]interface{}{
		"type":            "result",
		"subtype":         "error_max_turns",
		"duration_ms":     float64(1000),
		"duration_api_ms": float64(500),
		"is_error":        true,
		"num_turns":       float64(10),
		"session_id":      "session_123",
		"stop_reason":     nil,
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	resultMsg, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("Expected *ResultMessage, got %T", msg)
	}

	if resultMsg.StopReason != "" {
		t.Errorf("Expected empty StopReason for null value, got '%s'", resultMsg.StopReason)
	}
}

func TestParseResultMessageWithoutStopReason(t *testing.T) {
	// Test backward compatibility: parsing a result message without stop_reason field.
	// Older CLI output without the field should produce empty string.
	data := map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1000),
		"duration_api_ms": float64(500),
		"is_error":        false,
		"num_turns":       float64(2),
		"session_id":      "session_123",
	}

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	resultMsg, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("Expected *ResultMessage, got %T", msg)
	}

	if resultMsg.StopReason != "" {
		t.Errorf("Expected empty StopReason for missing field, got '%s'", resultMsg.StopReason)
	}
}

// TestParseAssistantMessageWithUsage tests that per-turn usage is preserved.
func TestParseAssistantMessageWithUsage(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "hi"}},
			"model":   "claude-opus-4-5",
			"usage": map[string]interface{}{
				"input_tokens":                float64(100),
				"output_tokens":               float64(50),
				"cache_read_input_tokens":     float64(2000),
				"cache_creation_input_tokens": float64(500),
			},
		},
	}
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	am, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", msg)
	}
	if am.Usage == nil {
		t.Fatal("Expected usage to be set")
	}
	if am.Usage["input_tokens"] != float64(100) {
		t.Errorf("Expected input_tokens=100, got %v", am.Usage["input_tokens"])
	}
}

// TestParseAssistantMessageWithoutUsage tests usage defaults to nil.
func TestParseAssistantMessageWithoutUsage(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "hi"}},
			"model":   "claude-opus-4-5",
		},
	}
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	am := msg.(*AssistantMessage)
	if am.Usage != nil {
		t.Errorf("Expected nil usage, got %v", am.Usage)
	}
}

// TestParseAssistantMessageWithAllFields tests id, stop_reason, session_id, uuid.
func TestParseAssistantMessageWithAllFields(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content":     []interface{}{map[string]interface{}{"type": "text", "text": "Hello"}},
			"model":       "claude-sonnet-4-5-20250929",
			"id":          "msg_01HRq7YZE3apPqSHydvG77Ve",
			"stop_reason": "end_turn",
			"usage":       map[string]interface{}{"input_tokens": float64(10), "output_tokens": float64(5)},
		},
		"session_id": "fdf2d90a-fd9e-4736-ae35-806edd13643f",
		"uuid":       "0dbd2453-1209-4fe9-bd51-4102f64e33df",
	}
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	am := msg.(*AssistantMessage)
	if am.MessageID != "msg_01HRq7YZE3apPqSHydvG77Ve" {
		t.Errorf("Expected MessageID, got '%s'", am.MessageID)
	}
	if am.StopReason != "end_turn" {
		t.Errorf("Expected StopReason='end_turn', got '%s'", am.StopReason)
	}
	if am.SessionID != "fdf2d90a-fd9e-4736-ae35-806edd13643f" {
		t.Errorf("Expected SessionID, got '%s'", am.SessionID)
	}
	if am.UUID != "0dbd2453-1209-4fe9-bd51-4102f64e33df" {
		t.Errorf("Expected UUID, got '%s'", am.UUID)
	}
}

// TestParseAssistantMessageOptionalFieldsAbsent tests defaults to zero values.
func TestParseAssistantMessageOptionalFieldsAbsent(t *testing.T) {
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "hi"}},
			"model":   "claude-opus-4-5",
		},
	}
	msg, _ := ParseMessage(data)
	am := msg.(*AssistantMessage)
	if am.MessageID != "" {
		t.Errorf("Expected empty MessageID, got '%s'", am.MessageID)
	}
	if am.StopReason != "" {
		t.Errorf("Expected empty StopReason, got '%s'", am.StopReason)
	}
	if am.SessionID != "" {
		t.Errorf("Expected empty SessionID, got '%s'", am.SessionID)
	}
	if am.UUID != "" {
		t.Errorf("Expected empty UUID, got '%s'", am.UUID)
	}
}

// TestParseResultMessageWithModelUsage tests modelUsage, permission_denials, uuid.
func TestParseResultMessageWithModelUsage(t *testing.T) {
	data := map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(3000),
		"duration_api_ms": float64(2000),
		"is_error":        false,
		"num_turns":       float64(1),
		"session_id":      "fdf2d90a-fd9e-4736-ae35-806edd13643f",
		"result":          "Hello",
		"modelUsage": map[string]interface{}{
			"claude-sonnet-4-5-20250929": map[string]interface{}{
				"inputTokens":  float64(3),
				"outputTokens": float64(24),
				"costUSD":      0.0106,
			},
		},
		"permission_denials": []interface{}{},
		"uuid":               "d379c496-f33a-4ea4-b920-3c5483baa6f7",
	}
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	rm := msg.(*ResultMessage)
	if rm.ModelUsage == nil {
		t.Fatal("Expected ModelUsage to be set")
	}
	if rm.UUID != "d379c496-f33a-4ea4-b920-3c5483baa6f7" {
		t.Errorf("Expected UUID, got '%s'", rm.UUID)
	}
}

// TestParseResultMessageOptionalFieldsAbsent tests defaults.
func TestParseResultMessageOptionalFieldsAbsent(t *testing.T) {
	data := map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1000),
		"duration_api_ms": float64(500),
		"is_error":        false,
		"num_turns":       float64(1),
		"session_id":      "session_123",
	}
	msg, _ := ParseMessage(data)
	rm := msg.(*ResultMessage)
	if rm.ModelUsage != nil {
		t.Errorf("Expected nil ModelUsage, got %v", rm.ModelUsage)
	}
	if rm.PermissionDenials != nil {
		t.Errorf("Expected nil PermissionDenials")
	}
	if rm.Errors != nil {
		t.Errorf("Expected nil Errors")
	}
	if rm.UUID != "" {
		t.Errorf("Expected empty UUID")
	}
}

// TestParseResultMessageWithErrors tests errors field.
func TestParseResultMessageWithErrors(t *testing.T) {
	data := map[string]interface{}{
		"type":            "result",
		"subtype":         "error_during_execution",
		"duration_ms":     float64(5000),
		"duration_api_ms": float64(3000),
		"is_error":        true,
		"num_turns":       float64(3),
		"session_id":      "session_456",
		"errors": []interface{}{
			"Tool execution failed: permission denied",
			"Unable to write to /etc/hosts",
		},
		"uuid": "err-uuid-789",
	}
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	rm := msg.(*ResultMessage)
	if len(rm.Errors) != 2 {
		t.Fatalf("Expected 2 errors, got %d", len(rm.Errors))
	}
	if rm.Errors[0] != "Tool execution failed: permission denied" {
		t.Errorf("Unexpected first error: %s", rm.Errors[0])
	}
	if rm.UUID != "err-uuid-789" {
		t.Errorf("Expected UUID, got '%s'", rm.UUID)
	}
}

// TestParseRateLimitEvent tests typed RateLimitEvent parsing.
func TestParseRateLimitEvent(t *testing.T) {
	data := map[string]interface{}{
		"type": "rate_limit_event",
		"rate_limit_info": map[string]interface{}{
			"status":        "allowed_warning",
			"resetsAt":      float64(1700000000),
			"rateLimitType": "five_hour",
			"utilization":   0.91,
		},
		"uuid":       "abc-123",
		"session_id": "session_xyz",
	}
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	rle, ok := msg.(*RateLimitEvent)
	if !ok {
		t.Fatalf("Expected *RateLimitEvent, got %T", msg)
	}
	if rle.UUID != "abc-123" {
		t.Errorf("Expected UUID 'abc-123', got '%s'", rle.UUID)
	}
	if rle.SessionID != "session_xyz" {
		t.Errorf("Expected SessionID 'session_xyz', got '%s'", rle.SessionID)
	}
	if rle.RateLimitInfo.Status != RateLimitStatusAllowedWarning {
		t.Errorf("Expected status 'allowed_warning', got '%s'", rle.RateLimitInfo.Status)
	}
	if rle.RateLimitInfo.ResetsAt == nil || *rle.RateLimitInfo.ResetsAt != 1700000000 {
		t.Errorf("Expected ResetsAt=1700000000, got %v", rle.RateLimitInfo.ResetsAt)
	}
	if rle.RateLimitInfo.RateLimitType != RateLimitTypeFiveHour {
		t.Errorf("Expected RateLimitType 'five_hour', got '%s'", rle.RateLimitInfo.RateLimitType)
	}
	if rle.RateLimitInfo.Utilization == nil || *rle.RateLimitInfo.Utilization != 0.91 {
		t.Errorf("Expected Utilization=0.91, got %v", rle.RateLimitInfo.Utilization)
	}
}
