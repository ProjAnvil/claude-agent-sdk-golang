package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// writeLog is a goroutine-safe capture of transport writes. The mock's
// WriteFunc is invoked from internal query goroutines, so plain slices race
// with test-side readers.
type writeLog struct {
	mu   sync.Mutex
	data []string
}

func (w *writeLog) add(s string) {
	w.mu.Lock()
	w.data = append(w.data, s)
	w.mu.Unlock()
}

// any reports whether any captured write matches pred.
func (w *writeLog) any(pred func(string) bool) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.data {
		if pred(s) {
			return true
		}
	}
	return false
}

// controlResponder installs a WriteFunc on mockT that captures writes and
// auto-responds to every control_request with a success control_response.
// payloadFor may return a response payload per request subtype (nil for none).
func controlResponder(t *MockTransport, writeCapture *writeLog, payloadFor func(subtype string) map[string]interface{}) {
	t.WriteFunc = func(data string) error {
		if writeCapture != nil {
			writeCapture.add(data)
		}

		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return err
		}
		if msg["type"] != "control_request" {
			return nil
		}
		reqID, _ := msg["request_id"].(string)
		req, _ := msg["request"].(map[string]interface{})
		subtype, _ := req["subtype"].(string)

		resp := map[string]interface{}{
			"type": "control_response",
			"response": map[string]interface{}{
				"subtype":    "success",
				"request_id": reqID,
			},
		}
		if payloadFor != nil {
			if payload := payloadFor(subtype); payload != nil {
				resp["response"].(map[string]interface{})["response"] = payload
			} else {
				resp["response"].(map[string]interface{})["response"] = map[string]interface{}{}
			}
		}
		t.readCh <- resp
		return nil
	}
}

// connectedClient returns a client connected over a mock transport whose
// control requests all succeed.
func connectedClient(t *testing.T, opts *ClaudeAgentOptions, payloadFor func(subtype string) map[string]interface{}) (*ClaudeSDKClient, *MockTransport, *writeLog) {
	t.Helper()
	mockT := newMockTransport()
	writes := &writeLog{}
	controlResponder(mockT, writes, payloadFor)

	client := NewClient(opts)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, mockT, writes
}

func TestNewClientDefaultTransportFactory(t *testing.T) {
	client := NewClient(nil)
	if client.transportFactory == nil {
		t.Fatal("expected default transport factory")
	}
	// Invoke the default factory. Whether it succeeds depends on whether a
	// CLI is installed on the host; either outcome must be well-formed.
	tr, err := client.transportFactory(make(chan map[string]interface{}), &transport.TransportOptions{})
	if err != nil && tr != nil {
		t.Errorf("factory returned both transport and error: %v, %v", tr, err)
	}
}

func TestConnectAlreadyConnected(t *testing.T) {
	client, _, _ := connectedClient(t, nil, nil)
	// Second Connect is a no-op.
	if err := client.Connect(context.Background()); err != nil {
		t.Errorf("second Connect should be nil, got %v", err)
	}
}

func TestConnectInvalidSessionStoreOptions(t *testing.T) {
	// ContinueConversation with a store that lacks ListSessions must fail fast.
	client := NewClient(&ClaudeAgentOptions{
		SessionStore:         &BaseSessionStore{},
		ContinueConversation: true,
	})
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return newMockTransport(), nil
	}
	err := client.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ListSessions") {
		t.Errorf("expected ListSessions validation error, got %v", err)
	}

	// session_store + enable_file_checkpointing is rejected.
	client2 := NewClient(&ClaudeAgentOptions{
		SessionStore:            NewInMemorySessionStore(),
		EnableFileCheckpointing: true,
	})
	client2.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return newMockTransport(), nil
	}
	err = client2.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "enable_file_checkpointing") {
		t.Errorf("expected checkpointing validation error, got %v", err)
	}
}

