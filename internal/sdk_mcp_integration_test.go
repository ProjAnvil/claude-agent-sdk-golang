package internal

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Uses mockTransport from query_test.go

func TestSDKMCPServerHandlers(t *testing.T) {
	// Track tool executions
	var toolExecutions []map[string]interface{}

	greetUser := MCPTool{
		Name:        "greet_user",
		Description: "Greets a user by name",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"},
			},
		},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			toolExecutions = append(toolExecutions, map[string]interface{}{
				"name": "greet_user",
				"args": args,
			})
			name, _ := args["name"].(string)
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "Hello, " + name + "!"},
				},
			}, nil
		},
	}

	addNumbers := MCPTool{
		Name:        "add_numbers",
		Description: "Adds two numbers",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a": map[string]interface{}{"type": "number"},
				"b": map[string]interface{}{"type": "number"},
			},
		},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			toolExecutions = append(toolExecutions, map[string]interface{}{
				"name": "add_numbers",
				"args": args,
			})
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": fmt.Sprintf("The sum is %g", a+b)},
				},
			}, nil
		},
	}

	server := &MCPServer{
		Name:    "test-sdk-server",
		Version: "1.0.0",
		Tools:   []MCPTool{greetUser, addNumbers},
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers: map[string]*MCPServer{
			"test-sdk-server": server,
		},
	})

	// Test tools/list
	request := map[string]interface{}{
		"server_name": "test-sdk-server",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		},
	}

	response, err := q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp := response["mcp_response"].(map[string]interface{})
	result := mcpResp["result"].(map[string]interface{})
	tools := result["tools"].([]map[string]interface{})

	if len(tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool["name"].(string)] = true
	}
	if !toolNames["greet_user"] {
		t.Error("greet_user not found")
	}
	if !toolNames["add_numbers"] {
		t.Error("add_numbers not found")
	}

	// Test tools/call greet_user
	request = map[string]interface{}{
		"server_name": "test-sdk-server",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "greet_user",
				"arguments": map[string]interface{}{
					"name": "Alice",
				},
			},
		},
	}

	response, err = q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp = response["mcp_response"].(map[string]interface{})
	result = mcpResp["result"].(map[string]interface{})
	content := result["content"].([]map[string]interface{})

	if content[0]["text"] != "Hello, Alice!" {
		t.Errorf("Expected 'Hello, Alice!', got '%v'", content[0]["text"])
	}

	if len(toolExecutions) != 1 {
		t.Errorf("Expected 1 execution, got %d", len(toolExecutions))
	}
	if toolExecutions[0]["name"] != "greet_user" {
		t.Errorf("Expected greet_user execution")
	}

	// Test tools/call add_numbers
	request = map[string]interface{}{
		"server_name": "test-sdk-server",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      3,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "add_numbers",
				"arguments": map[string]interface{}{
					"a": 5.0,
					"b": 3.0,
				},
			},
		},
	}

	response, err = q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp = response["mcp_response"].(map[string]interface{})
	result = mcpResp["result"].(map[string]interface{})
	content = result["content"].([]map[string]interface{})

	// Float comparison might be tricky with string conversion, checking contains
	if !strings.Contains(content[0]["text"].(string), "8") {
		t.Errorf("Expected result containing '8', got '%v'", content[0]["text"])
	}

	if len(toolExecutions) != 2 {
		t.Errorf("Expected 2 executions, got %d", len(toolExecutions))
	}
}

func TestToolCreation(t *testing.T) {
	// In Go, tools are struct instances. This test primarily verifies the struct works as expected.
	echoTool := MCPTool{
		Name:        "echo",
		Description: "Echo input",
		InputSchema: map[string]interface{}{"input": "string"},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"output": args["input"]}, nil
		},
	}

	if echoTool.Name != "echo" {
		t.Error("Expected tool name 'echo'")
	}
	if echoTool.Description != "Echo input" {
		t.Error("Expected tool description 'Echo input'")
	}
	if echoTool.Handler == nil {
		t.Error("Expected non-nil handler")
	}

	result, err := echoTool.Handler(map[string]interface{}{"input": "test"})
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}
	if result["output"] != "test" {
		t.Errorf("Expected output 'test', got '%v'", result["output"])
	}
}

