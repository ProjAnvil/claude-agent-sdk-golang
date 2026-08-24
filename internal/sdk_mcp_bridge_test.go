package internal

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests mirror the rewritten tests/test_sdk_mcp_integration.py from
// claude-agent-sdk-python#1218: they drive the JSON-RPC surface the CLI uses
// (initialize first, then tools/list / tools/call) and assert on the wire
// payloads the CLI actually receives. The servers are real *mcp.Server
// instances served by the go-sdk over its in-memory transport; SDKMCPBridge
// routes the CLI's raw frames to them.

// newTestBridge builds a factory *mcp.Server for the spec and bridges it,
// closing the bridge (and its session goroutines) with the test.
func newTestBridge(t *testing.T, name string, server *MCPServer) *SDKMCPBridge {
	t.Helper()
	bridge := NewSDKMCPBridge(name, BuildToolServer(server.Name, server.Version, server.Tools))
	t.Cleanup(bridge.Close)
	return bridge
}

// testSDKServers converts factory specs into the *mcp.Server map
// QueryConfig carries.
func testSDKServers(specs map[string]*MCPServer) map[string]*mcp.Server {
	servers := make(map[string]*mcp.Server, len(specs))
	for name, spec := range specs {
		servers[name] = BuildToolServer(spec.Name, spec.Version, spec.Tools)
	}
	return servers
}

// bridgeClient plays the CLI's part for one server, like the Python suite's
// SdkMcpClient: initialize first, then request/notify.
type bridgeClient struct {
	bridge *SDKMCPBridge
	nextID int
}

func newBridgeClient(t *testing.T, name string, server *MCPServer) *bridgeClient {
	t.Helper()
	client := &bridgeClient{bridge: newTestBridge(t, name, server)}
	client.request(t, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test-client", "version": "0.0.0"},
	})
	client.notify("notifications/initialized", nil)
	return client
}

// send forwards one raw message and returns its reply (nil for notifications).
func (c *bridgeClient) send(message map[string]interface{}) map[string]interface{} {
	return c.bridge.Handle(message)
}

// request sends one request and asserts on the response envelope.
func (c *bridgeClient) request(t *testing.T, method string, params map[string]interface{}) map[string]interface{} {
	t.Helper()
	c.nextID++
	message := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  method,
	}
	if params != nil {
		message["params"] = params
	}
	response := c.send(message)
	if response == nil {
		t.Fatalf("Expected a response for %s", method)
	}
	if response["jsonrpc"] != "2.0" {
		t.Errorf("Expected jsonrpc=2.0, got %v", response["jsonrpc"])
	}
	if response["id"] != c.nextID {
		t.Errorf("Expected id=%d, got %v", c.nextID, response["id"])
	}
	return response
}

// notify sends one notification and asserts it gets no reply.
func (c *bridgeClient) notify(method string, params map[string]interface{}) {
	message := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		message["params"] = params
	}
	if reply := c.send(message); reply != nil {
		panic(fmt.Sprintf("Expected no reply for notification %s, got %v", method, reply))
	}
}

// callTool calls a tool and asserts there is no protocol error.
func (c *bridgeClient) callTool(t *testing.T, name string, arguments map[string]interface{}) map[string]interface{} {
	t.Helper()
	if arguments == nil {
		arguments = map[string]interface{}{}
	}
	response := c.request(t, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	})
	if response["error"] != nil {
		t.Fatalf("Unexpected protocol error calling %s: %v", name, response["error"])
	}
	return response["result"].(map[string]interface{})
}

// listTools returns the wire tools of the server.
func (c *bridgeClient) listTools(t *testing.T) []map[string]interface{} {
	t.Helper()
	response := c.request(t, "tools/list", map[string]interface{}{})
	return response["result"].(map[string]interface{})["tools"].([]map[string]interface{})
}

// textOf extracts the text blocks of a tool result, like Python's texts().
func textOf(t *testing.T, result map[string]interface{}) []string {
	t.Helper()
	content, ok := result["content"].([]map[string]interface{})
	if !ok {
		t.Fatalf("Expected content blocks, got %T", result["content"])
	}
	var texts []string
	for _, block := range content {
		if block["type"] == "text" {
			texts = append(texts, block["text"].(string))
		}
	}
	return texts
}

func textTool(name string, text string) MCPTool {
	return MCPTool{
		Name:        name,
		Description: name + " tool",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": text}},
			}, nil
		},
	}
}

// --- Handshake ---------------------------------------------------------------

