package claude

// query_mcp_test.go — ports of the SDK-MCP stdin-lifecycle tests from Python's
// tests/test_query.py (TestStringPromptWithSdkMcpServers,
// TestAsyncIterablePromptWithSdkMcpServers and the MCP-flavored members of
// TestStdinStaysOpenWithInflightTasks).
//
// The stdin lifecycle differs in mechanism, not in contract: Python's Query
// closes stdin right after the prompt when no hooks/SDK-MCP servers are
// configured and holds it open for the first result otherwise; the Go Query
// closes stdin when a result frame arrives with no tasks in flight (see
// query.go), which keeps stdin available for MCP control responses in exactly
// the situations these tests pin down.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// makeGreeterServer builds the "greeter" SDK MCP server the Python suite uses
// (_make_greet_server).
func makeGreeterServer() *MCPSdkServerConfig {
	greetTool := Tool("greet", "Greet a user", map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		name, _ := args["name"].(string)
		return ToolResponse("Hi " + name), nil
	})
	return CreateSdkMcpServer("greeter", "1.0.0", []SdkMcpTool{greetTool})
}

// assistantFrame returns a minimal assistant message frame (mirrors
// _ASSISTANT_AND_RESULT[0] in python tests/test_query.py).
func assistantFrame() map[string]interface{} {
	return map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"role":    "assistant",
			"model":   "claude-sonnet-4-5",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "Hello!"}},
		},
	}
}

// mcpControlRequests are the MCP handshake frames the CLI sends once it knows
// an SDK server exists (mirrors _MCP_CONTROL_REQUESTS).
func mcpControlRequests() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"type":       "control_request",
			"request_id": "mcp_init_1",
			"request": map[string]interface{}{
				"subtype":     "mcp_message",
				"server_name": "greeter",
				"message": map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      1,
					"method":  "initialize",
					"params": map[string]interface{}{
						"protocolVersion": "2025-06-18",
						"capabilities":    map[string]interface{}{},
						"clientInfo":      map[string]interface{}{"name": "test-cli", "version": "0"},
					},
				},
			},
		},
		{
			"type":       "control_request",
			"request_id": "mcp_init_2",
			"request": map[string]interface{}{
				"subtype":     "mcp_message",
				"server_name": "greeter",
				"message": map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      2,
					"method":  "tools/list",
					"params":  map[string]interface{}{},
				},
			},
		},
	}
}

// mcpQueryFixture wires a MockTransport into the Query factory the way the
// Python suite patches SubprocessCLITransport, installing the initialization
// auto-responder. It returns the mock and a record of everything written.
// Channel prompts are streamed to stdin like the real transport's streamInput.
type mcpQueryFixture struct {
	mock    *MockTransport
	mu      sync.Mutex
	writes  []string
	respond *sync.Map // request_id -> chan struct{}, closed when its control response was written
}

func newMCPQueryFixture(t *testing.T) *mcpQueryFixture {
	t.Helper()
	fixture := &mcpQueryFixture{mock: newMockTransport(), respond: &sync.Map{}}
	fixture.mock.WriteFunc = func(data string) error {
		fixture.mu.Lock()
		fixture.writes = append(fixture.writes, data)
		fixture.mu.Unlock()

		var frame map[string]interface{}
		if err := json.Unmarshal([]byte(data), &frame); err == nil {
			if frame["type"] == "control_response" {
				if response, ok := frame["response"].(map[string]interface{}); ok {
					if reqID, ok := response["request_id"].(string); ok {
						if chRaw, ok := fixture.respond.Load(reqID); ok {
							ch := chRaw.(chan struct{})
							select {
							case <-ch:
							default:
								close(ch)
							}
						}
					}
				}
			}
		}
		return nil
	}
	handleInitialization(fixture.mock, nil)

	originalMakeTransport := makeTransport
	makeTransport = func(p interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		if ch, ok := p.(chan map[string]interface{}); ok {
			go func() {
				for msg := range ch {
					data, err := json.Marshal(msg)
					if err != nil {
						continue
					}
					if err := fixture.mock.Write(string(data) + "\n"); err != nil {
						return
					}
				}
			}()
		}
		return fixture.mock, nil
	}
	t.Cleanup(func() { makeTransport = originalMakeTransport })
	return fixture
}

// writtenFrames returns every written frame that parses as JSON.
func (f *mcpQueryFixture) writtenFrames() []map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	var frames []map[string]interface{}
	for _, data := range f.writes {
		var frame map[string]interface{}
		if err := json.Unmarshal([]byte(data), &frame); err == nil {
			frames = append(frames, frame)
		}
	}
	return frames
}

// expectResponseChannel registers a request id whose control response the
// feeder will wait for.
func (f *mcpQueryFixture) expectResponseChannel(requestID string) chan struct{} {
	ch := make(chan struct{})
	f.respond.Store(requestID, ch)
	return ch
}

