package claude

// mcp_large_output_test.go — ported from Python SDK's test_mcp_large_output.py
// (TestToolResultParsing / TestPersistedOutputDetectionHelper).
//
// The transport-layer half of that file (MAX_MCP_OUTPUT_TOKENS passthrough,
// env inheritance, layer-2 boundary) lives in
// internal/transport/mcp_large_output_test.go. This file covers the
// message-parser half: after a layer-2 spill the CLI emits a
// <persisted-output> tag plus a 2 KB preview instead of the full tool result,
// and the SDK parser must surface that content unchanged so callers can
// detect the degraded path and warn users.
//
// The Python tests test_inline_content_preserved, test_error_tool_result_flagged
// and test_normal_tool_result_not_flagged are already covered here by
// TestParseUserMessageWithToolResult and TestParseUserMessageWithToolResultError
// in parser_test.go.

import (
	"strings"
	"testing"
)

// layer2ThresholdChars mirrors the CLI's layer-2 spill threshold
// (DEFAULT_MAX_RESULT_SIZE_CHARS = 50000 in toolLimits.ts).
const layer2ThresholdChars = 50_000

// persistedContent is what the CLI emits after a layer-2 spill:
// <persisted-output> tag + 2 KB preview (PREVIEW_SIZE_BYTES = 2000).
var persistedContent = "<persisted-output>\n" +
	"Output too large (73.0KB). Full output saved to: /tmp/.claude/tool-results/abc123.txt\n" +
	"\nPreview (first 2KB):\n" + strings.Repeat("x", 2000) + "\n...\n</persisted-output>"

// inlineContent is below the layer-2 threshold — passed inline by the CLI.
var inlineContent = strings.Repeat("x", 1000)

func userMessageWithToolResult(content string, isError bool) map[string]interface{} {
	return map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "toolu_01ABC",
					"content":     content,
					"is_error":    isError,
				},
			},
		},
		"parent_tool_use_id": nil,
		"tool_use_result":    nil,
		"uuid":               "test-uuid-1234",
	}
}

// parseToolResultBlock parses a user message carrying one tool_result block
// and returns that block.
func parseToolResultBlock(t *testing.T, content string, isError bool) *ToolResultBlock {
	t.Helper()
	msg, err := ParseMessage(userMessageWithToolResult(content, isError))
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
	return toolResult
}

// isPersistedOutput is the recommended caller pattern for detecting the
// degraded path: the CLI spilled this tool result to a temp file (layer 2).
func isPersistedOutput(block *ToolResultBlock) bool {
	content, ok := block.Content.(string)
	return ok && strings.HasPrefix(content, "<persisted-output>")
}

// Mirrors test_persisted_output_detectable_by_prefix: after a layer-2 spill,
// content starts with '<persisted-output>' — callers can detect this and
// warn users or raise an error.
func TestPersistedOutputDetectableByPrefix(t *testing.T) {
	block := parseToolResultBlock(t, persistedContent, false)
	content, ok := block.Content.(string)
	if !ok {
		t.Fatalf("Expected string content, got %T", block.Content)
	}
	if !strings.HasPrefix(content, "<persisted-output>") {
		preview := content
		if len(preview) > 100 {
			preview = preview[:100]
		}
		t.Errorf("Expected persisted-output wrapper, got: %q", preview)
	}
}

// Mirrors test_persisted_output_is_not_full_content: Claude receives only the
// 2 KB preview, not the original large content.
func TestPersistedOutputIsNotFullContent(t *testing.T) {
	block := parseToolResultBlock(t, persistedContent, false)
	content, ok := block.Content.(string)
	if !ok {
		t.Fatalf("Expected string content, got %T", block.Content)
	}
	if len(content) >= layer2ThresholdChars {
		t.Errorf("Expected preview under %d chars, got %d", layer2ThresholdChars, len(content))
	}
}

// Mirrors TestPersistedOutputDetectionHelper.test_helper_detects_persisted.
func TestIsPersistedOutputDetectsPersisted(t *testing.T) {
	block := parseToolResultBlock(t, persistedContent, false)
	if !isPersistedOutput(block) {
		t.Error("Expected isPersistedOutput to detect the spilled result")
	}
}

// Mirrors TestPersistedOutputDetectionHelper.test_helper_passes_inline.
func TestIsPersistedOutputPassesInline(t *testing.T) {
	block := parseToolResultBlock(t, inlineContent, false)
	if isPersistedOutput(block) {
		t.Error("Expected isPersistedOutput to pass inline content through")
	}
	if content, _ := block.Content.(string); content != inlineContent {
		t.Errorf("Expected inline content preserved, got %d chars", len(content))
	}
}