// Mirrors test_initialize_reports_real_server_info_and_capabilities.
func TestBridgeInitializeReportsServerInfoAndCapabilities(t *testing.T) {
	server := &MCPServer{Name: "hello", Version: "3.2.1", Tools: []MCPTool{textTool("noop", "")}}
	bridge := newTestBridge(t, "hello", server)

	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test-client", "version": "0.0.0"},
		},
	})

	result := response["result"].(map[string]interface{})
	serverInfo := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "hello" || serverInfo["version"] != "3.2.1" {
		t.Errorf("Unexpected serverInfo: %v", serverInfo)
	}
	// The client's requested protocol version is negotiated (echoed), not
	// fixed at 2024-11-05.
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("Expected protocolVersion=2025-06-18, got %v", result["protocolVersion"])
	}
	// Capabilities are the real ones of a tools-only server.
	capabilities := result["capabilities"].(map[string]interface{})
	toolsCap, ok := capabilities["tools"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected tools capability, got %v", capabilities)
	}
	if toolsCap["listChanged"] != false {
		t.Errorf("Expected tools.listChanged=false, got %v", toolsCap["listChanged"])
	}
	if _, ok := capabilities["experimental"]; !ok {
		t.Error("Expected experimental capability")
	}
}

// The protocol version falls back to the historical default when the client
// does not name one.
func TestBridgeInitializeDefaultsProtocolVersion(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	bridge := newTestBridge(t, "srv", server)

	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	result := response["result"].(map[string]interface{})
	if result["protocolVersion"] != defaultMCPProtocolVersion {
		t.Errorf("Expected protocolVersion=%s, got %v", defaultMCPProtocolVersion, result["protocolVersion"])
	}
}

// Mirrors test_initialized_notification_gets_no_jsonrpc_reply.
func TestBridgeInitializedNotificationGetsNoReply(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	bridge := newTestBridge(t, "srv", server)

	if reply := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	}); reply == nil {
		t.Fatal("Expected initialize response")
	}
	if reply := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); reply != nil {
		t.Errorf("Expected no reply for notification, got %v", reply)
	}
	// The server is still healthy afterwards.
	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	tools := response["result"].(map[string]interface{})["tools"].([]map[string]interface{})
	if len(tools) != 0 {
		t.Errorf("Expected empty tools list, got %v", tools)
	}
}

// Mirrors test_control_request_for_notification_is_still_acknowledged, at the
// handleMCPMessage level: the control request carrying a notification is
// acked with {"jsonrpc": "2.0", "result": {}}.
func TestBridgeControlRequestForNotificationIsAcknowledged(t *testing.T) {
	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers: testSDKServers(map[string]*MCPServer{
			"srv": {Name: "srv", Version: "1.0.0"},
		}),
	})

	response, err := q.handleMCPMessage(map[string]interface{}{
		"server_name": "srv",
		"message": map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "notifications/initialized",
		},
	})
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}
	mcpResp := response["mcp_response"].(map[string]interface{})
	if mcpResp["jsonrpc"] != "2.0" {
		t.Errorf("Expected jsonrpc=2.0, got %v", mcpResp["jsonrpc"])
	}
	result, ok := mcpResp["result"].(map[string]interface{})
	if !ok || len(result) != 0 {
		t.Errorf("Expected empty result ack, got %v", mcpResp["result"])
	}
	if _, hasID := mcpResp["id"]; hasID {
		t.Error("Expected no id on a notification ack")
	}
}

// Mirrors test_unknown_server_is_a_jsonrpc_error.
func TestBridgeUnknownServerIsAJSONRPCError(t *testing.T) {
	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers:   map[string]*mcp.Server{},
	})

	response, err := q.handleMCPMessage(map[string]interface{}{
		"server_name": "missing",
		"message":     map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
	})
	if err != nil {
		t.Fatalf("handleMCPMessage failed: %v", err)
	}
	mcpResp := response["mcp_response"].(map[string]interface{})
	errObj := mcpResp["error"].(map[string]interface{})
	if errObj["code"] != -32601 {
		t.Errorf("Expected code=-32601, got %v", errObj["code"])
	}
	if !strings.Contains(errObj["message"].(string), "missing") {
		t.Errorf("Expected server name in error message, got %v", errObj["message"])
	}
}

// Mirrors test_unimplemented_method_is_answered_by_the_server: a tools-only
// server refuses resources/list and prompts/get with -32601, exactly what the
// mcp library answers for unimplemented methods.
func TestBridgeUnimplementedMethodsAreRefused(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	client := newBridgeClient(t, "srv", server)

	for _, method := range []string{"resources/list", "resources/read", "prompts/list", "prompts/get"} {
		response := client.request(t, method, map[string]interface{}{})
		errObj, ok := response["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected error for %s, got %v", method, response)
		}
		if errObj["code"] != -32601 {
			t.Errorf("Expected code=-32601 for %s, got %v", method, errObj["code"])
		}
	}
}

// Ping is answered with an empty result, as the mcp library answers it.
func TestBridgePingIsAnswered(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	client := newBridgeClient(t, "srv", server)

	response := client.request(t, "ping", nil)
	result, ok := response["result"].(map[string]interface{})
	if !ok || len(result) != 0 {
		t.Errorf("Expected empty ping result, got %v", response["result"])
	}
}