// feedMCPHandshake sends the MCP control requests like the real CLI — each
// one only after the previous got its control response — then the assistant
// and result frames.
func (f *mcpQueryFixture) feedMCPHandshake(t *testing.T) {
	t.Helper()
	go func() {
		for _, req := range mcpControlRequests() {
			reqID := req["request_id"].(string)
			responded := f.expectResponseChannel(reqID)
			f.mock.readCh <- req
			select {
			case <-responded:
			case <-time.After(5 * time.Second):
				t.Errorf("Timed out waiting for the control response to %s", reqID)
				return
			}
		}
		f.mock.readCh <- assistantFrame()
		f.mock.readCh <- resultFrame("uuid-r1")
	}()
}

// assertMCPHandshakeSucceeded mirrors _assert_mcp_handshake_succeeded: the two
// MCP control requests got well-formed success responses, the initialize
// carrying the server identity and the listing carrying the greet tool.
func assertMCPHandshakeSucceeded(t *testing.T, fixture *mcpQueryFixture) {
	t.Helper()
	responses := map[string]map[string]interface{}{}
	for _, frame := range fixture.writtenFrames() {
		if frame["type"] != "control_response" {
			continue
		}
		response, _ := frame["response"].(map[string]interface{})
		reqID, _ := response["request_id"].(string)
		if strings.HasPrefix(reqID, "mcp_init_") {
			responses[reqID] = response
		}
	}
	if len(responses) != 2 {
		t.Fatalf("Expected 2 MCP control responses, got %d (%v)", len(responses), fixture.writtenFrames())
	}

	init := responses["mcp_init_1"]
	if init["subtype"] != "success" {
		t.Fatalf("Expected a success response for the initialize, got %v", init)
	}
	initPayload, _ := init["response"].(map[string]interface{})
	mcpResponse, _ := initPayload["mcp_response"].(map[string]interface{})
	result, _ := mcpResponse["result"].(map[string]interface{})
	serverInfo, _ := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "greeter" {
		t.Errorf("Expected serverInfo.name=greeter, got %v", serverInfo)
	}

	listing := responses["mcp_init_2"]
	if listing["subtype"] != "success" {
		t.Fatalf("Expected a success response for the tools/list, got %v", listing)
	}
	listPayload, _ := listing["response"].(map[string]interface{})
	listResponse, _ := listPayload["mcp_response"].(map[string]interface{})
	listResult, _ := listResponse["result"].(map[string]interface{})
	tools, _ := listResult["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool in the listing, got %v", listResult)
	}
	if name, _ := tools[0].(map[string]interface{})["name"].(string); name != "greet" {
		t.Errorf("Expected the greet tool, got %v", tools[0])
	}
}

// Mirrors test_string_prompt_waits_for_result_with_sdk_mcp_servers: with SDK
// MCP servers present, the user message is written and stdin stays open until
// the result (EndInput is driven by the result frame, not by the write).
func TestStringPromptWithSdkMcpServersWaitsForResult(t *testing.T) {
	fixture := newMCPQueryFixture(t)

	endInputCalled := make(chan struct{})
	fixture.mock.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		return nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		fixture.mock.readCh <- assistantFrame()
		fixture.mock.readCh <- resultFrame("uuid-r1")
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", &ClaudeAgentOptions{
		MCPServers: map[string]MCPServerConfig{"greeter": makeGreeterServer()},
	})

	done := make(chan struct{})
	var msgs []Message
	var errors []error
	go func() {
		msgs, errors = collectQuery(messages, errs)
		close(done)
	}()

	select {
	case <-endInputCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("EndInput was never called after the result")
	}
	fixture.mock.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query() did not complete within 5 seconds")
	}

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if _, ok := msgs[0].(*AssistantMessage); !ok {
		t.Errorf("Expected an AssistantMessage, got %T", msgs[0])
	}
	if _, ok := msgs[1].(*ResultMessage); !ok {
		t.Errorf("Expected a ResultMessage, got %T", msgs[1])
	}

	// The user message was written.
	foundUserMessage := false
	for _, frame := range fixture.writtenFrames() {
		if frame["type"] == "user" {
			if message, _ := frame["message"].(map[string]interface{}); message["content"] == "Hello" {
				foundUserMessage = true
			}
		}
	}
	if !foundUserMessage {
		t.Error("Expected the user message to be written to stdin")
	}
}

