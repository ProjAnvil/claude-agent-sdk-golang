package internal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tests for serving a hand-built *mcp.Server through MCPSdkServerConfig
// .Instance: everything the go-sdk server implements (resources, prompts,
// server-initiated traffic) flows over the bridge at full fidelity, unlike
// the factory-built server which pins the tools-only wire semantics.

// newInstanceBridgeClient plays the CLI's part for a hand-built *mcp.Server:
// initialize first, then request/notify. The bridge is closed with the test.
func newInstanceBridgeClient(t *testing.T, name string, server *mcp.Server) *bridgeClient {
	t.Helper()
	bridge := NewSDKMCPBridge(name, server)
	t.Cleanup(bridge.Close)
	client := &bridgeClient{bridge: bridge}
	client.request(t, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test-client", "version": "0.0.0"},
	})
	client.notify("notifications/initialized", nil)
	return client
}

// A hand-built server with a resource and a prompt serves resources/* and
// prompts/* verbatim — the gap the Instance field exists to close.
func TestInstanceServerServesResourcesAndPrompts(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "custom", Version: "2.0.0"}, nil)
	server.AddResource(&mcp.Resource{
		URI:         "file:///doc.txt",
		Name:        "doc",
		Description: "A document",
		MIMEType:    "text/plain",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "file:///doc.txt",
				MIMEType: "text/plain",
				Text:     "document body",
			}},
		}, nil
	})
	server.AddPrompt(&mcp.Prompt{
		Name:        "greet",
		Description: "A greeting prompt",
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "A greeting prompt",
			Messages: []*mcp.PromptMessage{{
				Role:    mcp.Role("user"),
				Content: &mcp.TextContent{Text: "Say hello"},
			}},
		}, nil
	})
	client := newInstanceBridgeClient(t, "custom", server)

	// The initialize handshake reports the hand-built server's identity and
	// its real capabilities (resources and prompts, served by the go-sdk).
	initResponse := client.request(t, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test-client", "version": "0.0.0"},
	})
	initResult := initResponse["result"].(map[string]interface{})
	serverInfo := initResult["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "custom" || serverInfo["version"] != "2.0.0" {
		t.Errorf("Unexpected serverInfo: %v", serverInfo)
	}
	capabilities := initResult["capabilities"].(map[string]interface{})
	if _, ok := capabilities["resources"]; !ok {
		t.Errorf("Expected resources capability, got %v", capabilities)
	}
	if _, ok := capabilities["prompts"]; !ok {
		t.Errorf("Expected prompts capability, got %v", capabilities)
	}

	// resources/list reaches the server.
	resourcesResponse := client.request(t, "resources/list", map[string]interface{}{})
	resources := resourcesResponse["result"].(map[string]interface{})["resources"].([]map[string]interface{})
	if len(resources) != 1 || resources[0]["uri"] != "file:///doc.txt" || resources[0]["name"] != "doc" {
		t.Fatalf("Unexpected resources: %v", resources)
	}

	// resources/read reaches the handler.
	readResponse := client.request(t, "resources/read", map[string]interface{}{"uri": "file:///doc.txt"})
	contents := readResponse["result"].(map[string]interface{})["contents"].([]map[string]interface{})
	if len(contents) != 1 || contents[0]["text"] != "document body" {
		t.Fatalf("Unexpected contents: %v", contents)
	}

	// prompts/list and prompts/get reach the server.
	promptsResponse := client.request(t, "prompts/list", map[string]interface{}{})
	prompts := promptsResponse["result"].(map[string]interface{})["prompts"].([]map[string]interface{})
	if len(prompts) != 1 || prompts[0]["name"] != "greet" {
		t.Fatalf("Unexpected prompts: %v", prompts)
	}

	getResponse := client.request(t, "prompts/get", map[string]interface{}{"name": "greet"})
	messages := getResponse["result"].(map[string]interface{})["messages"].([]map[string]interface{})
	if len(messages) != 1 || messages[0]["role"] != "user" {
		t.Fatalf("Unexpected prompt messages: %v", messages)
	}
	content := messages[0]["content"].(map[string]interface{})
	if content["type"] != "text" || content["text"] != "Say hello" {
		t.Errorf("Unexpected prompt content: %v", content)
	}
}

// A server-initiated ping is answered with an empty result, so a handler
// waiting on it completes. A server-initiated notification is dropped
// without stalling the session.
func TestInstanceServerPingIsAnswered(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "pinger", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "ping_client",
		Description: "Pings the client mid-call",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// A notification the bridge drops must not stall the call.
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: "tok", Progress: 1,
		})
		if err := req.Session.Ping(ctx, nil); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ping answered"}},
		}, nil
	})
	client := newInstanceBridgeClient(t, "pinger", server)

	result := client.callTool(t, "ping_client", nil)
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "ping answered" {
		t.Errorf("Expected the ping to be answered, got %v", texts)
	}
}