// Mirrors test_malformed_message_is_a_jsonrpc_error_and_the_session_survives.
func TestBridgeMalformedMessageIsAJSONRPCErrorAndServerSurvives(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	client := newBridgeClient(t, "srv", server)

	response := client.send(map[string]interface{}{"jsonrpc": "2.0", "id": 5})
	if response == nil {
		t.Fatal("Expected a response for the malformed message")
	}
	if response["id"] != 5 {
		t.Errorf("Expected id=5, got %v", response["id"])
	}
	errObj := response["error"].(map[string]interface{})
	if errObj["code"] != -32603 {
		t.Errorf("Expected code=-32603, got %v", errObj["code"])
	}
	// The server carries on.
	if tools := client.listTools(t); len(tools) != 0 {
		t.Errorf("Expected empty tools list after malformed message, got %v", tools)
	}
}

// Mirrors test_message_mcp_reads_as_a_notification_gets_no_reply: a frame
// whose id is not a valid JSON-RPC id is a notification and gets no reply.
func TestBridgeNonIntegralIDGetsNoReply(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	client := newBridgeClient(t, "srv", server)

	if reply := client.send(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2.5, "method": "tools/list",
	}); reply != nil {
		t.Errorf("Expected no reply for fractional id, got %v", reply)
	}
	// The server carries on.
	if tools := client.listTools(t); len(tools) != 0 {
		t.Errorf("Expected empty tools list, got %v", tools)
	}
}

// A JSON-RPC response frame (id + result, no method) gets no reply.
func TestBridgeResponseFrameGetsNoReply(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	client := newBridgeClient(t, "srv", server)

	if reply := client.send(map[string]interface{}{
		"jsonrpc": "2.0", "id": 9, "result": map[string]interface{}{},
	}); reply != nil {
		t.Errorf("Expected no reply for a response frame, got %v", reply)
	}
	if reply := client.send(map[string]interface{}{
		"jsonrpc": "2.0", "id": 10, "error": map[string]interface{}{"code": -1, "message": "x"},
	}); reply != nil {
		t.Errorf("Expected no reply for an error frame, got %v", reply)
	}
}

// Mirrors test_repeated_handshake_is_served_by_the_same_session: a repeated
// initialize is answered like the first and a call in flight is undisturbed.
func TestBridgeRepeatedHandshakeLeavesInFlightCallUndisturbed(t *testing.T) {
	release := make(chan struct{})
	slow := MCPTool{
		Name:        "slow",
		Description: "Finishes when released",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			<-release
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "slow done"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{slow}}
	client := newBridgeClient(t, "srv", server)

	callResult := make(chan map[string]interface{}, 1)
	go func() {
		callResult <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 50, "method": "tools/call",
			"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
		})
	}()

	// Re-initialize while the call is in flight.
	response := client.request(t, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
	})
	if response["error"] != nil {
		t.Fatalf("Repeated initialize failed: %v", response["error"])
	}

	close(release)
	call := <-callResult
	if call["id"] != 50 {
		t.Errorf("Expected id=50, got %v", call["id"])
	}
	result := call["result"].(map[string]interface{})
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "slow done" {
		t.Errorf("Expected 'slow done', got %v", texts)
	}
}

// --- tools/list wire format ----------------------------------------------------

// Mirrors test_tools_list_wire_format.
func TestBridgeToolsListWireFormat(t *testing.T) {
	tTrue, tFalse := true, false
	readData := MCPTool{
		Name:        "read_data",
		Description: "Read data from source",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"source": map[string]interface{}{"type": "string"}},
			"required":   []interface{}{"source"},
		},
		Annotations: ToolAnnotations{ReadOnlyHint: &tTrue, OpenWorldHint: &tFalse},
		Handler:     textTool("read_data", "").Handler,
	}
	deleteItem := MCPTool{
		Name:        "delete_item",
		Description: "Delete an item",
		InputSchema: map[string]interface{}{"type": "object"},
		Annotations: ToolAnnotations{DestructiveHint: &tTrue, IdempotentHint: &tTrue},
		Handler:     textTool("delete_item", "").Handler,
	}
	plain := MCPTool{
		Name:        "plain",
		Description: "Tool without annotations",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler:     textTool("plain", "").Handler,
	}

	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{readData, deleteItem, plain}}
	client := newBridgeClient(t, "srv", server)

	tools := make(map[string]map[string]interface{})
	for _, tool := range client.listTools(t) {
		tools[tool["name"].(string)] = tool
	}
	if len(tools) != 3 {
		t.Fatalf("Expected 3 tools, got %d", len(tools))
	}

	plainTool := tools["plain"]
	if plainTool["description"] != "Tool without annotations" {
		t.Errorf("Unexpected description: %v", plainTool["description"])
	}
	if _, ok := plainTool["inputSchema"].(map[string]interface{}); !ok {
		t.Errorf("Expected inputSchema object, got %T", plainTool["inputSchema"])
	}
	if _, ok := plainTool["annotations"]; ok {
		t.Error("Expected no annotations for plain tool")
	}
	if _, ok := plainTool["_meta"]; ok {
		t.Error("Expected no _meta for plain tool")
	}

	ann := tools["read_data"]["annotations"].(map[string]interface{})
	if ann["readOnlyHint"] != true || ann["openWorldHint"] != false {
		t.Errorf("Unexpected read_data annotations: %v", ann)
	}
	if len(ann) != 2 {
		t.Errorf("Expected exactly 2 annotation keys, got %v", ann)
	}

	ann = tools["delete_item"]["annotations"].(map[string]interface{})
	if ann["destructiveHint"] != true || ann["idempotentHint"] != true {
		t.Errorf("Unexpected delete_item annotations: %v", ann)
	}
}

