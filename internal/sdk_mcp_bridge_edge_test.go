package internal

import (
	"math"
	"strings"
	"testing"
)

// Additional edge-case tests for the SDK MCP bridge helpers.

func TestNormalizeRequestID(t *testing.T) {
	valid := []struct {
		in, want interface{}
	}{
		{"abc", "abc"},
		{7, int64(7)},
		{int64(7), int64(7)},
		{7.0, int64(7)},
		{0, int64(0)},
	}
	for _, tc := range valid {
		got, ok := normalizeRequestID(tc.in)
		if !ok || got != tc.want {
			t.Errorf("normalizeRequestID(%v) = %v, %v; want %v, true", tc.in, got, ok, tc.want)
		}
	}

	invalid := []interface{}{2.5, nil, true, math.NaN(), math.Inf(1), math.Inf(-1), []interface{}{}}
	for _, in := range invalid {
		if _, ok := normalizeRequestID(in); ok {
			t.Errorf("normalizeRequestID(%v): expected invalid", in)
		}
	}
}

// A string request id works end to end and matches its cancellation requestId.
func TestBridgeStringRequestIDCancellation(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	defer close(finish)
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
			"jsonrpc": "2.0", "id": "req-abc", "method": "tools/call",
			"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
		})
	}()

	<-started
	client.notify("notifications/cancelled", map[string]interface{}{"requestId": "req-abc"})

	response := <-callResponse
	if response["id"] != "req-abc" {
		t.Errorf("Expected id echoed as 'req-abc', got %v", response["id"])
	}
	errObj := response["error"].(map[string]interface{})
	if errObj["code"] != -32800 {
		t.Errorf("Expected code=-32800, got %v", errObj["code"])
	}
}

// An id freed by a cancellation can be reused by a later request, and the
// late handler result does not clobber the new request's response.
func TestBridgeIDReusableAfterCancellation(t *testing.T) {
	finish := make(chan struct{})
	slow := MCPTool{
		Name:        "slow",
		Description: "Waits",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			<-finish
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "late"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{slow, textTool("fast", "quick")}}
	client := newBridgeClient(t, "srv", server)

	orphaned := make(chan map[string]interface{}, 1)
	go func() {
		orphaned <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 42, "method": "tools/call",
			"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
		})
	}()
	// Let the slow call register, then cancel it.
	for {
		client.notify("notifications/cancelled", map[string]interface{}{"requestId": 42})
		select {
		case response := <-orphaned:
			if response["error"] == nil {
				t.Fatalf("Expected cancellation error, got %v", response)
			}
			goto cancelled
		default:
		}
	}
cancelled:

	// The id is free again immediately.
	result := client.callTool(t, "fast", nil)
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "quick" {
		t.Errorf("Expected 'quick', got %v", texts)
	}

	// The abandoned handler finishes; nothing else happens.
	close(finish)
}

func TestToolAnnotationsWireEmptyStruct(t *testing.T) {
	wire, maxSize := toolAnnotationsWire(ToolAnnotations{})
	if wire == nil || len(wire) != 0 {
		t.Errorf("Expected empty (non-nil) wire map, got %v", wire)
	}
	if maxSize != nil {
		t.Errorf("Expected no maxResultSizeChars, got %v", maxSize)
	}
}

func TestToolAnnotationsWireNilPointer(t *testing.T) {
	wire, maxSize := toolAnnotationsWire((*ToolAnnotations)(nil))
	if wire != nil || maxSize != nil {
		t.Errorf("Expected nil annotations, got %v, %v", wire, maxSize)
	}
}

func TestToolAnnotationsWireUnknownKeysPassThrough(t *testing.T) {
	wire, maxSize := toolAnnotationsWire(map[string]interface{}{
		"read_only_hint": true,
		"custom/vendor":  "kept",
	})
	if wire["readOnlyHint"] != true {
		t.Errorf("Expected snake_case hint normalized, got %v", wire)
	}
	if wire["custom/vendor"] != "kept" {
		t.Errorf("Expected unknown key to pass through, got %v", wire)
	}
	if maxSize != nil {
		t.Errorf("Expected no maxResultSizeChars, got %v", maxSize)
	}
}

func TestToolAnnotationsWireUnsupportedType(t *testing.T) {
	// An annotations value of an unsupported type yields no wire annotations.
	wire, maxSize := toolAnnotationsWire(42)
	if wire != nil || maxSize != nil {
		t.Errorf("Expected nil annotations, got %v, %v", wire, maxSize)
	}

	// A map[string]string is tolerated and passed through.
	wire, _ = toolAnnotationsWire(map[string]string{"custom": "x"})
	if wire["custom"] != "x" {
		t.Errorf("Expected map[string]string passthrough, got %v", wire)
	}
}

func TestConvertToolContentMalformedInputs(t *testing.T) {
	if _, msg := convertToolContent("not a list"); !strings.Contains(msg, "'content' must be a list") {
		t.Errorf("Expected content list error, got %q", msg)
	}
	if _, msg := convertToolContent([]interface{}{"not a map"}); !strings.Contains(msg, "content item must be an object") {
		t.Errorf("Expected content item error, got %q", msg)
	}
	if _, msg := convertToolContent([]interface{}{
		map[string]interface{}{"type": "image", "mimeType": "image/png"},
	}); msg != "'data'" {
		t.Errorf("Expected 'data', got %q", msg)
	}
	if _, msg := convertToolContent([]interface{}{
		map[string]interface{}{"type": "image", "data": "AAAA"},
	}); msg != "'mimeType'" {
		t.Errorf("Expected 'mimeType', got %q", msg)
	}
}

func TestJSONNumberCoversNumericTypes(t *testing.T) {
	for _, value := range []interface{}{float32(1.5), int(2), int64(3), int32(4), 5.0} {
		if _, ok := jsonNumber(value); !ok {
			t.Errorf("Expected %T to convert, got not-a-number", value)
		}
	}
	if _, ok := jsonNumber("nope"); ok {
		t.Error("Expected string to be not-a-number")
	}
}