// A server-initiated request other than ping (roots, sampling, elicitation)
// is refused with -32601 so the server's caller fails at once instead of
// waiting forever.
func TestInstanceServerClientRequestIsRefused(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "rooty", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "list_roots",
		Description: "Asks the client for roots",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, err := req.Session.ListRoots(ctx, nil)
		if err == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "unexpectedly answered"}},
			}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "refused: " + err.Error()}},
		}, nil
	})
	client := newInstanceBridgeClient(t, "rooty", server)

	result := client.callTool(t, "list_roots", nil)
	texts := textOf(t, result)
	if len(texts) != 1 || !strings.HasPrefix(texts[0], "refused: ") ||
		!strings.Contains(texts[0], "roots/list is not supported for SDK servers") {
		t.Errorf("Expected the roots request to be refused, got %v", texts)
	}
}

// When the server session ends underneath the CLI, messages are answered
// with an error naming the server and the cause, and a new initialize starts
// a fresh session.
func TestInstanceServerStoppedAndRestartedByInitialize(t *testing.T) {
	server := BuildToolServer("srv", "1.0.0", []MCPTool{textTool("ok", "fine")})
	bridge := NewSDKMCPBridge("srv", server)
	t.Cleanup(bridge.Close)

	initialize := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if initialize["error"] != nil {
		t.Fatalf("initialize failed: %v", initialize["error"])
	}

	// Stop the server underneath the bridge.
	bridge.mu.Lock()
	session := bridge.session
	bridge.mu.Unlock()
	if session == nil {
		t.Fatal("Expected a live session after initialize")
	}
	if err := session.serverSess.Close(); err != nil {
		t.Fatalf("Failed to stop the server session: %v", err)
	}
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Session did not notice the server stopping")
	}

	// Every non-initialize message is answered with the stopped error.
	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	errObj, ok := response["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected a stopped-server error, got %v", response)
	}
	message := errObj["message"].(string)
	if !strings.Contains(message, "SDK MCP server") || !strings.Contains(message, "stopped") {
		t.Errorf("Unexpected stopped-server message: %v", message)
	}

	// A new initialize starts a fresh session that serves again.
	restarted := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "initialize",
	})
	if restarted["error"] != nil {
		t.Fatalf("re-initialize failed: %v", restarted["error"])
	}
	toolsResponse := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 4, "method": "tools/list",
	})
	tools := toolsResponse["result"].(map[string]interface{})["tools"].([]map[string]interface{})
	if len(tools) != 1 || tools[0]["name"] != "ok" {
		t.Errorf("Expected the restarted server to serve, got %v", tools)
	}
}

// When the bridge is closed with a call in flight, the unanswered request
// is settled with a session-closed error rather than hanging. (The abandoned
// handler itself runs to completion — factory handlers ignore their
// context — and its late result is discarded.)
func TestBridgeCloseFailsInFlightCall(t *testing.T) {
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
	server := BuildToolServer("srv", "1.0.0", []MCPTool{slow})
	bridge := NewSDKMCPBridge("srv", server)

	if reply := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	}); reply["error"] != nil {
		t.Fatalf("initialize failed: %v", reply["error"])
	}

	callResponse := make(chan map[string]interface{}, 1)
	go func() {
		callResponse <- bridge.Handle(map[string]interface{}{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
		})
	}()
	<-started

	// Close the bridge with the call unanswered; closing the connection
	// fails the pending waiter at once.
	closed := make(chan struct{})
	go func() {
		bridge.Close()
		close(closed)
	}()

	select {
	case response := <-callResponse:
		errObj, ok := response["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected a session-closed error, got %v", response)
		}
		if !strings.Contains(errObj["message"].(string), "session closed") {
			t.Errorf("Unexpected message: %v", errObj["message"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("In-flight call was not settled when the bridge closed")
	}

	// Let the abandoned handler finish so the session can wind down, then
	// Close returns.
	close(finish)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Bridge.Close did not return after the handler finished")
	}
}

// A repeated initialize on a live session is answered from the first
// handshake's result (go-sdk rejects a second initialize, but the CLI
// re-initializes SDK servers at turn starts).
func TestBridgeRepeatedInitializeAnswersFromFirstHandshake(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{textTool("ok", "fine")}}
	bridge := newTestBridge(t, "srv", server)

	first := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": "2025-06-18"},
	})
	firstResult := first["result"].(map[string]interface{})
	if firstResult["protocolVersion"] != "2025-06-18" {
		t.Fatalf("Unexpected first handshake: %v", firstResult)
	}

	// A second initialize, even one naming a different protocol version, is
	// answered with the first handshake's result and does not disturb the
	// session.
	second := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": "2025-03-26"},
	})
	if second["id"] != 2 {
		t.Errorf("Expected id=2, got %v", second["id"])
	}
	secondResult := second["result"].(map[string]interface{})
	if secondResult["protocolVersion"] != firstResult["protocolVersion"] {
		t.Errorf("Expected the cached protocolVersion, got %v", secondResult["protocolVersion"])
	}
	serverInfo := secondResult["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "srv" {
		t.Errorf("Unexpected serverInfo: %v", serverInfo)
	}

	// The session is undisturbed.
	toolsResponse := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/list",
	})
	tools := toolsResponse["result"].(map[string]interface{})["tools"].([]map[string]interface{})
	if len(tools) != 1 || tools[0]["name"] != "ok" {
		t.Errorf("Expected the server to keep serving, got %v", tools)
	}
}