// A tool without an input schema lists with an empty object schema.
func TestBridgeToolWithoutSchemaListsEmptyObject(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{{Name: "bare", Handler: textTool("bare", "").Handler}}}
	client := newBridgeClient(t, "srv", server)

	tools := client.listTools(t)
	schema, ok := tools[0]["inputSchema"].(map[string]interface{})
	if !ok || len(schema) != 0 {
		t.Errorf("Expected empty object inputSchema, got %v", tools[0]["inputSchema"])
	}
}

// Mirrors test_tool_annotations_take_either_spelling_on_every_mcp_version: a
// map of annotations accepts camelCase or snake_case hint names, both reach
// the wire as camelCase, camelCase wins when both are given, and
// maxResultSizeChars (either spelling) flows to _meta only.
func TestBridgeAnnotationsTakeEitherSpelling(t *testing.T) {
	camel := MCPTool{
		Name:        "camel",
		Description: "Annotated either way",
		InputSchema: map[string]interface{}{},
		Annotations: map[string]interface{}{
			"readOnlyHint": true, "destructiveHint": false, "maxResultSizeChars": 11,
		},
		Handler: textTool("camel", "").Handler,
	}
	snake := MCPTool{
		Name:        "snake",
		Description: "Annotated either way",
		InputSchema: map[string]interface{}{},
		Annotations: map[string]interface{}{
			"read_only_hint": true, "destructive_hint": false, "max_result_size_chars": 11,
		},
		Handler: textTool("snake", "").Handler,
	}
	both := MCPTool{
		Name:        "both",
		Description: "Both spellings given",
		InputSchema: map[string]interface{}{},
		Annotations: map[string]interface{}{
			"openWorldHint": true, "open_world_hint": false, "max_result_size_chars": 2,
		},
		Handler: textTool("both", "").Handler,
	}

	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{camel, snake, both}}
	client := newBridgeClient(t, "srv", server)

	tools := make(map[string]map[string]interface{})
	for _, tool := range client.listTools(t) {
		tools[tool["name"].(string)] = tool
	}

	for _, name := range []string{"camel", "snake"} {
		ann := tools[name]["annotations"].(map[string]interface{})
		if ann["readOnlyHint"] != true || ann["destructiveHint"] != false || len(ann) != 2 {
			t.Errorf("Unexpected %s annotations: %v", name, ann)
		}
		meta := tools[name]["_meta"].(map[string]interface{})
		if meta["anthropic/maxResultSizeChars"] != 11 {
			t.Errorf("Expected %s _meta maxResultSizeChars=11, got %v", name, meta)
		}
	}

	// Given both spellings, the wire (camelCase) name wins.
	ann := tools["both"]["annotations"].(map[string]interface{})
	if ann["openWorldHint"] != true || len(ann) != 1 {
		t.Errorf("Unexpected both annotations: %v", ann)
	}
	meta := tools["both"]["_meta"].(map[string]interface{})
	if meta["anthropic/maxResultSizeChars"] != 2 {
		t.Errorf("Expected both _meta maxResultSizeChars=2, got %v", meta)
	}
}