func TestConnectMaterializeResumeError(t *testing.T) {
	// A store whose Load fails makes resume materialization fail before spawn.
	store := &failingLoadStore{BaseSessionStore: &BaseSessionStore{}}
	client := NewClient(&ClaudeAgentOptions{
		SessionStore: store,
		Resume:       "550e8400-e29b-41d4-a716-446655440000",
	})
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return newMockTransport(), nil
	}
	err := client.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "resume materialization") {
		t.Errorf("expected materialization error, got %v", err)
	}
}

// failingLoadStore implements Load (always failing) and nothing else.
type failingLoadStore struct {
	*BaseSessionStore
}

func (s *failingLoadStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	return nil, context.DeadlineExceeded
}

func TestConnectTransportFactoryError(t *testing.T) {
	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return nil, &transport.CLIConnectionError{Message: "no cli"}
	}
	err := client.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no cli") {
		t.Errorf("expected factory error, got %v", err)
	}
}

func TestConnectTransportConnectError(t *testing.T) {
	mockT := newMockTransport()
	mockT.ConnectFunc = func(ctx context.Context) error {
		return context.DeadlineExceeded
	}
	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err == nil {
		t.Error("expected connect error")
	}
}

func TestConnectInitializeErrorClosesTransport(t *testing.T) {
	mockT := newMockTransport()
	closed := false
	mockT.CloseFunc = func() error {
		closed = true
		mockT.shutdown()
		return nil
	}
	mockT.WriteFunc = func(data string) error {
		if strings.Contains(data, `"initialize"`) {
			reqID := extractJSONStringField(data, "request_id")
			mockT.readCh <- map[string]interface{}{
				"type": "control_response",
				"response": map[string]interface{}{
					"subtype":    "error",
					"request_id": reqID,
					"error":      "init failed",
				},
			}
		}
		return nil
	}
	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	err := client.Connect(context.Background())
	if err == nil {
		t.Error("expected initialize error")
	}
	if !closed {
		t.Error("transport should be closed after initialize failure")
	}
}

func TestConnectWithStringPromptCoverage(t *testing.T) {
	mockT := newMockTransport()
	writes := &writeLog{}
	controlResponder(mockT, writes, nil)

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background(), "hello claude"); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	found := writes.any(func(w string) bool {
		return strings.Contains(w, `"type":"user"`) && strings.Contains(w, "hello claude")
	})
	if !found {
		t.Error("string prompt was not sent after initialize")
	}
}

func TestConnectWithFullOptions(t *testing.T) {
	sdkServer := CreateSdkMcpServer("inproc", "1.0.0", nil)
	hookInvoked := make(chan struct{}, 1)

	opts := &ClaudeAgentOptions{
		Hooks: map[HookEvent][]HookMatcher{
			HookEventPreToolUse: {
				{
					Matcher: "Bash",
					Hooks: []HookCallback{
						func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
							select {
							case hookInvoked <- struct{}{}:
							default:
							}
							return HookOutput{}, nil
						},
					},
				},
			},
		},
		MCPServers: map[string]MCPServerConfig{"inproc": sdkServer},
		Agents:     map[string]AgentDefinition{"helper": {Description: "d", Prompt: "p"}},
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			return &PermissionResultAllow{Behavior: "allow"}, nil
		},
	}
	client, _, _ := connectedClient(t, opts, nil)
	if !client.connected {
		t.Error("client should be connected")
	}
}

func TestSendConnected(t *testing.T) {
	client, _, writes := connectedClient(t, nil, nil)
	if err := client.Send(context.Background(), "ping", WithSessionID("sess-42")); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	found := writes.any(func(w string) bool {
		return strings.Contains(w, "ping") && strings.Contains(w, "sess-42")
	})
	if !found {
		t.Error("Send did not write the user message with the custom session id")
	}
}

func TestSendNotConnected(t *testing.T) {
	client := NewClient(nil)
	err := client.Send(context.Background(), "ping")
	if !IsCLIConnectionError(err) {
		t.Errorf("expected CLIConnectionError, got %v", err)
	}
}