func TestErrorHandling(t *testing.T) {
	failTool := MCPTool{
		Name:        "fail",
		Description: "Always fails",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return nil, errors.New("Expected error")
		},
	}

	server := &MCPServer{
		Name:  "error-test",
		Tools: []MCPTool{failTool},
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers: map[string]*MCPServer{
			"error-test": server,
		},
	})

	request := map[string]interface{}{
		"server_name": "error-test",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      "fail",
				"arguments": map[string]interface{}{},
			},
		},
	}

	response, err := q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp := response["mcp_response"].(map[string]interface{})
	if mcpResp["error"] == nil {
		t.Error("Expected error in response")
	}

	errObj := mcpResp["error"].(map[string]interface{})
	if errObj["message"] != "Expected error" {
		t.Errorf("Expected error message 'Expected error', got '%v'", errObj["message"])
	}
}

func TestImageContentSupport(t *testing.T) {
	pngData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAACXBIWXMAAAsTAAALEwEAmpwYAAAADElEQVQI12NgYAAAAAMAASV!HAAAAABJRU5ErkJggg==" // Simplified base64

	generateChart := MCPTool{
		Name: "generate_chart",
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "Generated chart: " + args["title"].(string)},
					{
						"type":     "image",
						"data":     pngData,
						"mimeType": "image/png",
					},
				},
			}, nil
		},
	}

	server := &MCPServer{
		Name:  "image-test-server",
		Tools: []MCPTool{generateChart},
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers: map[string]*MCPServer{
			"image-test-server": server,
		},
	})

	request := map[string]interface{}{
		"server_name": "image-test-server",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "generate_chart",
				"arguments": map[string]interface{}{
					"title": "Sales Report",
				},
			},
		},
	}

	response, err := q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp := response["mcp_response"].(map[string]interface{})
	result := mcpResp["result"].(map[string]interface{})
	content := result["content"].([]map[string]interface{})

	if len(content) != 2 {
		t.Errorf("Expected 2 content items, got %d", len(content))
	}

	if content[0]["type"] != "text" || content[0]["text"] != "Generated chart: Sales Report" {
		t.Errorf("Unexpected text content: %v", content[0])
	}

	if content[1]["type"] != "image" {
		t.Errorf("Expected image type, got %v", content[1]["type"])
	}
	if content[1]["data"] != pngData {
		t.Errorf("Unexpected image data")
	}
	if content[1]["mimeType"] != "image/png" {
		t.Errorf("Unexpected mimeType")
	}
}

