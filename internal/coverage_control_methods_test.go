package internal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// autoAnswerControlRequests starts a goroutine that watches the transport for
// outgoing control requests and answers each one through respond. respond
// receives the inner request map and returns the inner response map to send
// back (including "subtype": "success"/"error"); returning nil leaves the
// request unanswered (used by timeout tests). The returned stop function must
// be called before the transport is closed.
func autoAnswerControlRequests(mt *mockTransport, respond func(request map[string]interface{}) map[string]interface{}) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		seen := 0
		for {
			select {
			case <-done:
				return
			default:
			}
			written := mt.getWritten()
			for seen < len(written) {
				raw := written[seen]
				seen++
				var msg map[string]interface{}
				if err := json.Unmarshal([]byte(raw), &msg); err != nil {
					continue
				}
				if msg["type"] != "control_request" {
					continue
				}
				reqID, _ := msg["request_id"].(string)
				inner, _ := msg["request"].(map[string]interface{})
				resp := respond(inner)
				if resp == nil {
					continue
				}
				resp["request_id"] = reqID
				mt.messages <- map[string]interface{}{
					"type":     "control_response",
					"response": resp,
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return func() { close(done); wg.Wait() }
}

// successResponder answers every control request with a success response
// carrying the given payload.
func successResponder(payload map[string]interface{}) func(request map[string]interface{}) map[string]interface{} {
	return func(request map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"subtype":  "success",
			"response": payload,
		}
	}
}

// lastControlRequest returns the inner request map of the most recent
// control_request written to the transport.
func lastControlRequest(t *testing.T, mt *mockTransport) map[string]interface{} {
	t.Helper()
	written := mt.getWritten()
	for i := len(written) - 1; i >= 0; i-- {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(written[i]), &msg); err != nil {
			continue
		}
		if msg["type"] != "control_request" {
			continue
		}
		inner, ok := msg["request"].(map[string]interface{})
		if !ok {
			t.Fatalf("control_request without request payload: %v", msg)
		}
		return inner
	}
	t.Fatal("no control_request written to transport")
	return nil
}

// controlMethodCase describes one outbound control-protocol method.
type controlMethodCase struct {
	name         string
	invoke       func(ctx context.Context, q *Query) error
	wantSubtype  string
	checkRequest func(t *testing.T, req map[string]interface{})
}

func controlMethodCases() []controlMethodCase {
	return []controlMethodCase{
		{
			name: "GetMCPStatus",
			invoke: func(ctx context.Context, q *Query) error {
				_, err := q.GetMCPStatus(ctx)
				return err
			},
			wantSubtype: "mcp_status",
		},
		{
			name: "GetContextUsage",
			invoke: func(ctx context.Context, q *Query) error {
				_, err := q.GetContextUsage(ctx)
				return err
			},
			wantSubtype: "get_context_usage",
		},
		{
			name: "Interrupt",
			invoke: func(ctx context.Context, q *Query) error {
				return q.Interrupt(ctx)
			},
			wantSubtype: "interrupt",
		},
		{
			name: "SetPermissionMode",
			invoke: func(ctx context.Context, q *Query) error {
				return q.SetPermissionMode(ctx, "acceptEdits")
			},
			wantSubtype: "set_permission_mode",
			checkRequest: func(t *testing.T, req map[string]interface{}) {
				if req["mode"] != "acceptEdits" {
					t.Errorf("mode: got %v, want acceptEdits", req["mode"])
				}
			},
		},
		{
			name: "SetModel",
			invoke: func(ctx context.Context, q *Query) error {
				return q.SetModel(ctx, "test-model")
			},
			wantSubtype: "set_model",
			checkRequest: func(t *testing.T, req map[string]interface{}) {
				if req["model"] != "test-model" {
					t.Errorf("model: got %v, want test-model", req["model"])
				}
			},
		},
		{
			name: "RewindFiles",
			invoke: func(ctx context.Context, q *Query) error {
				return q.RewindFiles(ctx, "msg-42")
			},
			wantSubtype: "rewind_files",
			checkRequest: func(t *testing.T, req map[string]interface{}) {
				if req["user_message_id"] != "msg-42" {
					t.Errorf("user_message_id: got %v, want msg-42", req["user_message_id"])
				}
			},
		},
		{
			name: "ReconnectMCPServer",
			invoke: func(ctx context.Context, q *Query) error {
				return q.ReconnectMCPServer(ctx, "db")
			},
			wantSubtype: "mcp_reconnect",
			checkRequest: func(t *testing.T, req map[string]interface{}) {
				if req["serverName"] != "db" {
					t.Errorf("serverName: got %v, want db", req["serverName"])
				}
			},
		},
		{
			name: "ToggleMCPServer",
			invoke: func(ctx context.Context, q *Query) error {
				return q.ToggleMCPServer(ctx, "db", true)
			},
			wantSubtype: "mcp_toggle",
			checkRequest: func(t *testing.T, req map[string]interface{}) {
				if req["serverName"] != "db" {
					t.Errorf("serverName: got %v, want db", req["serverName"])
				}
				if req["enabled"] != true {
					t.Errorf("enabled: got %v, want true", req["enabled"])
				}
			},
		},
		{
			name: "StopTask",
			invoke: func(ctx context.Context, q *Query) error {
				return q.StopTask(ctx, "task-7")
			},
			wantSubtype: "stop_task",
			checkRequest: func(t *testing.T, req map[string]interface{}) {
				if req["task_id"] != "task-7" {
					t.Errorf("task_id: got %v, want task-7", req["task_id"])
				}
			},
		},
	}
}