func TestRewindFilesCoverage(t *testing.T) {
	// Not connected.
	client := NewClient(nil)
	if err := client.RewindFiles(context.Background(), "msg-1"); !IsCLIConnectionError(err) {
		t.Errorf("expected CLIConnectionError, got %v", err)
	}

	// Connected: the mock answers the rewind_files control request.
	connected, _, _ := connectedClient(t, nil, nil)
	if err := connected.RewindFiles(context.Background(), "msg-1"); err != nil {
		t.Errorf("RewindFiles failed: %v", err)
	}
}

func TestGetServerInfoCoverage(t *testing.T) {
	// Not connected: nil.
	client := NewClient(nil)
	if info := client.GetServerInfo(); info != nil {
		t.Errorf("expected nil server info, got %v", info)
	}

	// Connected: returns the initialize response payload.
	connected, _, _ := connectedClient(t, nil, func(subtype string) map[string]interface{} {
		if subtype == "initialize" {
			return map[string]interface{}{"version": "9.9.9"}
		}
		return nil
	})
	info := connected.GetServerInfo()
	if info == nil {
		t.Fatal("expected server info after connect")
	}
	if v, _ := info["version"].(string); v != "9.9.9" {
		t.Errorf("unexpected server info: %v", info)
	}
}

func TestGetMCPStatusFullParsing(t *testing.T) {
	payloadFor := func(subtype string) map[string]interface{} {
		if subtype != "mcp_status" {
			return nil
		}
		return map[string]interface{}{
			"mcpServers": []interface{}{
				map[string]interface{}{
					"name":   "full",
					"status": "connected",
					"scope":  "project",
					"serverInfo": map[string]interface{}{
						"name":    "full-server",
						"version": "2.0.0",
					},
					"tools": []interface{}{
						map[string]interface{}{
							"name":        "toolA",
							"description": "does A",
							"annotations": map[string]interface{}{
								"readOnly":    true,
								"destructive": false,
								"openWorld":   true,
							},
						},
						// A tool entry of the wrong shape is skipped.
						"not-a-map",
					},
					"config": map[string]interface{}{"command": "srv"},
				},
				// A server entry of the wrong shape is skipped.
				"not-a-map",
			},
		}
	}
	client, _, _ := connectedClient(t, nil, payloadFor)

	resp, err := client.GetMCPStatus(context.Background())
	if err != nil {
		t.Fatalf("GetMCPStatus failed: %v", err)
	}
	if len(resp.MCPServers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(resp.MCPServers))
	}
	srv := resp.MCPServers[0]
	if srv.Name != "full" || srv.Status != McpServerStatusConnected || srv.Scope != "project" {
		t.Errorf("unexpected server status: %+v", srv)
	}
	if srv.ServerInfo == nil || srv.ServerInfo.Name != "full-server" || srv.ServerInfo.Version != "2.0.0" {
		t.Errorf("unexpected server info: %+v", srv.ServerInfo)
	}
	if len(srv.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(srv.Tools))
	}
	tool := srv.Tools[0]
	if tool.Name != "toolA" || tool.Description != "does A" {
		t.Errorf("unexpected tool: %+v", tool)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnly || tool.Annotations.Destructive || !tool.Annotations.OpenWorld {
		t.Errorf("unexpected annotations: %+v", tool.Annotations)
	}
	if srv.Config["command"] != "srv" {
		t.Errorf("unexpected config: %v", srv.Config)
	}
}

func TestGetMCPStatusNotConnectedCoverage(t *testing.T) {
	client := NewClient(nil)
	if _, err := client.GetMCPStatus(context.Background()); !IsCLIConnectionError(err) {
		t.Errorf("expected CLIConnectionError, got %v", err)
	}
}