// Annotations given as a *ToolAnnotations pointer are served the same way.
func TestBridgeAnnotationsPointerForm(t *testing.T) {
	tTrue := true
	maxSize := 333
	tool := MCPTool{
		Name:        "pointed",
		Description: "Pointer annotations",
		InputSchema: map[string]interface{}{},
		Annotations: &ToolAnnotations{ReadOnlyHint: &tTrue, MaxResultSizeChars: &maxSize},
		Handler:     textTool("pointed", "").Handler,
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	tools := client.listTools(t)
	ann := tools[0]["annotations"].(map[string]interface{})
	if ann["readOnlyHint"] != true {
		t.Errorf("Unexpected annotations: %v", ann)
	}
	meta := tools[0]["_meta"].(map[string]interface{})
	if meta["anthropic/maxResultSizeChars"] != 333 {
		t.Errorf("Unexpected _meta: %v", meta)
	}
}

// --- tools/call -----------------------------------------------------------------

// Mirrors test_tool_call_text_results, including the always-present isError.
func TestBridgeToolCallTextResults(t *testing.T) {
	greet := MCPTool{
		Name:        "greet_user",
		Description: "Greets a user by name",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			name, _ := args["name"].(string)
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "Hello, " + name + "!"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{greet}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "greet_user", map[string]interface{}{"name": "Alice"})
	content := result["content"].([]map[string]interface{})
	if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "Hello, Alice!" {
		t.Errorf("Unexpected content: %v", content)
	}
	if result["isError"] != false {
		t.Errorf("Expected isError=false on success, got %v", result["isError"])
	}
}

// Mirrors test_tool_call_image_content.
func TestBridgeToolCallImageContent(t *testing.T) {
	const pngData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"
	chart := MCPTool{
		Name:        "generate_chart",
		Description: "Generates a chart",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			title, _ := args["title"].(string)
			return map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Generated chart: " + title},
					map[string]interface{}{"type": "image", "data": pngData, "mimeType": "image/png"},
				},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{chart}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "generate_chart", map[string]interface{}{"title": "Sales"})
	content := result["content"].([]map[string]interface{})
	if len(content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %v", content)
	}
	if content[0]["type"] != "text" || content[0]["text"] != "Generated chart: Sales" {
		t.Errorf("Unexpected text block: %v", content[0])
	}
	image := content[1]
	if image["type"] != "image" || image["data"] != pngData || image["mimeType"] != "image/png" {
		t.Errorf("Unexpected image block: %v", image)
	}
	if len(image) != 3 {
		t.Errorf("Expected image block to carry only type/data/mimeType, got %v", image)
	}
}

// Mirrors test_is_error_flag_propagated: snake_case is_error maps to isError
// and does not leak onto the wire.
func TestBridgeIsErrorFlagPropagated(t *testing.T) {
	divide := MCPTool{
		Name:        "divide",
		Description: "Divide two numbers",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			b, _ := args["b"].(float64)
			if b == 0 {
				return map[string]interface{}{
					"content":  []map[string]interface{}{{"type": "text", "text": "Division by zero"}},
					"is_error": true,
				}, nil
			}
			a, _ := args["a"].(float64)
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": fmt.Sprintf("%v", a/b)}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{divide}}
	client := newBridgeClient(t, "srv", server)

	failure := client.callTool(t, "divide", map[string]interface{}{"a": 1.0, "b": 0.0})
	if failure["isError"] != true {
		t.Errorf("Expected isError=true, got %v", failure["isError"])
	}
	if _, ok := failure["is_error"]; ok {
		t.Error("Expected no snake_case is_error on the wire")
	}
	if texts := textOf(t, failure); len(texts) != 1 || texts[0] != "Division by zero" {
		t.Errorf("Unexpected failure text: %v", texts)
	}

	success := client.callTool(t, "divide", map[string]interface{}{"a": 6.0, "b": 3.0})
	if success["isError"] != false {
		t.Errorf("Expected isError=false, got %v", success["isError"])
	}
}

// Mirrors test_unknown_tool_is_an_error_result.
func TestBridgeUnknownToolIsAnErrorResult(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{textTool("known", "")}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "mystery", nil)
	if result["isError"] != true {
		t.Errorf("Expected isError=true, got %v", result["isError"])
	}
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "Tool 'mystery' not found" {
		t.Errorf("Unexpected text: %v", texts)
	}
}

// Mirrors test_handler_exception_is_an_error_result.
func TestBridgeHandlerErrorIsAnErrorResult(t *testing.T) {
	failing := MCPTool{
		Name:        "fail",
		Description: "Always fails",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return nil, errors.New("Expected error")
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{failing}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "fail", nil)
	if result["isError"] != true {
		t.Errorf("Expected isError=true, got %v", result["isError"])
	}
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "Expected error" {
		t.Errorf("Unexpected text: %v", texts)
	}
}

// A handler panic is the Go equivalent of a Python handler exception: an
// isError result, never a protocol error, and the server survives.
func TestBridgeHandlerPanicIsAnErrorResult(t *testing.T) {
	panicky := MCPTool{
		Name:        "panic",
		Description: "Always panics",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			panic("boom")
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{panicky, textTool("ok", "fine")}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "panic", nil)
	if result["isError"] != true {
		t.Errorf("Expected isError=true, got %v", result["isError"])
	}
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "boom" {
		t.Errorf("Unexpected text: %v", texts)
	}
	if result := client.callTool(t, "ok", nil); result["isError"] != false {
		t.Error("Expected the server to survive a panicking tool")
	}
}

