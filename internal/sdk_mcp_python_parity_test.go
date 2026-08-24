package internal

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Ports of the session/lifecycle tests from the Python SDK's rewritten
// tests/test_sdk_mcp_integration.py (#1218) that sdk_mcp_bridge_test.go and
// sdk_mcp_instance_test.go do not already cover. Where a Python test targets
// asyncio- or mcp-library-specific mechanics (an event loop, a lifespan, a
// cancellable coroutine), the port pins the observable wire behavior instead:
// Go handlers are goroutines that cannot be unwound, so "the waiter gave up"
// is a caller that stops reading the result channel.

// shrinkMCPShutdownGrace shortens the shutdown grace for the duration of a
// test, the way the Python suite monkeypatches SHUTDOWN_GRACE_SECONDS.
func shrinkMCPShutdownGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	old := mcpShutdownGrace
	mcpShutdownGrace = grace
	t.Cleanup(func() { mcpShutdownGrace = old })
}

// captureSlogHandler records log records so tests can assert on the warnings
// the bridge emits (or does not emit).
type captureSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r)
	h.mu.Unlock()
	return nil
}

func (h *captureSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureSlogHandler) WithGroup(string) slog.Handler      { return h }

// warnings returns the messages of records at level Warn or above.
func (h *captureSlogHandler) warnings() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		if r.Level >= slog.LevelWarn {
			out = append(out, r.Message)
		}
	}
	return out
}

// captureSlog installs a recording handler as the default logger for the
// duration of the test.
func captureSlog(t *testing.T) *captureSlogHandler {
	t.Helper()
	handler := &captureSlogHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(old) })
	return handler
}

// waitClosed waits for a channel to close, failing the test after a timeout.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("Timed out waiting for %s", what)
	}
}

// Mirrors test_message_before_any_initialize_starts_the_session: the first
// message — here a ping, before any initialize — lazily starts the session
// and is answered by the server.
func TestBridgeMessageBeforeInitializeStartsSession(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0"}
	bridge := newTestBridge(t, "srv", server)

	reply := bridge.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "ping",
	})
	if reply == nil {
		t.Fatal("Expected a reply for the pre-initialize ping")
	}
	if reply["id"] != 0 {
		t.Errorf("Expected id=0, got %v", reply["id"])
	}
	result, ok := reply["result"].(map[string]interface{})
	if !ok || len(result) != 0 {
		t.Errorf("Expected empty ping result, got %v", reply)
	}

	// The session was started by the ping.
	bridge.mu.Lock()
	started := bridge.session != nil
	bridge.mu.Unlock()
	if !started {
		t.Error("Expected the ping to start the session")
	}
}

