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

	// Unknown type
	_, err = ParseMessage(map[string]interface{}{
		"type": "unknown",
	})
	if err == nil {
		t.Error("Expected error for unknown type")
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
	data := map[string]interface{}{"type": "unknown", "some": "data"}

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

	if val, ok := parseErr.Data["some"].(string); !ok || val != "data" {
		t.Errorf("Expected Data['some']='data', got %v", parseErr.Data["some"])
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