// Mirrors test_malformed_handler_payload_is_an_error_result.
func TestBridgeMalformedHandlerPayloadIsAnErrorResult(t *testing.T) {
	sloppy := MCPTool{
		Name:        "sloppy",
		Description: "Forgets the text key",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{sloppy}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "sloppy", nil)
	if result["isError"] != true {
		t.Errorf("Expected isError=true, got %v", result["isError"])
	}
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "'text'" {
		t.Errorf("Expected \"'text'\", got %v", texts)
	}
}

// Mirrors test_invalid_arguments_are_rejected_before_the_handler_runs.
func TestBridgeInvalidArgumentsAreRejectedBeforeTheHandlerRuns(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]interface{}
	add := MCPTool{
		Name:        "add",
		Description: "Add two numbers",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a": map[string]interface{}{"type": "number"},
				"b": map[string]interface{}{"type": "number"},
			},
			"required": []interface{}{"a", "b"},
		},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			mu.Lock()
			calls = append(calls, args)
			mu.Unlock()
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": fmt.Sprintf("%v", a+b)}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{add}}
	client := newBridgeClient(t, "srv", server)

	missing := client.callTool(t, "add", map[string]interface{}{"a": 1.0})
	if missing["isError"] != true {
		t.Errorf("Expected isError=true, got %v", missing["isError"])
	}
	if texts := textOf(t, missing); len(texts) != 1 ||
		texts[0] != "Input validation error: 'b' is a required property" {
		t.Errorf("Unexpected text: %v", texts)
	}

	wrongType := client.callTool(t, "add", map[string]interface{}{"a": 1.0, "b": "two"})
	if wrongType["isError"] != true {
		t.Errorf("Expected isError=true, got %v", wrongType["isError"])
	}
	texts := textOf(t, wrongType)
	if len(texts) != 1 || !strings.HasPrefix(texts[0], "Input validation error: ") ||
		!strings.Contains(texts[0], "'two'") {
		t.Errorf("Unexpected text: %v", texts)
	}

	fine := client.callTool(t, "add", map[string]interface{}{"a": 1.0, "b": 2.0})
	if texts := textOf(t, fine); len(texts) != 1 || texts[0] != "3" {
		t.Errorf("Unexpected text: %v", texts)
	}

	if len(calls) != 1 {
		t.Fatalf("Expected the handler to run once, got %d calls", len(calls))
	}
	if calls[0]["b"] != 2.0 {
		t.Errorf("Unexpected handler args: %v", calls[0])
	}
}

// Mirrors test_json_schema_constraints_are_enforced.
func TestBridgeJSONSchemaConstraintsAreEnforced(t *testing.T) {
	validate := MCPTool{
		Name:        "validate",
		Description: "Validate input",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "minLength": 2.0}},
			"required":   []interface{}{"name"},
		},
		Handler: textTool("validate", "OK").Handler,
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{validate}}
	client := newBridgeClient(t, "srv", server)

	tooShort := client.callTool(t, "validate", map[string]interface{}{"name": "x"})
	if tooShort["isError"] != true {
		t.Errorf("Expected isError=true, got %v", tooShort["isError"])
	}
	if texts := textOf(t, tooShort); len(texts) != 1 ||
		!strings.HasPrefix(texts[0], "Input validation error: ") {
		t.Errorf("Unexpected text: %v", texts)
	}

	ok := client.callTool(t, "validate", map[string]interface{}{"name": "xy"})
	if texts := textOf(t, ok); len(texts) != 1 || texts[0] != "OK" {
		t.Errorf("Unexpected text: %v", texts)
	}
}

// Mirrors test_invalid_tool_schema_is_an_error_result: a schema the validator
// cannot use fails the call as an error result, not a protocol error.
func TestBridgeInvalidToolSchemaIsAnErrorResult(t *testing.T) {
	broken := MCPTool{
		Name:        "broken",
		Description: "Has an invalid schema",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"x": map[string]interface{}{"type": "bogus"}},
		},
		Handler: textTool("broken", "unreachable").Handler,
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{broken}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "broken", map[string]interface{}{"x": 1.0})
	if result["isError"] != true {
		t.Errorf("Expected isError=true, got %v", result["isError"])
	}
	if texts := textOf(t, result); len(texts) != 1 || !strings.Contains(texts[0], "bogus") {
		t.Errorf("Expected 'bogus' in error text, got %v", texts)
	}
}

// --- Cancellation and concurrency ----------------------------------------------