// Mirrors test_close_tears_down_every_bridge_without_leaks: closing the Query
// tears down every bridge, an in-flight call does not keep Close from
// finishing (a factory handler ignores cancellation, so Close waits out the
// grace period for it), and closing again is a no-op.
func TestQueryCloseTearsDownEveryBridgeWithoutLeaks(t *testing.T) {
	shrinkMCPShutdownGrace(t, 500*time.Millisecond)

	release := make(chan struct{})
	slow := MCPTool{
		Name:        "slow",
		Description: "Sleeps",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			<-release
			return map[string]interface{}{"content": []map[string]interface{}{}}, nil
		},
	}
	servers := map[string]*MCPServer{
		"one": {Name: "one", Version: "1.0.0", Tools: []MCPTool{slow}},
		"two": {Name: "two", Version: "1.0.0"},
	}

	mt := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       mt,
		IsStreamingMode: true,
		SdkMCPServers:   testSDKServers(servers),
	})

	initializeMCPServer(t, q, "one")
	initializeMCPServer(t, q, "two")

	bridges := map[string]*SDKMCPBridge{}
	sessions := map[string]*sdkMCPSession{}
	for name, server := range q.sdkMCPServers {
		bridge := q.bridgeForServer(name, server)
		bridges[name] = bridge
		bridge.mu.Lock()
		sessions[name] = bridge.session
		bridge.mu.Unlock()
	}
	if sessions["one"] == nil || sessions["two"] == nil {
		t.Fatal("Expected both servers to have a live session")
	}

	// A tool call still in flight must not keep Close from finishing.
	inFlight := make(chan map[string]interface{}, 1)
	go func() {
		response, err := q.handleMCPMessage(map[string]interface{}{
			"server_name": "one",
			"message": map[string]interface{}{
				"jsonrpc": "2.0", "id": 9, "method": "tools/call",
				"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
			},
		})
		if err != nil {
			inFlight <- map[string]interface{}{"error": map[string]interface{}{"message": err.Error()}}
			return
		}
		inFlight <- response["mcp_response"].(map[string]interface{})
	}()
	time.Sleep(50 * time.Millisecond) // let the call register

	closed := make(chan struct{})
	go func() {
		if err := q.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
		close(closed)
	}()
	waitClosed(t, closed, "Query.Close with an in-flight call")

	// The in-flight call was settled, not left hanging.
	select {
	case reply := <-inFlight:
		errObj, ok := reply["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected the in-flight call to end with an error, got %v", reply)
		}
		if !strings.Contains(errObj["message"].(string), "session closed") {
			t.Errorf("Unexpected in-flight error: %v", errObj["message"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("In-flight call was not settled by Close")
	}

	// Every bridge refuses further messages and starts nothing new.
	for name, bridge := range bridges {
		reply := bridge.Handle(map[string]interface{}{
			"jsonrpc": "2.0", "id": 10, "method": "tools/list",
		})
		errObj, ok := reply["error"].(map[string]interface{})
		if !ok || !strings.Contains(errObj["message"].(string), "is closed") {
			t.Errorf("Bridge %q: expected an is-closed error, got %v", name, reply)
		}
		bridge.mu.Lock()
		session := bridge.session
		bridge.mu.Unlock()
		if session != nil {
			t.Errorf("Bridge %q: a message after close started a new session", name)
		}
	}

	// The quiet session wound down with Close; the blocked one finishes once
	// the handler lets go.
	waitClosed(t, sessions["two"].done, "session two to wind down")
	close(release)
	waitClosed(t, sessions["one"].done, "session one to wind down after release")

	// Closing again is a no-op.
	if err := q.Close(); err != nil {
		t.Errorf("Second Close failed: %v", err)
	}
}

// Mirrors test_close_does_not_hang_on_a_tool_blocked_outside_the_event_loop:
// a handler that cannot be cancelled (Go handlers are goroutines; Python's
// was blocked in a worker thread) must not hang Close — Close waits out the
// grace period, logs a warning, and stops waiting.
func TestBridgeCloseDoesNotHangOnBlockedTool(t *testing.T) {
	shrinkMCPShutdownGrace(t, 200*time.Millisecond)
	logs := captureSlog(t)

	release := make(chan struct{})
	entered := make(chan struct{})
	blocked := MCPTool{
		Name:        "blocked",
		Description: "Blocks ignoring cancellation",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			close(entered)
			<-release
			return map[string]interface{}{"content": []map[string]interface{}{}}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{blocked}}
	bridge := newTestBridge(t, "srv", server)
	client := &bridgeClient{bridge: bridge}
	client.request(t, "initialize", map[string]interface{}{"protocolVersion": "2025-06-18"})

	bridge.mu.Lock()
	session := bridge.session
	bridge.mu.Unlock()
	if session == nil {
		t.Fatal("Expected a live session after initialize")
	}

	inFlight := make(chan map[string]interface{}, 1)
	go func() {
		inFlight <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 5, "method": "tools/call",
			"params": map[string]interface{}{"name": "blocked", "arguments": map[string]interface{}{}},
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Blocked tool never started")
	}

	t0 := time.Now()
	bridge.Close()
	elapsed := time.Since(t0)
	if elapsed < mcpShutdownGrace {
		t.Errorf("Close returned after %v, before the %v grace period expired", elapsed, mcpShutdownGrace)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Close hung on the blocked tool: %v", elapsed)
	}

	// The waiter was failed rather than left hanging on the blocked tool.
	select {
	case reply := <-inFlight:
		if reply["error"] == nil {
			t.Errorf("Expected the in-flight call to fail, got %v", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("In-flight call was not settled")
	}

	// The abandonment was logged.
	found := false
	for _, message := range logs.warnings() {
		if strings.Contains(message, "did not stop") {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected a 'did not stop' warning, got %v", logs.warnings())
	}

	// Once the handler lets go, the abandoned session finishes on its own.
	close(release)
	waitClosed(t, session.done, "the abandoned session to finish after release")
}

// Mirrors test_close_with_a_cancellable_tool_in_flight_is_prompt_and_quiet: a
// handler that observes its context ends when the connection closes (go-sdk
// cancels in-flight handlers), so Close is prompt — no grace-period wait, no
// warning — and the handler never finishes normally.
func TestBridgeCloseWithCancellableToolInFlightIsPromptAndQuiet(t *testing.T) {
	logs := captureSlog(t)

	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	started := make(chan struct{})

	server := mcp.NewServer(&mcp.Implementation{Name: "srv", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "sleepy",
		Description: "Sleeps cancellably",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		record("start")
		close(started)
		select {
		case <-ctx.Done():
			record("cancelled")
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
			record("finished")
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "done"}},
		}, nil
	})
	client := newInstanceBridgeClient(t, "srv", server)

	inFlight := make(chan map[string]interface{}, 1)
	go func() {
		inFlight <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 7, "method": "tools/call",
			"params": map[string]interface{}{"name": "sleepy", "arguments": map[string]interface{}{}},
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Sleepy tool never started")
	}

	t0 := time.Now()
	client.bridge.Close()
	elapsed := time.Since(t0)
	if elapsed > mcpShutdownGrace/5 {
		t.Errorf("Close took %v with a cancellable tool in flight; want prompt (grace is %v)", elapsed, mcpShutdownGrace)
	}

	select {
	case reply := <-inFlight:
		if reply["error"] == nil {
			t.Errorf("Expected the in-flight call to fail on close, got %v", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("In-flight call was not settled")
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "start" || got[1] != "cancelled" {
		t.Errorf("Expected [start cancelled], got %v", got)
	}

	for _, message := range logs.warnings() {
		if strings.Contains(message, "SDK MCP server") {
			t.Errorf("Expected a quiet close, got warning: %s", message)
		}
	}
}

// Mirrors test_id_of_a_call_whose_waiter_gave_up_stays_reserved_until_the_server_answers:
// a caller that stops waiting does not free the request id — the server still
// owns it until it answers; only then can the id be reused. (Go cannot cancel
// the waiting goroutine the way anyio cancels the Python waiter; "giving up"
// is not reading the result channel until later.)
func TestBridgeIDStaysReservedUntilServerAnswers(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	slow := MCPTool{
		Name:        "slow",
		Description: "Finishes when released",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "late"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{slow}}
	client := newBridgeClient(t, "srv", server)

	call := map[string]interface{}{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
	}
	// The caller stops waiting (the CLI's own timeout, an interrupt).
	abandoned := make(chan map[string]interface{}, 1)
	go func() {
		abandoned <- client.send(call)
	}()
	time.Sleep(50 * time.Millisecond) // let the call register its id

	// The server still owns id 7, so a new request may not reuse it.
	reused := client.send(call)
	if reused == nil || reused["id"] != 7 {
		t.Fatalf("Expected the duplicate request to be answered, got %v", reused)
	}
	errObj, ok := reused["error"].(map[string]interface{})
	if !ok || !strings.Contains(errObj["message"].(string), "already in flight") {
		t.Fatalf("Expected an already-in-flight error, got %v", reused)
	}
	mu.Lock()
	if calls != 1 {
		t.Errorf("Expected the handler to have run once, got %d", calls)
	}
	mu.Unlock()

	// Once the server has answered, the id is free again.
	close(release)
	first := <-abandoned // drain the abandoned waiter
	if first["result"] == nil {
		t.Errorf("Expected the abandoned call to resolve with the late result, got %v", first)
	}
	again := client.send(call)
	if again == nil || again["error"] != nil {
		t.Fatalf("Expected the reused id to succeed after the answer, got %v", again)
	}
	if texts := textOf(t, again["result"].(map[string]interface{})); len(texts) != 1 || texts[0] != "late" {
		t.Errorf("Expected 'late', got %v", texts)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("Expected the handler to have run twice, got %d", calls)
	}
}

// Mirrors test_late_response_for_a_caller_that_gave_up_is_dropped: when the
// tool finishes after the caller stopped waiting, the orphaned response
// changes nothing and the session keeps serving.
func TestBridgeLateResponseForGoneCallerKeepsSessionServing(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	slow := MCPTool{
		Name:        "slow",
		Description: "Finishes when released",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			<-release
			close(finished)
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "late"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{slow, textTool("fast", "fast")}}
	client := newBridgeClient(t, "srv", server)

	// The caller gives up on the slow call: nothing reads its reply until the
	// end of the test.
	abandoned := make(chan map[string]interface{}, 1)
	go func() {
		abandoned <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 20, "method": "tools/call",
			"params": map[string]interface{}{"name": "slow", "arguments": map[string]interface{}{}},
		})
	}()
	time.Sleep(50 * time.Millisecond)

	close(release)
	waitClosed(t, finished, "the slow tool to finish")
	time.Sleep(50 * time.Millisecond) // let the orphaned response land

	// The session keeps serving.
	result := client.callTool(t, "fast", nil)
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "fast" {
		t.Errorf("Expected 'fast', got %v", texts)
	}

	// The late response went nowhere harmful.
	late := <-abandoned
	if late["id"] != 20 || late["result"] == nil {
		t.Errorf("Expected the late result for id=20, got %v", late)
	}
}

// Mirrors test_waiter_on_a_stuck_call_is_failed_once_the_grace_period_is_up:
// a caller waiting on a call whose handler never yields (a factory handler
// cannot be cancelled) is settled with an error once Close has given up on
// the session — it does not hang on the stuck handler.
func TestBridgeWaiterOnStuckCallFailsAfterGracePeriod(t *testing.T) {
	shrinkMCPShutdownGrace(t, 200*time.Millisecond)

	release := make(chan struct{})
	entered := make(chan struct{})
	blocked := MCPTool{
		Name:        "blocked",
		Description: "Blocks ignoring cancellation",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			close(entered)
			<-release
			return map[string]interface{}{"content": []map[string]interface{}{}}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{blocked}}
	bridge := newTestBridge(t, "srv", server)
	client := &bridgeClient{bridge: bridge}
	client.request(t, "initialize", map[string]interface{}{"protocolVersion": "2025-06-18"})

	bridge.mu.Lock()
	session := bridge.session
	bridge.mu.Unlock()

	outcome := make(chan string, 1)
	go func() {
		// A caller Query.Close does not cancel: it waits on the bridge
		// directly, as a second user of the bridge would.
		reply := bridge.Handle(map[string]interface{}{
			"jsonrpc": "2.0", "id": 5, "method": "tools/call",
			"params": map[string]interface{}{"name": "blocked", "arguments": map[string]interface{}{}},
		})
		outcome <- fmt.Sprintf("replied: %v", reply)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Blocked tool never started")
	}

	t0 := time.Now()
	bridge.Close()
	elapsed := time.Since(t0)

	// Close returned after the grace period; the waiter must have finished on
	// its own with an error, not hung on the stuck handler.
	if elapsed < mcpShutdownGrace {
		t.Errorf("Close returned after %v, before the %v grace period expired", elapsed, mcpShutdownGrace)
	}
	select {
	case got := <-outcome:
		if !strings.Contains(got, "session closed") {
			t.Errorf("Expected a session-closed error, got %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("The waiter hung on the stuck call")
	}

	close(release)
	waitClosed(t, session.done, "the abandoned session to finish after release")
}

// Mirrors test_cancelling_a_hand_built_servers_call_never_stops_the_server:
// cancelling a call to a hand-built server settles the waiter with "Request
// cancelled"; the handler — which cannot be unwound — runs to completion and
// its late answer is dropped, and the server keeps serving.
func TestBridgeCancellingHandBuiltCallNeverStopsServer(t *testing.T) {
	logs := captureSlog(t)

	release := make(chan struct{})
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "threaded", Version: "0.1.0"}, nil)
	for _, name := range []string{"blocking", "echo"} {
		name := name
		server.AddTool(&mcp.Tool{
			Name:        name,
			InputSchema: map[string]interface{}{"type": "object"},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if name == "blocking" {
				record("start")
				<-release // the idiomatic blocking call: no ctx to observe
				record("finish")
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: name}},
			}, nil
		})
	}
	client := newInstanceBridgeClient(t, "threaded", server)

	replies := make(chan map[string]interface{}, 1)
	go func() {
		replies <- client.send(map[string]interface{}{
			"jsonrpc": "2.0", "id": 21, "method": "tools/call",
			"params": map[string]interface{}{"name": "blocking", "arguments": map[string]interface{}{}},
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		startedCount := len(events)
		mu.Unlock()
		if startedCount > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Blocking tool never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	client.notify("notifications/cancelled", map[string]interface{}{
		"requestId": 21, "reason": "t",
	})

	select {
	case reply := <-replies:
		errObj, ok := reply["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected a cancellation error, got %v", reply)
		}
		if errObj["message"] != "Request cancelled" {
			t.Errorf("Expected 'Request cancelled', got %v", errObj["message"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cancelled call was not settled")
	}

	// The handler runs to completion as it always has; its late answer is
	// dropped and the server survives.
	close(release)
	deadline = time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		finishedCount := len(events)
		mu.Unlock()
		if finishedCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Blocking tool never finished: %v", events)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	result := client.callTool(t, "echo", nil)
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "echo" {
		t.Errorf("Expected the server to keep serving, got %v", texts)
	}
	for _, message := range logs.warnings() {
		if strings.Contains(message, "SDK MCP server") {
			t.Errorf("Expected no bridge warnings, got: %s", message)
		}
	}
}

// Mirrors test_session_cancelled_before_it_starts_still_closes: a session
// whose context is cancelled before its goroutines have run a single step
// still winds down and closes.
func TestBridgeSessionCancelledBeforeStartStillCloses(t *testing.T) {
	server := BuildToolServer("srv", "1.0.0", nil)
	session := newSDKMCPSession("srv", server)
	session.cancel() // before the reader has necessarily run a single step

	closed := make(chan struct{})
	go func() {
		session.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close() hung on a session cancelled before it started")
	}
	if !session.finished() {
		t.Error("Expected the session to be finished after close")
	}
}

// Mirrors test_input_schema_that_is_a_plain_class_lists_as_an_empty_object:
// a schema the factory cannot interpret (Python: a plain class; Go: anything
// that is not a schema map) lists as an empty object schema, and the tool
// still takes any arguments.
func TestBridgeOpaqueInputSchemaListsAsEmptyObject(t *testing.T) {
	opaque := MCPTool{
		Name:        "opaque",
		Description: "Takes anything",
		InputSchema: "not a schema map", // the Go analog of Python's plain class
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{opaque}}
	client := newBridgeClient(t, "srv", server)

	tools := client.listTools(t)
	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %v", tools)
	}
	want := map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	if !reflect.DeepEqual(tools[0]["inputSchema"], want) {
		t.Errorf("Expected inputSchema %v, got %v", want, tools[0]["inputSchema"])
	}

	result := client.callTool(t, "opaque", map[string]interface{}{"whatever": 1.0})
	if texts := textOf(t, result); len(texts) != 1 || texts[0] != "ok" {
		t.Errorf("Expected 'ok', got %v", texts)
	}
}

// Mirrors test_mixed_content_types_with_resource_link: text and image blocks
// pass through and a resource link flattens to text, in order.
func TestBridgeMixedContentTypesWithResourceLink(t *testing.T) {
	const pngData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"
	mixed := MCPTool{
		Name:        "get_mixed",
		Description: "Returns mixed content",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Here is the document:"},
					map[string]interface{}{"type": "image", "data": pngData, "mimeType": "image/png"},
					map[string]interface{}{
						"type": "resource_link",
						"name": "Report",
						"uri":  "https://example.com/report",
					},
				},
			}, nil
		},
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{mixed}}
	client := newBridgeClient(t, "srv", server)

	result := client.callTool(t, "get_mixed", nil)
	content := result["content"].([]map[string]interface{})
	if len(content) != 3 {
		t.Fatalf("Expected 3 content blocks, got %v", content)
	}
	if content[0]["type"] != "text" || content[0]["text"] != "Here is the document:" {
		t.Errorf("Unexpected text block: %v", content[0])
	}
	if content[1]["type"] != "image" || content[1]["data"] != pngData || content[1]["mimeType"] != "image/png" {
		t.Errorf("Unexpected image block: %v", content[1])
	}
	if content[2]["type"] != "text" || content[2]["text"] != "Report\nhttps://example.com/report" {
		t.Errorf("Unexpected flattened link block: %v", content[2])
	}
}

// Mirrors test_json_schema_dict_passthrough: a full JSON Schema dict goes on
// the wire unchanged.
func TestBridgeJSONSchemaDictPassthrough(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "minLength": 1},
			"age":  map[string]interface{}{"type": "integer", "minimum": 0},
		},
		"required": []interface{}{"name"},
	}
	validate := MCPTool{
		Name:        "validate",
		Description: "Validate input",
		InputSchema: schema,
		Handler:     textTool("validate", "OK").Handler,
	}
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{validate}}
	client := newBridgeClient(t, "srv", server)

	tools := client.listTools(t)
	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %v", tools)
	}
	if !reflect.DeepEqual(tools[0]["inputSchema"], schema) {
		t.Errorf("Expected the schema to pass through unchanged, got %v", tools[0]["inputSchema"])
	}
}

// Mirrors test_tool_list_is_stable: repeated tools/list calls return the same
// payload.
func TestBridgeToolListIsStable(t *testing.T) {
	server := &MCPServer{Name: "srv", Version: "1.0.0", Tools: []MCPTool{textTool("cached", "x")}}
	client := newBridgeClient(t, "srv", server)

	first := client.listTools(t)
	second := client.listTools(t)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Expected a stable tool list, got %v then %v", first, second)
	}
}