// TestControlMethodsSuccess verifies each control method sends the right
// request subtype and payload, and resolves on a success response.
func TestControlMethodsSuccess(t *testing.T) {
	for _, tc := range controlMethodCases() {
		t.Run(tc.name, func(t *testing.T) {
			mockTrans := newMockTransport()
			query := NewQuery(QueryConfig{
				Transport:       mockTrans,
				IsStreamingMode: true,
			})
			query.Start()
			stop := autoAnswerControlRequests(mockTrans, successResponder(map[string]interface{}{"ok": true}))

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := tc.invoke(ctx, query); err != nil {
				t.Fatalf("%s failed: %v", tc.name, err)
			}

			req := lastControlRequest(t, mockTrans)
			if req["subtype"] != tc.wantSubtype {
				t.Errorf("subtype: got %v, want %s", req["subtype"], tc.wantSubtype)
			}
			if tc.checkRequest != nil {
				tc.checkRequest(t, req)
			}

			stop()
			if err := query.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

// TestControlMethodsErrorResponse verifies each control method surfaces an
// error control response as a Go error.
func TestControlMethodsErrorResponse(t *testing.T) {
	for _, tc := range controlMethodCases() {
		t.Run(tc.name, func(t *testing.T) {
			mockTrans := newMockTransport()
			query := NewQuery(QueryConfig{
				Transport:       mockTrans,
				IsStreamingMode: true,
			})
			query.Start()
			stop := autoAnswerControlRequests(mockTrans, func(request map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{
					"subtype": "error",
					"error":   "permission denied",
				}
			})

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := tc.invoke(ctx, query)
			if err == nil {
				t.Fatalf("%s: expected error response to surface as error", tc.name)
			}
			if !strings.Contains(err.Error(), "permission denied") {
				t.Errorf("%s: error %q does not contain server message", tc.name, err)
			}

			stop()
			if err := query.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

// TestControlMethodsTimeout verifies each control method fails with a timeout
// error when the CLI never answers.
func TestControlMethodsTimeout(t *testing.T) {
	for _, tc := range controlMethodCases() {
		t.Run(tc.name, func(t *testing.T) {
			mockTrans := newMockTransport()
			query := NewQuery(QueryConfig{
				Transport:       mockTrans,
				IsStreamingMode: true,
			})
			query.Start()

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			err := tc.invoke(ctx, query)
			if err == nil {
				t.Fatalf("%s: expected timeout error", tc.name)
			}
			if !strings.Contains(err.Error(), "control request timeout") {
				t.Errorf("%s: error %q does not mention timeout", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubtype) {
				t.Errorf("%s: error %q does not name subtype %s", tc.name, err, tc.wantSubtype)
			}

			if err := query.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

// TestControlMethodsNonStreaming verifies each control method rejects calls
// when the query is not in streaming mode.
func TestControlMethodsNonStreaming(t *testing.T) {
	for _, tc := range controlMethodCases() {
		t.Run(tc.name, func(t *testing.T) {
			query := NewQuery(QueryConfig{
				Transport:       newMockTransport(),
				IsStreamingMode: false,
			})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := tc.invoke(ctx, query)
			if err == nil {
				t.Fatalf("%s: expected error in non-streaming mode", tc.name)
			}
			if !strings.Contains(err.Error(), "streaming mode") {
				t.Errorf("%s: error %q does not mention streaming mode", tc.name, err)
			}
		})
	}
}

// TestControlMethodsAfterClose verifies control requests made after the query
// (and its read loop) is closed cannot be answered and fail via the context
// deadline rather than hanging forever.
func TestControlMethodsAfterClose(t *testing.T) {
	for _, tc := range controlMethodCases() {
		t.Run(tc.name, func(t *testing.T) {
			mockTrans := newMockTransport()
			query := NewQuery(QueryConfig{
				Transport:       mockTrans,
				IsStreamingMode: true,
			})
			query.Start()
			if err := query.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			err := tc.invoke(ctx, query)
			if err == nil {
				t.Fatalf("%s: expected error after close", tc.name)
			}
			if !strings.Contains(err.Error(), "control request timeout") {
				t.Errorf("%s: error %q does not mention timeout", tc.name, err)
			}
		})
	}
}

// TestGetMCPStatusReturnsPayload verifies the MCP status payload is passed
// through to the caller.
func TestGetMCPStatusReturnsPayload(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()
	stop := autoAnswerControlRequests(mockTrans, successResponder(map[string]interface{}{
		"mcpServers": []interface{}{
			map[string]interface{}{"name": "db", "status": "connected"},
		},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload, err := query.GetMCPStatus(ctx)
	if err != nil {
		t.Fatalf("GetMCPStatus failed: %v", err)
	}
	servers, ok := payload["mcpServers"].([]interface{})
	if !ok || len(servers) != 1 {
		t.Fatalf("mcpServers: got %v, want one entry", payload["mcpServers"])
	}
	server, ok := servers[0].(map[string]interface{})
	if !ok || server["name"] != "db" || server["status"] != "connected" {
		t.Errorf("unexpected server entry: %v", servers[0])
	}

	stop()
	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestGetContextUsageReturnsPayload verifies the context usage breakdown is
// passed through to the caller.
func TestGetContextUsageReturnsPayload(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()
	stop := autoAnswerControlRequests(mockTrans, successResponder(map[string]interface{}{
		"totalTokens": 1234,
		"categories":  map[string]interface{}{"system": 1000, "tools": 234},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload, err := query.GetContextUsage(ctx)
	if err != nil {
		t.Fatalf("GetContextUsage failed: %v", err)
	}
	if payload["totalTokens"] != 1234 {
		t.Errorf("totalTokens: got %v, want 1234", payload["totalTokens"])
	}
	categories, ok := payload["categories"].(map[string]interface{})
	if !ok || categories["system"] != 1000 {
		t.Errorf("categories: got %v", payload["categories"])
	}

	stop()
	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestGetServerInfo verifies GetServerInfo is nil before initialize and
// returns the initialization result afterwards.
func TestGetServerInfo(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})

	if info := query.GetServerInfo(); info != nil {
		t.Errorf("expected nil server info before initialize, got %v", info)
	}

	query.Start()
	stop := autoAnswerControlRequests(mockTrans, successResponder(map[string]interface{}{
		"status": "initialized",
		"serverInfo": map[string]interface{}{
			"name":    "claude-code",
			"version": "1.2.3",
		},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := query.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	info := query.GetServerInfo()
	if info == nil {
		t.Fatal("expected server info after initialize")
	}
	serverInfo, ok := info["serverInfo"].(map[string]interface{})
	if !ok || serverInfo["name"] != "claude-code" || serverInfo["version"] != "1.2.3" {
		t.Errorf("unexpected server info: %v", info)
	}

	stop()
	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestInitializeSendsHooksAndAgents verifies the initialize request carries
// the hooks configuration (with callback IDs and matcher timeout) and the
// agents configuration.
func TestInitializeSendsHooksAndAgents(t *testing.T) {
	mockTrans := newMockTransport()
	hook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{}, nil
	}

	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
		Hooks: map[string][]HookMatcherInternal{
			"PreToolUse": {
				{Matcher: "Bash", Hooks: []HookCallback{hook}, Timeout: 3 * time.Second},
			},
		},
		Agents: map[string]interface{}{
			"reviewer": map[string]interface{}{"description": "reviews code", "prompt": "review it"},
		},
	})
	query.Start()
	stop := autoAnswerControlRequests(mockTrans, successResponder(map[string]interface{}{"status": "initialized"}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := query.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	req := lastControlRequest(t, mockTrans)
	if req["subtype"] != "initialize" {
		t.Errorf("subtype: got %v, want initialize", req["subtype"])
	}

	hooksCfg, ok := req["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hooks in initialize request, got %v", req)
	}
	preToolUse, ok := hooksCfg["PreToolUse"].([]interface{})
	if !ok || len(preToolUse) != 1 {
		t.Fatalf("expected one PreToolUse matcher, got %v", hooksCfg["PreToolUse"])
	}
	matcher, ok := preToolUse[0].(map[string]interface{})
	if !ok {
		t.Fatalf("malformed matcher config: %v", preToolUse[0])
	}
	if matcher["matcher"] != "Bash" {
		t.Errorf("matcher: got %v, want Bash", matcher["matcher"])
	}
	if matcher["timeout"] != float64(3) {
		t.Errorf("timeout: got %v, want 3 seconds", matcher["timeout"])
	}
	callbackIDs, ok := matcher["hookCallbackIds"].([]interface{})
	if !ok || len(callbackIDs) != 1 {
		t.Fatalf("expected one hookCallbackId, got %v", matcher["hookCallbackIds"])
	}
	callbackID, _ := callbackIDs[0].(string)
	if _, ok := query.hookCallbacks[callbackID]; !ok {
		t.Errorf("callback ID %q not registered in hookCallbacks", callbackID)
	}

	agents, ok := req["agents"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected agents in initialize request, got %v", req)
	}
	if _, ok := agents["reviewer"]; !ok {
		t.Errorf("expected reviewer agent in request, got %v", agents)
	}

	stop()
	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestSendControlRequestMarshalError verifies a request body that cannot be
// JSON-encoded fails instead of being written to the transport.
func TestSendControlRequestMarshalError(t *testing.T) {
	mockTrans := newMockTransport()
	query := NewQuery(QueryConfig{
		Transport:       mockTrans,
		IsStreamingMode: true,
	})
	query.Start()

	_, err := query.sendControlRequest(context.Background(), map[string]interface{}{
		"subtype": "bad_payload",
		"payload": make(chan int), // channels are not JSON-marshallable
	})
	if err == nil {
		t.Fatal("expected marshal error, got nil")
	}

	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// writeFailTransport is a mockTransport whose Write always fails.
type writeFailTransport struct {
	*mockTransport
	writeErr error
}

func (w *writeFailTransport) Write(string) error { return w.writeErr }

// TestSendControlRequestWriteError verifies a transport write failure is
// propagated to the caller.
func TestSendControlRequestWriteError(t *testing.T) {
	mockTrans := newMockTransport()
	failing := &writeFailTransport{mockTransport: mockTrans, writeErr: errors.New("write failed")}
	query := NewQuery(QueryConfig{
		Transport:       failing,
		IsStreamingMode: true,
	})
	query.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := query.sendControlRequest(ctx, map[string]interface{}{"subtype": "interrupt"})
	if err == nil {
		t.Fatal("expected write error, got nil")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Errorf("error %q does not contain write failure", err)
	}

	if err := query.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