// Mirrors test_cancelled_tool_call_is_ended: when the CLI cancels a call
// (notifications/cancelled), the request gets a terminal answer naming the
// cancellation, and the server carries on. (Go cannot unwind the running
// handler goroutine; its late result is discarded, the same caveat Python
// documents for hand-built servers on mcp 1.x.)
func TestBridgeCancelledToolCallIsEnded(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	slow := MCPTool{
		Name:        "slow",
		Description: "Sleeps",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			close(started)
			<-finish
			return map[string]interface{}{"content": []map[string]interface{}{}}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{slow}}
	client := newBridgeClient(t, "srv", server)

	callResponse := make(chan map[string]interface{}, 1)
	go func() {
		callResponse <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 77, "method": "tools/call",
			"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
		})
	}()

	<-started
	client.notify("notifications/cancelled", map[string]interface{}{
		"requestId": 77, "reason": "user interrupted",
	})

	var response map[string]interface{}
	select {
	case response = <-callResponse:
	case <-time.After(5 * time.Second):
		t.Fatal("Cancelled call was not ended")
	}
	if response["id"] != 77 {
		t.Errorf("Expected id=77, got %v", response["id"])
	}
	errObj, ok := response["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected error response, got %v", response)
	}
	if !strings.Contains(errObj["message"].(string), "cancelled") {
		t.Errorf("Expected 'cancelled' in error message, got %v", errObj["message"])
	}

	// The server carries on while the abandoned handler is still running.
	if tools := client.listTools(t); len(tools) != 1 || tools[0]["name"] != "slow" {
		t.Errorf("Expected server to keep serving, got %v", tools)
	}

	// The late handler result is discarded, not delivered.
	close(finish)
	time.Sleep(20 * time.Millisecond)
	select {
	case extra := <-callResponse:
		t.Errorf("Expected no further response, got %v", extra)
	default:
	}
}

// A cancellation naming an unknown request id is a no-op.
func TestBridgeCancellationOfUnknownIDIsANoop(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{textTool("ok", "fine")}}
	client := newBridgeClient(t, "srv", server)

	client.notify("notifications/cancelled", map[string]interface{}{"requestId": 999})
	client.notify("notifications/cancelled", nil)
	client.notify("notifications/cancelled", map[string]interface{}{"requestId": 2.5})

	if result := client.callTool(t, "ok", nil); result["isError"] != false {
		t.Error("Expected the server to be unaffected by stray cancellations")
	}
}

// Mirrors test_reusing_an_in_flight_request_id_is_refused_without_reaching_the_server.
func TestBridgeReusingAnInFlightRequestIDIsRefused(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	waitTool := MCPTool{
		Name:        "wait",
		Description: "Waits",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "done"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{waitTool}}
	client := newBridgeClient(t, "srv", server)

	call := map[string]interface{}{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]interface{}{"name": "wait", "arguments": map[string]interface{}{}},
	}
	firstReply := make(chan map[string]interface{}, 1)
	go func() {
		firstReply <- client.send(call)
	}()
	// Let the first call register its id.
	time.Sleep(50 * time.Millisecond)

	second := client.send(call)
	if second == nil {
		t.Fatal("Expected the duplicate request to be answered")
	}
	if second["id"] != 7 {
		t.Errorf("Expected id=7, got %v", second["id"])
	}
	errObj, ok := second["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected error response, got %v", second)
	}
	if !strings.Contains(errObj["message"].(string), "already in flight") {
		t.Errorf("Expected 'already in flight' in message, got %v", errObj["message"])
	}

	close(release)
	first := <-firstReply
	result, ok := first["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected the first call to succeed, got %v", first)
	}
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "done" {
		t.Errorf("Unexpected first call text: %v", texts)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("Expected the handler to run once, got %d", calls)
	}
}