func TestCloseWithStoreAndMaterializedResume(t *testing.T) {
	// Redirect the config dir so copyAuthFiles works in a sandboxed temp dir.
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", tmp)

	store := NewInMemorySessionStore()
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	projectKey := ProjectKeyForDirectory("")
	err := store.Append(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: sessionID}, []SessionStoreEntry{
		{"type": "user", "uuid": "u1", "message": map[string]interface{}{"content": "hi"}},
	})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	mockT := newMockTransport()
	controlResponder(mockT, nil, nil)

	client := NewClient(&ClaudeAgentOptions{
		SessionStore: store,
		Resume:       sessionID,
	})
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if client.materialized == nil {
		t.Fatal("expected materialized resume to be set")
	}
	if client.mirrorBatcher == nil {
		t.Fatal("expected mirror batcher to be set")
	}
	configDir := client.materialized.ConfigDir

	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if client.connected {
		t.Error("client should be disconnected after Close")
	}
	if client.materialized != nil {
		t.Error("materialized should be cleared after Close")
	}
	if client.mirrorBatcher != nil {
		t.Error("mirrorBatcher should be cleared after Close")
	}
	// The materialized temp dir is removed by Close.
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Error("materialized config dir should be removed after Close")
	}
}

func TestToStringCoverage(t *testing.T) {
	if got := toString("x"); got != "x" {
		t.Errorf("string: got %q", got)
	}
	if got := toString(42); got != "" {
		t.Errorf("int: got %q", got)
	}
	if got := toString(nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Second batch: control-request driven closures and error paths
// ---------------------------------------------------------------------------

// feedControlRequest delivers a control_request frame as if from the CLI.
func feedControlRequest(mockT *MockTransport, requestID string, request map[string]interface{}) {
	mockT.readCh <- map[string]interface{}{
		"type":       "control_request",
		"request_id": requestID,
		"request":    request,
	}
}

// waitForWrite polls until pred matches one of the captured writes or times out.
func waitForWrite(t *testing.T, writes *writeLog, pred func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if writes.any(pred) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for matching write")
}

func TestClientHookCallbackInvoked(t *testing.T) {
	hookCalled := make(chan HookInput, 1)
	opts := &ClaudeAgentOptions{
		Hooks: map[HookEvent][]HookMatcher{
			HookEventPreToolUse: {
				{
					Matcher: "Bash",
					Hooks: []HookCallback{
						func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
							hookCalled <- input
							return HookOutput{Decision: "block", Reason: "no"}, nil
						},
					},
				},
			},
		},
	}
	client, mockT, writes := connectedClient(t, opts, nil)
	_ = client

	// The CLI invokes the registered hook via a hook_callback control request
	// (callback IDs are assigned in registration order, starting at hook_0).
	feedControlRequest(mockT, "req_hook", map[string]interface{}{
		"subtype":     "hook_callback",
		"callback_id": "hook_0",
		"input": map[string]interface{}{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Bash",
			"session_id":      "s1",
		},
		"tool_use_id": "tu-1",
	})

	select {
	case input := <-hookCalled:
		if input.ToolName != "Bash" || input.HookEventName != "PreToolUse" {
			t.Errorf("unexpected hook input: %+v", input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook callback was not invoked")
	}

	// The SDK writes a control_response for the hook callback.
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_hook"`) && strings.Contains(w, "control_response")
	})
}

func TestClientHookCallbackErrorPropagates(t *testing.T) {
	opts := &ClaudeAgentOptions{
		Hooks: map[HookEvent][]HookMatcher{
			HookEventPreToolUse: {
				{
					Matcher: "Bash",
					Hooks: []HookCallback{
						func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
							return HookOutput{}, fmt.Errorf("hook exploded")
						},
					},
				},
			},
		},
	}
	_, mockT, writes := connectedClient(t, opts, nil)

	feedControlRequest(mockT, "req_hook_err", map[string]interface{}{
		"subtype":     "hook_callback",
		"callback_id": "hook_0",
		"input":       map[string]interface{}{"hook_event_name": "PreToolUse"},
	})

	// A failing hook yields an error control response.
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_hook_err"`) && strings.Contains(w, "hook exploded")
	})
}

