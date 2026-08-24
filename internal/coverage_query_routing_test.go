package internal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// waitForControlResponse polls the transport writes for a control_response
// with the given request ID and returns its inner response map.
func waitForControlResponse(t *testing.T, mt *mockTransport, requestID string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range mt.getWritten() {
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				continue
			}
			if msg["type"] != "control_response" {
				continue
			}
			resp, _ := msg["response"].(map[string]interface{})
			if resp == nil || resp["request_id"] != requestID {
				continue
			}
			return resp
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for control response to %s", requestID)
	return nil
}

// TestMalformedControlResponsesIgnored verifies that control_response frames
// missing their payload, missing a request ID, or referencing an unknown
// request are ignored without stalling the read loop.
func TestMalformedControlResponsesIgnored(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()

	// No "response" payload at all.
	mockTrans.messages <- map[string]interface{}{"type": "control_response"}
	// Response payload without a request_id.
	mockTrans.messages <- map[string]interface{}{
		"type":     "control_response",
		"response": map[string]interface{}{"subtype": "success"},
	}
	// Response for a request ID nobody is waiting on.
	mockTrans.messages <- map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "success",
			"request_id": "req_nobody",
			"response":   map[string]interface{}{},
		},
	}
	// Ordinary traffic must still be routed after all of the above.
	mockTrans.messages <- map[string]interface{}{"type": "assistant"}

	select {
	case msg := <-query.RawMessages():
		if msg["type"] != "assistant" {
			t.Errorf("expected assistant message, got %v", msg["type"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read loop stalled on malformed control responses")
	}

	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestControlRequestInvalidFormatRespondsError verifies a control_request
// without a request payload gets an error response back on the wire.
func TestControlRequestInvalidFormatRespondsError(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()

	mockTrans.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": "req_bad_format",
		// no "request" payload
	}

	resp := waitForControlResponse(t, mockTrans, "req_bad_format")
	if resp["subtype"] != "error" {
		t.Errorf("subtype: got %v, want error", resp["subtype"])
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "invalid request format") {
		t.Errorf("error: got %q, want it to mention invalid request format", errMsg)
	}

	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestControlRequestUnknownSubtypeRespondsError verifies an unsupported
// control request subtype gets an error response back on the wire.
func TestControlRequestUnknownSubtypeRespondsError(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()

	mockTrans.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": "req_unknown_subtype",
		"request": map[string]interface{}{
			"subtype": "does_not_exist",
		},
	}

	resp := waitForControlResponse(t, mockTrans, "req_unknown_subtype")
	if resp["subtype"] != "error" {
		t.Errorf("subtype: got %v, want error", resp["subtype"])
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "unsupported control request subtype: does_not_exist") {
		t.Errorf("error: got %q", errMsg)
	}

	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestControlRequestMCPMessageSubtype verifies an mcp_message control request
// is dispatched to the SDK MCP server and answered with a success response
// carrying the JSON-RPC reply.
func TestControlRequestMCPMessageSubtype(t *testing.T) {
	mockTrans := newMockTransport()
	server := &MCPServer{
		Name:    "test_server",
		Version: "1.0.0",
		Tools: []MCPTool{
			{
				Name:        "echo",
				Description: "echoes input",
				InputSchema: map[string]interface{}{"type": "object"},
				Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
					return map[string]interface{}{
						"content": []map[string]interface{}{
							{"type": "text", "text": "ok"},
						},
					}, nil
				},
			},
		},
	}
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		SdkMCPServers:   testSDKServers(map[string]*MCPServer{"test_server": server}),
	})
	query.Start()

	// The go-sdk enforces the MCP handshake: initialize before anything else.
	mockTrans.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": "req_mcp_init",
		"request": map[string]interface{}{
			"subtype":     "mcp_message",
			"server_name": "test_server",
			"message": map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      0,
				"method":  "initialize",
			},
		},
	}
	if resp := waitForControlResponse(t, mockTrans, "req_mcp_init"); resp["subtype"] != "success" {
		t.Fatalf("initialize: got %v, want success (error: %v)", resp["subtype"], resp["error"])
	}

	mockTrans.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": "req_mcp_message",
		"request": map[string]interface{}{
			"subtype":     "mcp_message",
			"server_name": "test_server",
			"message": map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/list",
			},
		},
	}

	resp := waitForControlResponse(t, mockTrans, "req_mcp_message")
	if resp["subtype"] != "success" {
		t.Fatalf("subtype: got %v, want success (error: %v)", resp["subtype"], resp["error"])
	}
	payload, ok := resp["response"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing response payload: %v", resp)
	}
	mcpResp, ok := payload["mcp_response"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing mcp_response: %v", payload)
	}
	result, ok := mcpResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result in mcp_response: %v", mcpResp)
	}
	tools, ok := result["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool in tools/list result, got %v", result["tools"])
	}

	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestReadMessagesStopsAfterQueryClosed verifies the read loop drops incoming
