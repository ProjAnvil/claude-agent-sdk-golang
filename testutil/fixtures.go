package testutil

// SampleUserMessage is a sample user message for testing.
var SampleUserMessage = map[string]interface{}{
	"type": "user",
	"message": map[string]interface{}{
		"role":    "user",
		"content": "Hello, Claude!",
	},
	"uuid":               "user-123",
	"parent_tool_use_id": nil,
}

// SampleAssistantMessage is a sample assistant message for testing.
var SampleAssistantMessage = map[string]interface{}{
	"type": "assistant",
	"message": map[string]interface{}{
		"model": "claude-sonnet-4-5-20250929",
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "Hello! How can I help you today?",
			},
		},
	},
	"parent_tool_use_id": nil,
}

// SampleToolUseMessage is a sample assistant message with tool use.
var SampleToolUseMessage = map[string]interface{}{
	"type": "assistant",
	"message": map[string]interface{}{
		"model": "claude-sonnet-4-5-20250929",
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "Let me check that for you.",
			},
			map[string]interface{}{
				"type":  "tool_use",
				"id":    "tool-123",
				"name":  "Bash",
				"input": map[string]interface{}{"command": "ls -la"},
			},
		},
	},
}

// SampleToolResultMessage is a sample user message with tool result.
var SampleToolResultMessage = map[string]interface{}{
	"type": "user",
	"message": map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": "tool-123",
				"content":     "file1.txt\nfile2.txt",
			},
		},
	},
}

// SampleSystemMessage is a sample system message for testing.
var SampleSystemMessage = map[string]interface{}{
	"type":    "system",
	"subtype": "init",
}

// SampleResultMessage is a sample result message for testing.
var SampleResultMessage = map[string]interface{}{
	"type":            "result",
	"subtype":         "success",
	"duration_ms":     1500.0,
	"duration_api_ms": 1200.0,
	"is_error":        false,
	"num_turns":       2.0,
	"session_id":      "session-123",
	"total_cost_usd":  0.0025,
	"usage": map[string]interface{}{
		"input_tokens":  100.0,
		"output_tokens": 50.0,
	},
}

// SampleStreamEvent is a sample stream event for testing.
var SampleStreamEvent = map[string]interface{}{
	"type":       "stream_event",
	"uuid":       "event-123",
	"session_id": "session-123",
	"event": map[string]interface{}{
		"type": "content_block_delta",
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": "Hello",
		},
	},
}

// SampleThinkingMessage is a sample assistant message with thinking block.
var SampleThinkingMessage = map[string]interface{}{
	"type": "assistant",
	"message": map[string]interface{}{
		"model": "claude-sonnet-4-5-20250929",
		"content": []interface{}{
			map[string]interface{}{
				"type":      "thinking",
				"thinking":  "Let me think about this...",
				"signature": "sig-123",
			},
			map[string]interface{}{
				"type": "text",
				"text": "Here's my answer.",
			},
		},
	},
}

// CreateConversation creates a sample conversation for testing.
func CreateConversation() []map[string]interface{} {
	return []map[string]interface{}{
		SampleUserMessage,
		SampleAssistantMessage,
		SampleResultMessage,
	}
}

// CreateToolUseConversation creates a conversation with tool use.
func CreateToolUseConversation() []map[string]interface{} {
	return []map[string]interface{}{
		SampleUserMessage,
		SampleToolUseMessage,
		SampleToolResultMessage,
		SampleAssistantMessage,
		SampleResultMessage,
	}
}