func TestClientCanUseToolInvoked(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			switch toolName {
			case "Allow":
				return &PermissionResultAllow{Behavior: "allow", UpdatedInput: input}, nil
			case "Deny":
				return &PermissionResultDeny{Behavior: "deny", Message: "no"}, nil
			default:
				return nil, fmt.Errorf("callback error")
			}
		},
	}
	_, mockT, writes := connectedClient(t, opts, nil)

	feedControlRequest(mockT, "req_allow", map[string]interface{}{
		"subtype":   "can_use_tool",
		"tool_name": "Allow",
		"input":     map[string]interface{}{"file_path": "/tmp/x"},
	})
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_allow"`) && strings.Contains(w, `"behavior":"allow"`)
	})

	feedControlRequest(mockT, "req_deny", map[string]interface{}{
		"subtype":   "can_use_tool",
		"tool_name": "Deny",
		"input":     map[string]interface{}{},
	})
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_deny"`) && strings.Contains(w, `"behavior":"deny"`)
	})

	// A callback error yields an error control response.
	feedControlRequest(mockT, "req_err", map[string]interface{}{
		"subtype":   "can_use_tool",
		"tool_name": "Boom",
		"input":     map[string]interface{}{},
	})
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_err"`) && strings.Contains(w, "callback error")
	})
}

func TestClientQueryWriteError(t *testing.T) {
	mockT := newMockTransport()
	writes := &writeLog{}
	controlResponder(mockT, writes, nil)
	// Fail writes after initialization completes.
	initialized := false
	inner := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		if initialized {
			return fmt.Errorf("stdin broken")
		}
		if strings.Contains(data, `"initialize"`) {
			initialized = true
		}
		return inner(data)
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Query(context.Background(), "hello"); err == nil {
		t.Error("expected write error from Query")
	}
	if err := client.Send(context.Background(), "hello"); err == nil {
		t.Error("expected write error from Send")
	}
}

func TestReceiveResponseSkipsUnparseable(t *testing.T) {
	client, mockT, _ := connectedClient(t, nil, nil)

	msgChan, err := client.ReceiveResponse(context.Background())
	if err != nil {
		t.Fatalf("ReceiveResponse failed: %v", err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		// Unparseable frame (assistant with non-dict message): skipped.
		mockT.readCh <- map[string]interface{}{
			"type":    "assistant",
			"message": "not-a-dict",
		}
		// Unknown type: skipped (nil message).
		mockT.readCh <- map[string]interface{}{"type": "future_type"}
		// Then a result, which terminates ReceiveResponse.
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(1),
			"duration_api_ms": float64(1),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "s1",
		}
	}()

	var got []Message
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				break loop
			}
			got = append(got, msg)
		case <-timeout:
			t.Fatal("timeout waiting for result")
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message (bad frames skipped), got %d", len(got))
	}
	if _, ok := got[0].(*ResultMessage); !ok {
		t.Errorf("expected *ResultMessage, got %T", got[0])
	}
}

func TestSetPermissionModeAndModelSuccess(t *testing.T) {
	client, _, writes := connectedClient(t, nil, nil)

	if err := client.SetPermissionMode(context.Background(), PermissionModePlan); err != nil {
		t.Errorf("SetPermissionMode failed: %v", err)
	}
	if err := client.SetModel(context.Background(), "claude-opus-4-7"); err != nil {
		t.Errorf("SetModel failed: %v", err)
	}

	sawMode := writes.any(func(w string) bool {
		return strings.Contains(w, "set_permission_mode") && strings.Contains(w, "plan")
	})
	sawModel := writes.any(func(w string) bool {
		return strings.Contains(w, "set_model") && strings.Contains(w, "claude-opus-4-7")
	})
	if !sawMode || !sawModel {
		t.Errorf("control requests not written: mode=%v model=%v", sawMode, sawModel)
	}
}

