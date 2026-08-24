package claude

import (
	"encoding/json"
	"testing"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
)

// TestToolBuilderWithAnnotations tests attaching ToolAnnotations through the
// fluent builder, mirroring Python's @tool(annotations=...).
func TestToolBuilderWithAnnotations(t *testing.T) {
	readOnly := true
	maxSize := 500000

	tool := Tool("get_large_schema", "Returns a large DB schema.", map[string]interface{}{
		"type": "object",
	}).WithAnnotations(ToolAnnotations{
		ReadOnlyHint:       &readOnly,
		MaxResultSizeChars: &maxSize,
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		return ToolResponse("schema"), nil
	})

	if tool.Annotations == nil {
		t.Fatal("Expected annotations on the built tool")
	}
	if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
		t.Error("Expected ReadOnlyHint=true")
	}
	if tool.Annotations.MaxResultSizeChars == nil || *tool.Annotations.MaxResultSizeChars != 500000 {
		t.Errorf("Expected MaxResultSizeChars=500000, got %v", tool.Annotations.MaxResultSizeChars)
	}

	// The annotations flow into the server the CLI is served from: Instance
	// is now a real *mcp.Server, so verify them on the tools/list wire.
	server := CreateSdkMcpServer("srv", "1.0.0", []SdkMcpTool{tool})
	if server.Instance == nil {
		t.Fatal("Expected a non-nil *mcp.Server instance")
	}
	bridge := internal.NewSDKMCPBridge("srv", server.Instance)
	defer bridge.Close()
	bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	result := response["result"].(map[string]interface{})
	tools := result["tools"].([]map[string]interface{})
	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %v", tools)
	}
	ann, ok := tools[0]["annotations"].(map[string]interface{})
	if !ok || ann["readOnlyHint"] != true {
		t.Fatalf("Expected readOnlyHint=true in wire annotations, got %v", tools[0]["annotations"])
	}
	meta, ok := tools[0]["_meta"].(map[string]interface{})
	if !ok || meta["anthropic/maxResultSizeChars"] != 500000 {
		t.Errorf("Expected MaxResultSizeChars to reach the wire _meta, got %v", tools[0]["_meta"])
	}
}

// TestToolBuilderWithoutAnnotations tests the zero-annotation path stays nil.
func TestToolBuilderWithoutAnnotations(t *testing.T) {
	tool := Tool("plain", "No hints", nil).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		return ToolResponse("ok"), nil
	})
	if tool.Annotations != nil {
		t.Errorf("Expected nil annotations, got %v", tool.Annotations)
	}

	server := CreateSdkMcpServer("srv", "1.0.0", []SdkMcpTool{tool})
	if server.Instance == nil {
		t.Fatal("Expected a non-nil *mcp.Server instance")
	}
	// A tool without annotations lists without an annotations key.
	bridge := internal.NewSDKMCPBridge("srv", server.Instance)
	defer bridge.Close()
	bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	result := response["result"].(map[string]interface{})
	tools := result["tools"].([]map[string]interface{})
	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %v", tools)
	}
	if _, ok := tools[0]["annotations"]; ok {
		t.Errorf("Expected no annotations on the wire, got %v", tools[0]["annotations"])
	}
}

// TestToolAnnotationsJSONWireNames verifies the struct marshals with the
// camelCase wire names (the only spelling Go users can write at compile
// time; the snake_case acceptance of #1218 lives in the bridge's map
// normalization).
func TestToolAnnotationsJSONWireNames(t *testing.T) {
	readOnly := true
	openWorld := false

	data, err := json.Marshal(ToolAnnotations{
		ReadOnlyHint:  &readOnly,
		OpenWorldHint: &openWorld,
	})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var wire map[string]interface{}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if wire["readOnlyHint"] != true || wire["openWorldHint"] != false {
		t.Errorf("Unexpected wire annotations: %v", wire)
	}
	if _, ok := wire["read_only_hint"]; ok {
		t.Error("Expected no snake_case keys on the wire")
	}
}