func TestToolAnnotations(t *testing.T) {
	tTrue := true
	tFalse := false

	readData := MCPTool{
		Name: "read_data",
		Annotations: ToolAnnotations{
			ReadOnlyHint: &tTrue,
		},
	}

	deleteItem := MCPTool{
		Name: "delete_item",
		Annotations: ToolAnnotations{
			DestructiveHint: &tTrue,
			IdempotentHint:  &tTrue,
		},
	}

	search := MCPTool{
		Name: "search",
		Annotations: ToolAnnotations{
			OpenWorldHint: &tTrue,
		},
	}

	readOnlyTool := MCPTool{
		Name: "read_only_tool",
		Annotations: ToolAnnotations{
			ReadOnlyHint:  &tTrue,
			OpenWorldHint: &tFalse,
		},
	}

	plainTool := MCPTool{
		Name: "plain_tool",
	}

	server := &MCPServer{
		Name: "annotations-test",
		Tools: []MCPTool{
			readData, deleteItem, search, readOnlyTool, plainTool,
		},
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers: map[string]*MCPServer{
			"annotations-test": server,
		},
	})

	request := map[string]interface{}{
		"server_name": "annotations-test",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		},
	}

	response, err := q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp := response["mcp_response"].(map[string]interface{})
	result := mcpResp["result"].(map[string]interface{})
	tools := result["tools"].([]map[string]interface{})

	toolsByName := make(map[string]map[string]interface{})
	for _, tool := range tools {
		toolsByName[tool["name"].(string)] = tool
	}

	// Verify read_data annotations
	readDataTool := toolsByName["read_data"]
	ann, ok := readDataTool["annotations"].(ToolAnnotations)
	if !ok {
		t.Fatal("Expected ToolAnnotations struct")
	}
	if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
		t.Error("Expected readOnlyHint=true")
	}

	// Verify delete_item annotations
	deleteItemTool := toolsByName["delete_item"]
	ann = deleteItemTool["annotations"].(ToolAnnotations)
	if ann.DestructiveHint == nil || !*ann.DestructiveHint {
		t.Error("Expected destructiveHint=true")
	}
	if ann.IdempotentHint == nil || !*ann.IdempotentHint {
		t.Error("Expected idempotentHint=true")
	}

	// Verify search annotations
	searchTool := toolsByName["search"]
	ann = searchTool["annotations"].(ToolAnnotations)
	if ann.OpenWorldHint == nil || !*ann.OpenWorldHint {
		t.Error("Expected openWorldHint=true")
	}

	// Verify read_only_tool annotations
	roTool := toolsByName["read_only_tool"]
	ann = roTool["annotations"].(ToolAnnotations)
	if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
		t.Error("Expected readOnlyHint=true")
	}
	if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
		t.Error("Expected openWorldHint=false")
	}

	// Verify plain_tool has no annotations
	plain := toolsByName["plain_tool"]
	if _, ok := plain["annotations"]; ok {
		t.Error("Expected no annotations for plain_tool")
	}
}

// TestToolAnnotations_MaxResultSizeChars tests that maxResultSizeChars is
// forwarded as _meta["anthropic/maxResultSizeChars"] in tools/list responses,
// bypassing Zod annotation stripping in the CLI (#756).
func TestToolAnnotations_MaxResultSizeChars(t *testing.T) {
	tTrue := true
	maxSize := 100000

	toolWithMax := MCPTool{
		Name:        "large_output_tool",
		Description: "Returns large results",
		Annotations: ToolAnnotations{
			ReadOnlyHint:       &tTrue,
			MaxResultSizeChars: &maxSize,
		},
	}

	toolWithoutMax := MCPTool{
		Name:        "normal_tool",
		Description: "Normal results",
		Annotations: ToolAnnotations{
			ReadOnlyHint: &tTrue,
		},
	}

	toolNoAnnotations := MCPTool{
		Name:        "plain",
		Description: "No annotations at all",
	}

	server := &MCPServer{
		Name:  "meta-test",
		Tools: []MCPTool{toolWithMax, toolWithoutMax, toolNoAnnotations},
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers: map[string]*MCPServer{
			"meta-test": server,
		},
	})

	request := map[string]interface{}{
		"server_name": "meta-test",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		},
	}

	response, err := q.handleMCPMessage(request)
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}

	mcpResp := response["mcp_response"].(map[string]interface{})
	result := mcpResp["result"].(map[string]interface{})
	tools := result["tools"].([]map[string]interface{})

	toolsByName := make(map[string]map[string]interface{})
	for _, tool := range tools {
		toolsByName[tool["name"].(string)] = tool
	}

	// Tool with MaxResultSizeChars should have _meta
	largeTool := toolsByName["large_output_tool"]
	meta, ok := largeTool["_meta"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected _meta on tool with MaxResultSizeChars")
	}
	if meta["anthropic/maxResultSizeChars"] != 100000 {
		t.Errorf("Expected anthropic/maxResultSizeChars=100000, got %v", meta["anthropic/maxResultSizeChars"])
	}

	// Tool without MaxResultSizeChars should NOT have _meta
	normalTool := toolsByName["normal_tool"]
	if _, ok := normalTool["_meta"]; ok {
		t.Error("Expected no _meta on tool without MaxResultSizeChars")
	}

	// Tool with no annotations should NOT have _meta
	plainTool := toolsByName["plain"]
	if _, ok := plainTool["_meta"]; ok {
		t.Error("Expected no _meta on tool with no annotations")
	}
}