func TestGetMCPStatusErrorResponse(t *testing.T) {
	mockT := newMockTransport()
	mockT.WriteFunc = func(data string) error {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return err
		}
		if msg["type"] != "control_request" {
			return nil
		}
		reqID, _ := msg["request_id"].(string)
		req, _ := msg["request"].(map[string]interface{})
		subtype, _ := req["subtype"].(string)
		if subtype == "initialize" {
			mockT.readCh <- map[string]interface{}{
				"type": "control_response",
				"response": map[string]interface{}{
					"subtype":    "success",
					"request_id": reqID,
					"response":   map[string]interface{}{},
				},
			}
			return nil
		}
		mockT.readCh <- map[string]interface{}{
			"type": "control_response",
			"response": map[string]interface{}{
				"subtype":    "error",
				"request_id": reqID,
				"error":      "mcp status unavailable",
			},
		}
		return nil
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if _, err := client.GetMCPStatus(context.Background()); err == nil {
		t.Error("expected mcp_status error")
	}
	if _, err := client.GetContextUsage(context.Background()); err == nil {
		t.Error("expected context usage error")
	}
}

func TestGetContextUsageSuccessAndParseError(t *testing.T) {
	payloadFor := func(subtype string) map[string]interface{} {
		if subtype == "get_context_usage" {
			return map[string]interface{}{
				"categories": []interface{}{
					map[string]interface{}{"name": "System", "tokens": float64(100), "color": "red"},
				},
				"totalTokens": float64(100),
				"maxTokens":   float64(200000),
				"percentage":  float64(0.05),
				"model":       "claude-opus-4-7",
			}
		}
		return nil
	}
	client, _, _ := connectedClient(t, nil, payloadFor)

	resp, err := client.GetContextUsage(context.Background())
	if err != nil {
		t.Fatalf("GetContextUsage failed: %v", err)
	}
	if resp.TotalTokens != 100 || resp.MaxTokens != 200000 || resp.Model != "claude-opus-4-7" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if len(resp.Categories) != 1 || resp.Categories[0].Name != "System" {
		t.Errorf("unexpected categories: %+v", resp.Categories)
	}

	// A payload with a type mismatch fails to unmarshal into the typed struct.
	badPayload := func(subtype string) map[string]interface{} {
		if subtype == "get_context_usage" {
			return map[string]interface{}{"totalTokens": "not-a-number"}
		}
		return nil
	}
	client2, _, _ := connectedClient(t, nil, badPayload)
	if _, err := client2.GetContextUsage(context.Background()); err == nil {
		t.Error("expected unmarshal error for mismatched payload")
	}
}

// ---------------------------------------------------------------------------
// Third batch
// ---------------------------------------------------------------------------

func TestNewClientDefaultFactoryCLIPathPassthrough(t *testing.T) {
	client := NewClient(nil)
	// The default factory constructs a subprocess transport without
	// validating the CLI path up front.
	tr, err := client.transportFactory(make(chan map[string]interface{}), &transport.TransportOptions{
		CLIPath: "/definitely/not/a/real/claude-binary",
	})
	if err != nil || tr == nil {
		t.Errorf("expected transport with explicit CLI path, got %v, %v", tr, err)
	}

	// With no CLI path and an empty PATH, discovery fails.
	t.Setenv("PATH", t.TempDir())
	tr, err = client.transportFactory(make(chan map[string]interface{}), &transport.TransportOptions{})
	if err == nil || tr != nil {
		t.Errorf("expected CLI discovery error, got %v, %v", tr, err)
	}
}

func TestConnectStringPromptWriteError(t *testing.T) {
	mockT := newMockTransport()
	initialized := false
	handleInitialization(mockT, nil)
	baseWrite := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		if initialized {
			return fmt.Errorf("stdin broken")
		}
		if strings.Contains(data, `"initialize"`) {
			initialized = true
		}
		return baseWrite(data)
	}

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	err := client.Connect(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "stdin broken") {
		t.Errorf("expected write error, got %v", err)
	}
}
