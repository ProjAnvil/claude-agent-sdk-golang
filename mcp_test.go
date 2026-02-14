package claude

import (
	"testing"
)

// TestToolBuilder tests the Tool builder pattern.
func TestToolBuilder(t *testing.T) {
	tool := Tool("test_tool", "A test tool", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"arg": map[string]interface{}{"type": "string"},
		},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"result": "success",
		}, nil
	})

	if tool.Name != "test_tool" {
		t.Errorf("Expected name='test_tool', got %q", tool.Name)
	}

	if tool.Description != "A test tool" {
		t.Errorf("Expected description='A test tool', got %q", tool.Description)
	}

	if tool.InputSchema == nil {
		t.Error("Expected non-nil input schema")
	}

	if tool.Handler == nil {
		t.Error("Expected non-nil handler")
	}

	// Test handler execution
	result, err := tool.Handler(map[string]interface{}{"arg": "value"})
	if err != nil {
		t.Errorf("Handler failed: %v", err)
	}

	if result["result"] != "success" {
		t.Errorf("Expected result='success', got %v", result["result"])
	}
}

// TestCreateSdkMcpServer tests SDK MCP server creation.
func TestCreateSdkMcpServer(t *testing.T) {
	tool1 := Tool("add", "Add two numbers", map[string]interface{}{
		"type": "object",
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		return ToolResponse("2 + 2 = 4"), nil
	})

	tool2 := Tool("subtract", "Subtract two numbers", map[string]interface{}{
		"type": "object",
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		return ToolResponse("5 - 3 = 2"), nil
	})

	server := CreateSdkMcpServer("calculator", "1.0.0", []SdkMcpTool{tool1, tool2})

	if server == nil {
		t.Fatal("Expected non-nil server")
	}

	if server.Type != "sdk" {
		t.Errorf("Expected type='sdk', got %q", server.Type)
	}

	if server.Name != "calculator" {
		t.Errorf("Expected name='calculator', got %q", server.Name)
	}

	if server.Instance == nil {
		t.Error("Expected non-nil instance")
	}
}

// TestTextContent tests text content helper.
func TestTextContent(t *testing.T) {
	content := TextContent("Hello, world!")

	if content["type"] != "text" {
		t.Errorf("Expected type='text', got %v", content["type"])
	}

	if content["text"] != "Hello, world!" {
		t.Errorf("Expected text='Hello, world!', got %v", content["text"])
	}
}

// TestToolResponse tests tool response helper.
func TestToolResponse(t *testing.T) {
	response := ToolResponse("Operation completed")

	content, ok := response["content"].([]map[string]interface{})
	if !ok {
		t.Fatal("Expected content to be []map[string]interface{}")
	}

	if len(content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(content))
	}

	if content[0]["type"] != "text" {
		t.Errorf("Expected type='text', got %v", content[0]["type"])
	}

	if content[0]["text"] != "Operation completed" {
		t.Errorf("Expected text='Operation completed', got %v", content[0]["text"])
	}
}

// TestToolErrorResponse tests tool error response helper.
func TestToolErrorResponse(t *testing.T) {
	response := ToolErrorResponse("Operation failed")

	content, ok := response["content"].([]map[string]interface{})
	if !ok {
		t.Fatal("Expected content to be []map[string]interface{}")
	}

	if len(content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(content))
	}

	if content[0]["text"] != "Operation failed" {
		t.Errorf("Expected text='Operation failed', got %v", content[0]["text"])
	}

	isError, ok := response["is_error"].(bool)
	if !ok || !isError {
		t.Error("Expected is_error=true")
	}
}

// TestToolHandlerError tests error handling in tool handlers.
func TestToolHandlerError(t *testing.T) {
	tool := Tool("error_tool", "A tool that errors", map[string]interface{}{
		"type": "object",
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		return nil, NewClaudeSDKError("tool execution failed", nil)
	})

	result, err := tool.Handler(map[string]interface{}{})
	if err == nil {
		t.Error("Expected error from handler")
	}

	if result != nil {
		t.Error("Expected nil result on error")
	}
}

// TestMultipleTools tests creating a server with multiple tools.
func TestMultipleTools(t *testing.T) {
	tools := []SdkMcpTool{
		Tool("tool1", "First tool", nil).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
			return ToolResponse("tool1 result"), nil
		}),
		Tool("tool2", "Second tool", nil).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
			return ToolResponse("tool2 result"), nil
		}),
		Tool("tool3", "Third tool", nil).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
			return ToolResponse("tool3 result"), nil
		}),
	}

	server := CreateSdkMcpServer("multi_tool_server", "1.0.0", tools)

	if server == nil {
		t.Fatal("Expected non-nil server")
	}

	// Verify all tools are registered
	for i, tool := range tools {
		if tool.Name == "" {
			t.Errorf("Tool %d has empty name", i)
		}
		if tool.Handler == nil {
			t.Errorf("Tool %d has nil handler", i)
		}
	}
}