// Closing a Query stops the bridges it created: their sessions end and
// later messages are answered with a closed-bridge error.
func TestQueryCloseStopsMCPBridges(t *testing.T) {
	mt := newMockTransport()
	server := BuildToolServer("srv", "1.0.0", nil)
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers:   map[string]*mcp.Server{"srv": server},
	})

	if _, err := q.handleMCPMessage(map[string]interface{}{
		"server_name": "srv",
		"message":     map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
	}); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	bridge := q.bridgeForServer("srv", server)
	bridge.mu.Lock()
	session := bridge.session
	bridge.mu.Unlock()
	if session == nil {
		t.Fatal("Expected a live session after initialize")
	}

	if err := q.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// The session's goroutines wound down.
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Error("Session goroutines did not stop on Query.Close")
	}

	// And the bridge refuses further requests.
	response := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "ping",
	})
	errObj, ok := response["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected a closed-bridge error, got %v", response)
	}
	if !strings.Contains(errObj["message"].(string), "is closed") {
		t.Errorf("Unexpected closed-bridge message: %v", errObj["message"])
	}
	// Notifications get no reply, as always.
	if reply := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); reply != nil {
		t.Errorf("Expected no reply for a notification on a closed bridge, got %v", reply)
	}
}

// Mirrors test_hand_built_server_is_served_verbatim: a hand-built *mcp.Server
// — one not built by CreateSdkMcpServer — is served untouched: the handshake
// reports its own identity and capabilities, and a tool result carrying
// content types and fields the factory never produces (audio,
// structuredContent) reaches the CLI verbatim.
func TestInstanceServerServesToolResultVerbatim(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "hand-built", Version: "0.1.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "raw",
		Description: "Returns content the SDK factory never produces",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"n": map[string]interface{}{"type": "integer"}},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments map[string]interface{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &arguments); err != nil {
				return nil, err
			}
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "verbatim"},
				// "RIFF" base64-encodes to UklGRg== on the wire.
				&mcp.AudioContent{Data: []byte("RIFF"), MIMEType: "audio/wav"},
			},
			StructuredContent: map[string]interface{}{"tool": "raw", "arguments": arguments},
		}, nil
	})
	server.AddResource(&mcp.Resource{URI: "memo://readme", Name: "readme"},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{URI: "memo://readme", Text: "memo"}},
			}, nil
		})
	client := newInstanceBridgeClient(t, "hand-built", server)

	// The handshake reports the hand-built server's identity and its real
	// capabilities.
	initResponse := client.request(t, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test-client", "version": "0.0.0"},
	})
	initResult := initResponse["result"].(map[string]interface{})
	serverInfo := initResult["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "hand-built" || serverInfo["version"] != "0.1.0" {
		t.Errorf("Unexpected serverInfo: %v", serverInfo)
	}
	capabilities := initResult["capabilities"].(map[string]interface{})
	for _, capability := range []string{"tools", "resources"} {
		if _, ok := capabilities[capability]; !ok {
			t.Errorf("Expected %s capability, got %v", capability, capabilities)
		}
	}

	listed := client.listTools(t)
	if len(listed) != 1 || listed[0]["name"] != "raw" {
		t.Fatalf("Unexpected tools: %v", listed)
	}

	// The tool result reaches the CLI verbatim, including the content types
	// and fields the factory never produces.
	result := client.callTool(t, "raw", map[string]interface{}{"n": 7.0})
	content := result["content"].([]map[string]interface{})
	if len(content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %v", content)
	}
	if content[0]["type"] != "text" || content[0]["text"] != "verbatim" {
		t.Errorf("Unexpected text block: %v", content[0])
	}
	audio := content[1]
	if audio["type"] != "audio" || audio["data"] != "UklGRg==" || audio["mimeType"] != "audio/wav" {
		t.Errorf("Unexpected audio block: %v", audio)
	}
	structured, ok := result["structuredContent"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected structuredContent, got %v", result)
	}
	arguments, _ := structured["arguments"].(map[string]interface{})
	if structured["tool"] != "raw" || arguments["n"] != 7 {
		t.Errorf("Unexpected structuredContent: %v", structured)
	}
	// go-sdk omits a false isError; the CLI reads an absent flag as false.
	if isError, ok := result["isError"]; ok && isError != false {
		t.Errorf("Expected no error flag, got %v", result["isError"])
	}

	// resources/list reaches the hand-built server too.
	resourcesResponse := client.request(t, "resources/list", map[string]interface{}{})
	resources := resourcesResponse["result"].(map[string]interface{})["resources"].([]map[string]interface{})
	if len(resources) != 1 || resources[0]["uri"] != "memo://readme" {
		t.Errorf("Unexpected resources: %v", resources)
	}
}