// messages and terminates once the query has been marked closed.
func TestReadMessagesStopsAfterQueryClosed(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()

	query.mu.Lock()
	query.closed = true
	query.mu.Unlock()

	// This message must be dropped: the read loop observes the closed flag
	// and breaks before routing it.
	mockTrans.messages <- map[string]interface{}{"type": "assistant"}

	// Closing the transport error channel lets the read loop finish its
	// error-drain phase and close rawMessages.
	close(mockTrans.errors)

	select {
	case msg, ok := <-query.RawMessages():
		if ok {
			t.Errorf("expected rawMessages to close without delivering %v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not terminate after query was closed")
	}
}

// TestResultFlushesMirrorBatcher verifies that transcript_mirror frames are
// enqueued into the batcher, that a result frame triggers a flush before the
// result is delivered, and that Close closes the batcher.
func TestResultFlushesMirrorBatcher(t *testing.T) {
	mockTrans := newMockTransport()
	batcher := &mockMirrorBatcher{}
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		MirrorBatcher:   batcher,
	})
	query.Start()

	mockTrans.messages <- map[string]interface{}{
		"type":      "transcript_mirror",
		"file_path": "/tmp/session.jsonl",
		"entries": []interface{}{
			map[string]interface{}{"type": "user", "text": "hi"},
		},
	}
	mockTrans.messages <- map[string]interface{}{
		"type":     "result",
		"subtype":  "success",
		"is_error": false,
	}

	// The result is only yielded after the batcher flush, so once it arrives
	// both the enqueue and the flush must have happened.
	select {
	case msg := <-query.RawMessages():
		if msg["type"] != "result" {
			t.Fatalf("expected result message, got %v", msg["type"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result message")
	}

	batcher.mu.Lock()
	enqueued := len(batcher.enqueued)
	flushed := batcher.flushed
	batcher.mu.Unlock()
	if enqueued != 1 {
		t.Errorf("batcher enqueues: got %d, want 1", enqueued)
	}
	if flushed < 1 {
		t.Errorf("batcher flushes: got %d, want at least 1", flushed)
	}

	if err := query.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	batcher.mu.Lock()
	closed := batcher.closed
	batcher.mu.Unlock()
	if closed != 1 {
		t.Errorf("batcher closes: got %d, want 1", closed)
	}
}

// TestHandleControlRequestCancelledContextSkipsResponse verifies that when a
// control handler's context is cancelled (e.g. by control_cancel_request), no
// response is written for that request.
func TestHandleControlRequestCancelledContextSkipsResponse(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	query.handleControlRequest(ctx, map[string]interface{}{
		"request_id": "req_cancelled",
		"request":    map[string]interface{}{"subtype": "does_not_exist"},
	})

	for _, raw := range mockTrans.getWritten() {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}
		if msg["type"] == "control_response" {
			t.Fatalf("expected no response for cancelled request, got %v", msg)
		}
	}
}