// Mirrors test_string_prompt_mcp_server_control_requests_succeed: MCP control
// requests arriving after the user message are handled successfully because
// stdin is still open.
func TestStringPromptMCPControlRequestsSucceed(t *testing.T) {
	fixture := newMCPQueryFixture(t)

	endInputCalled := make(chan struct{})
	fixture.mock.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		return nil
	}

	fixture.feedMCPHandshake(t)

	ctx := context.Background()
	messages, errs := Query(ctx, "Greet Alice", &ClaudeAgentOptions{
		MCPServers: map[string]MCPServerConfig{"greeter": makeGreeterServer()},
	})

	done := make(chan struct{})
	var msgs []Message
	var errors []error
	go func() {
		msgs, errors = collectQuery(messages, errs)
		close(done)
	}()

	select {
	case <-endInputCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("EndInput was never called after the result")
	}
	fixture.mock.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query() did not complete within 5 seconds")
	}

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	assertMCPHandshakeSucceeded(t, fixture)
}

// Mirrors test_async_iterable_with_sdk_mcp_servers: a channel (AsyncIterable)
// prompt with SDK MCP servers streams the user message and holds stdin until
// the result.
func TestChannelPromptWithSdkMcpServersWaitsForResult(t *testing.T) {
	fixture := newMCPQueryFixture(t)

	endInputCalled := make(chan struct{})
	fixture.mock.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		return nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		fixture.mock.readCh <- assistantFrame()
		fixture.mock.readCh <- resultFrame("uuid-r1")
	}()

	promptCh := make(chan map[string]interface{}, 1)
	promptCh <- map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"role": "user", "content": "Hello"},
	}
	close(promptCh)

	ctx := context.Background()
	messages, errs := Query(ctx, promptCh, &ClaudeAgentOptions{
		MCPServers: map[string]MCPServerConfig{"greeter": makeGreeterServer()},
	})

	done := make(chan struct{})
	var msgs []Message
	var errors []error
	go func() {
		msgs, errors = collectQuery(messages, errs)
		close(done)
	}()

	select {
	case <-endInputCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("EndInput was never called after the result")
	}
	fixture.mock.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query() did not complete within 5 seconds")
	}

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}

	foundUserMessage := false
	for _, frame := range fixture.writtenFrames() {
		if frame["type"] == "user" {
			if message, _ := frame["message"].(map[string]interface{}); message["content"] == "Hello" {
				foundUserMessage = true
			}
		}
	}
	if !foundUserMessage {
		t.Error("Expected the user message to be written to stdin")
	}
}

// Mirrors test_async_iterable_mcp_control_requests_succeed: MCP control
// requests are handled correctly with a channel (AsyncIterable) prompt.
func TestChannelPromptMCPControlRequestsSucceed(t *testing.T) {
	fixture := newMCPQueryFixture(t)

	endInputCalled := make(chan struct{})
	fixture.mock.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		return nil
	}

	fixture.feedMCPHandshake(t)

	promptCh := make(chan map[string]interface{}, 1)
	promptCh <- map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"role": "user", "content": "Greet Alice"},
	}
	close(promptCh)

	ctx := context.Background()
	messages, errs := Query(ctx, promptCh, &ClaudeAgentOptions{
		MCPServers: map[string]MCPServerConfig{"greeter": makeGreeterServer()},
	})

	done := make(chan struct{})
	var msgs []Message
	var errors []error
	go func() {
		msgs, errors = collectQuery(messages, errs)
		close(done)
	}()

	select {
	case <-endInputCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("EndInput was never called after the result")
	}
	fixture.mock.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query() did not complete within 5 seconds")
	}

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	assertMCPHandshakeSucceeded(t, fixture)
}

// Mirrors test_never_ending_shell_does_not_wedge_stdin_open: a backgrounded
// shell that never finishes must not hang the query — it is not a deferring
// task type, so the result closes stdin even with SDK MCP servers configured.
func TestNeverEndingShellDoesNotWedgeStdinOpen(t *testing.T) {
	fixture := newMCPQueryFixture(t)

	endInputCalled := make(chan struct{})
	fixture.mock.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		return nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		fixture.mock.readCh <- assistantFrame()
		fixture.mock.readCh <- taskStartedFrame("shell-1", "local_bash")
		fixture.mock.readCh <- map[string]interface{}{
			"type":    "system",
			"subtype": "background_tasks_changed",
			"tasks": []interface{}{
				map[string]interface{}{"task_id": "shell-1", "task_type": "local_bash"},
			},
		}
		fixture.mock.readCh <- resultFrame("uuid-r1")
		// The shell never exits: no terminal frame ever arrives. The query
		// must still close stdin and terminate.
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "start the dev server", &ClaudeAgentOptions{
		MCPServers: map[string]MCPServerConfig{"greeter": makeGreeterServer()},
	})

	done := make(chan struct{})
	go func() {
		for range messages {
		}
		for range errs {
		}
		close(done)
	}()

	select {
	case <-endInputCalled:
	case <-time.After(10 * time.Second):
		t.Fatal("stdin was never closed")
	}
	fixture.mock.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("query() did not terminate")
	}
}