// Mirrors test_concurrent_tool_calls_on_one_server_both_resolve: each handler
// waits until the sibling call has started, so this only passes when the
// bridge really runs calls concurrently on one server.
func TestBridgeConcurrentToolCallsOnOneServerBothResolve(t *testing.T) {
	var mu sync.Mutex
	var arrived []string
	bothArrived := make(chan struct{})
	var once sync.Once
	rendezvous := MCPTool{
		Name:        "rendezvous",
		Description: "Waits for its sibling call",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			tag, _ := args["tag"].(string)
			mu.Lock()
			arrived = append(arrived, tag)
			if len(arrived) == 2 {
				once.Do(func() { close(bothArrived) })
			}
			mu.Unlock()
			<-bothArrived
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": tag}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{rendezvous}}
	client := newBridgeClient(t, "srv", server)

	results := make(map[string]map[string]interface{}, 2)
	var wg sync.WaitGroup
	var resultsMu sync.Mutex
	for i, tag := range []string{"a", "b"} {
		wg.Add(1)
		go func(id int, tag string) {
			defer wg.Done()
			response := client.send(map[string]interface{}{
				"jsonrpc": "2.0", "id": id, "method": "tools/call",
				"params": map[string]interface{}{
					"name":      "rendezvous",
					"arguments": map[string]interface{}{"tag": tag},
				},
			})
			if response["error"] != nil {
				t.Errorf("Unexpected protocol error for %s: %v", tag, response["error"])
				return
			}
			resultsMu.Lock()
			results[tag] = response["result"].(map[string]interface{})
			resultsMu.Unlock()
		}(100+i, tag)
	}
	wg.Wait()

	for _, tag := range []string{"a", "b"} {
		if texts := textOf(t, results[tag]); len(texts) != 1 || texts[0] != tag {
			t.Errorf("Expected %q result, got %v", tag, texts)
		}
	}
}

// --- Content conversion ---------------------------------------------------------

// Mirrors test_resource_link_content_converted_to_text.
func TestBridgeResourceLinkContentConvertedToText(t *testing.T) {
	getResource := MCPTool{
		Name:        "get_resource",
		Description: "Returns a resource link",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type":        "resource_link",
						"name":        "Example Document",
						"uri":         "https://example.com/doc",
						"description": "An example document",
					},
				},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{getResource}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "get_resource", nil)
	content := result["content"].([]map[string]interface{})
	if len(content) != 1 || content[0]["type"] != "text" {
		t.Fatalf("Expected one text block, got %v", content)
	}
	text := content[0]["text"].(string)
	for _, part := range []string{"Example Document", "https://example.com/doc", "An example document"} {
		if !strings.Contains(text, part) {
			t.Errorf("Expected %q in flattened text, got %q", part, text)
		}
	}
}

// A resource link without any parts flattens to a placeholder.
func TestBridgeBareResourceLinkGetsPlaceholderText(t *testing.T) {
	tool := MCPTool{
		Name:        "bare_link",
		Description: "Resource link without parts",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "resource_link"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	if texts := textOf(t, client.callTool(t, "bare_link", nil)); len(texts) != 1 || texts[0] != "Resource link" {
		t.Errorf("Expected 'Resource link', got %v", texts)
	}
}

// Mirrors test_embedded_resource_text_content_converted.
func TestBridgeEmbeddedResourceTextContentConverted(t *testing.T) {
	tool := MCPTool{
		Name:        "read_file",
		Description: "Returns a text resource",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type":     "resource",
						"resource": map[string]interface{}{"uri": "file:///example.txt", "text": "File contents here"},
					},
				},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "read_file", nil)
	content := result["content"].([]map[string]interface{})
	if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "File contents here" {
		t.Errorf("Expected flattened text resource, got %v", content)
	}
}

// Mirrors test_binary_embedded_resource_skipped_with_warning.
func TestBridgeBinaryEmbeddedResourceSkipped(t *testing.T) {
	tool := MCPTool{
		Name:        "read_binary",
		Description: "Returns a binary resource",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "Before binary"},
					{
						"type":     "resource",
						"resource": map[string]interface{}{"uri": "file:///image.png", "blob": "aGVsbG8="},
					},
					{"type": "text", "text": "After binary"},
				},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	if texts := textOf(t, client.callTool(t, "read_binary", nil)); len(texts) != 2 ||
		texts[0] != "Before binary" || texts[1] != "After binary" {
		t.Errorf("Expected the binary resource to be skipped, got %v", texts)
	}
}

// Mirrors test_unknown_content_type_skipped_with_warning.
func TestBridgeUnknownContentTypeSkipped(t *testing.T) {
	tool := MCPTool{
		Name:        "weird",
		Description: "Returns an unknown content type",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "Before"},
					{"type": "custom_type", "customField": "value"},
					{"type": "text", "text": "After"},
				},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	if texts := textOf(t, client.callTool(t, "weird", nil)); len(texts) != 2 ||
		texts[0] != "Before" || texts[1] != "After" {
		t.Errorf("Expected the unknown content type to be skipped, got %v", texts)
	}
}

// A handler result with extra keys (beyond content/is_error) has them
// dropped from the wire result, like Python's CallToolResult construction.
func TestBridgeHandlerResultExtraKeysAreDropped(t *testing.T) {
	tool := MCPTool{
		Name:        "chatty",
		Description: "Returns extra keys",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "hi"}},
				"output":  "dropped",
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "chatty", nil)
	if _, ok := result["output"]; ok {
		t.Errorf("Expected extra keys to be dropped, got %v", result)
	}
	if len(result) != 2 {
		t.Errorf("Expected exactly content and isError, got %v", result)
	}
}

// A handler result without a content key yields an empty content list.
func TestBridgeMissingContentYieldsEmptyList(t *testing.T) {
	tool := MCPTool{
		Name:        "empty",
		Description: "Returns no content",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{tool}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "empty", nil)
	content, ok := result["content"].([]map[string]interface{})
	if !ok || len(content) != 0 {
		t.Errorf("Expected empty content list, got %v", result["content"])
	}
	if result["isError"] != false {
		t.Errorf("Expected isError=false, got %v", result["isError"])
	}
}
