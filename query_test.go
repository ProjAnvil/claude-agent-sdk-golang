package claude

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// TestQuery tests the one-shot Query function.
func TestQuery(t *testing.T) {
	// Save original factory
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	// Setup mock transport
	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	// Override factory
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		if promptStr, ok := prompt.(string); !ok || promptStr != "Say hello" {
			t.Errorf("Expected prompt 'Say hello', got %v", prompt)
		}
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)

		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",

			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-3-5-sonnet",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Hello world",
					},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result", // Signals end of stream
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-123",
		}
		mockTrans.Close()
	}()

	ctx := context.Background()
	messages, errors := Query(ctx, "Say hello", nil)

	// Collect results
	var msgs []Message
	var errs []error

	done := make(chan struct{})
	go func() {
		for msg := range messages {
			msgs = append(msgs, msg)
		}
		for err := range errors {
			errs = append(errs, err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for query results")
	}

	if len(errs) > 0 {
		t.Errorf("Unexpected errors: %v", errs)
	}

	if len(msgs) != 2 { // Text message + Result message
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	}

	// Verify text message
	asstMsg, ok := msgs[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected AssistantMessage, got %T", msgs[0])
	}

	if len(asstMsg.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(asstMsg.Content))
	}

	txtBlock, ok := asstMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected TextBlock, got %T", asstMsg.Content[0])
	}

	if txtBlock.Text != "Hello world" {
		t.Errorf("Expected text 'Hello world', got %q", txtBlock.Text)
	}

	// Verify result message
	_, ok = msgs[1].(*ResultMessage)
	if !ok {
		t.Errorf("Expected ResultMessage, got %T", msgs[1])
	}
}

// TestQueryWithOptions tests Query with options.
func TestQueryWithOptions(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		if opts.CWD != "/tmp" {
			t.Errorf("Expected CWD='/tmp', got %q", opts.CWD)
		}
		if opts.SystemPrompt != "sys_prompt" {
			t.Errorf("Expected SystemPrompt='sys_prompt', got %q", opts.SystemPrompt)
		}
		if len(opts.AllowedTools) != 2 || opts.AllowedTools[0] != "Read" || opts.AllowedTools[1] != "Write" {
			t.Errorf("Expected AllowedTools=['Read', 'Write'], got %v", opts.AllowedTools)
		}
		if opts.PermissionMode != "acceptEdits" {
			t.Errorf("Expected PermissionMode='acceptEdits', got %q", opts.PermissionMode)
		}
		if opts.MaxTurns != 5 {
			t.Errorf("Expected MaxTurns=5, got %d", opts.MaxTurns)
		}
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-123",
		}
		mockTrans.Close()
	}()

	opts := &ClaudeAgentOptions{
		CWD:            "/tmp",
		SystemPrompt:   "sys_prompt",
		AllowedTools:   []string{"Read", "Write"},
		PermissionMode: PermissionModeAcceptEdits,
		MaxTurns:       5,
	}

	ctx := context.Background()
	messages, _ := Query(ctx, "test", opts)

	// Drain messages
	for range messages {
	}
}

// TestQuerySync tests QuerySync helper.
func TestQuerySync(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-3-5-sonnet",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Sync"},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-123",
		}
		mockTrans.Close()
	}()

	ctx := context.Background()
	msgs, err := QuerySync(ctx, "test", nil)

	if err != nil {
		t.Errorf("QuerySync failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	}
}

// TestQueryDeadlockRegression verifies that Query() does not deadlock when
// the transport only closes stdout after receiving stdin EOF.
//
// This regression test simulates real CLI behavior:
//   - CLI keeps stdout open until stdin is closed (EOF)
//   - Without the fix, Query() goroutine hangs forever waiting for channels
//     to close, while channels only close when the goroutine exits (circular)
//   - The fix: call q.EndInput() after forwarding a ResultMessage
func TestQueryDeadlockRegression(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	endInputCalled := make(chan struct{})

	// EndInput simulates real CLI: close channels (CLI exits) after stdin EOF.
	mockTrans.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			mockTrans.shutdown()
		}()
		return nil
	}

	mockTrans.CloseFunc = func() error {
		_ = mockTrans.EndInputFunc()
		return nil
	}

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	// Send messages WITHOUT calling mockTrans.Close() — the key difference
	// from existing tests. The transport only closes after EndInput().
	go func() {
		time.Sleep(20 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-sonnet-4-5",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello!"},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-deadlock",
		}
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)

	var msgs []Message
	done := make(chan struct{})
	go func() {
		for msg := range messages {
			msgs = append(msgs, msg)
		}
		for range errs {
		}
		close(done)
	}()

	select {
	case <-done:
		// Success — Query completed without deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: Query() did not complete within 5 seconds")
	}

	select {
	case <-endInputCalled:
	default:
		t.Error("EndInput was never called after ResultMessage")
	}

	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages (assistant + result), got %d", len(msgs))
	}
	if len(msgs) >= 2 {
		if _, ok := msgs[0].(*AssistantMessage); !ok {
			t.Errorf("First message should be AssistantMessage, got %T", msgs[0])
		}
		if _, ok := msgs[1].(*ResultMessage); !ok {
			t.Errorf("Second message should be ResultMessage, got %T", msgs[1])
		}
	}
}

// TestQuerySyncDeadlockRegression is the QuerySync variant of the deadlock test.
func TestQuerySyncDeadlockRegression(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	endInputCalled := make(chan struct{})

	mockTrans.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			mockTrans.shutdown()
		}()
		return nil
	}

	mockTrans.CloseFunc = func() error {
		_ = mockTrans.EndInputFunc()
		return nil
	}

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		mockTrans.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":  "assistant",
				"model": "claude-sonnet-4-5",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Sync hello!"},
				},
			},
		}
		mockTrans.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(200),
			"duration_api_ms": float64(100),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-sync-deadlock",
		}
	}()

	ctx := context.Background()
	done := make(chan struct{})
	var msgs []Message
	var queryErr error
	go func() {
		msgs, queryErr = QuerySync(ctx, "Hello", nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK: QuerySync() did not complete within 5 seconds")
	}

	if queryErr != nil {
		t.Errorf("QuerySync failed: %v", queryErr)
	}
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgs))
	}
}

// taskStartedFrame returns a system/task_started frame for a deferring task
// type (mirrors _TASK_STARTED in python tests/test_query.py).
func taskStartedFrame(taskID, taskType string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "system",
		"subtype":     "task_started",
		"task_id":     taskID,
		"task_type":   taskType,
		"description": "background subagent",
		"uuid":        "uuid-ts-" + taskID,
		"session_id":  "test",
	}
}

// resultFrame returns a minimal result frame (mirrors _make_result in python
// tests/test_query.py).
func resultFrame(uuid string) map[string]interface{} {
	return map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(100),
		"duration_api_ms": float64(50),
		"is_error":        false,
		"num_turns":       float64(1),
		"session_id":      "sess-123",
		"uuid":            uuid,
	}
}

// TestTaskLifecycleTracker unit-tests the tracker transitions, mirroring
// python test_track_task_lifecycle_unit and
// test_shell_and_monitor_tasks_never_defer_the_close (#1088).
func TestTaskLifecycleTracker(t *testing.T) {
	tracker := newTaskLifecycleTracker()

	// task_started with a deferring task_type adds the task.
	tracker.track(taskStartedFrame("task-1", "local_agent"))
	if !tracker.hasInflight() {
		t.Error("Expected task-1 in flight after task_started(local_agent)")
	}

	// Non-terminal patch does not clear the task.
	tracker.track(map[string]interface{}{
		"subtype": "task_updated",
		"task_id": "task-1",
		"patch":   map[string]interface{}{"status": "running"},
	})
	if !tracker.hasInflight() {
		t.Error("Non-terminal task_updated patch must not clear the task")
	}

	// Patch without a map payload is ignored.
	tracker.track(map[string]interface{}{
		"subtype": "task_updated",
		"task_id": "task-1",
		"patch":   nil,
	})
	if !tracker.hasInflight() {
		t.Error("task_updated with nil patch must not clear the task")
	}

	// Terminal patch clears it.
	tracker.track(map[string]interface{}{
		"subtype": "task_updated",
		"task_id": "task-1",
		"patch":   map[string]interface{}{"status": "completed"},
	})
	if tracker.hasInflight() {
		t.Error("Terminal task_updated patch must clear the task")
	}

	// Draining an unknown/already-cleared task is a no-op, not an error.
	tracker.track(map[string]interface{}{
		"subtype":   "task_notification",
		"task_id":   "task-1",
		"status":    "completed",
		"task_type": "local_agent",
	})
	if tracker.hasInflight() {
		t.Error("Draining an unknown task must be a no-op")
	}

	// Frames without a task_id are ignored.
	tracker.track(map[string]interface{}{"subtype": "task_started", "task_type": "local_agent"})
	if tracker.hasInflight() {
		t.Error("Frame without task_id must be ignored")
	}

	// Only delegated agent work defers the close: background shells, monitors
	// and teammates can run forever, so tracking one would withhold the stdin
	// close permanently rather than briefly (#1088).
	for _, taskType := range []string{"local_bash", "monitor_mcp", "monitor_ws", "in_process_teammate"} {
		tracker.track(taskStartedFrame(taskType+"-1", taskType))
		if tracker.hasInflight() {
			t.Errorf("task_started(%s) must not be tracked", taskType)
		}
	}

	// A start frame with no task_type at all is not assumed to be an agent.
	tracker.track(map[string]interface{}{"subtype": "task_started", "task_id": "unknown-1"})
	if tracker.hasInflight() {
		t.Error("task_started without task_type must not be tracked")
	}

	// Agent work still defers, alongside the ignored shell.
	tracker.track(taskStartedFrame("task-2", "local_workflow"))
	if !tracker.hasInflight() {
		t.Error("Expected task-2 in flight after task_started(local_workflow)")
	}

	// task_notification drains it.
	tracker.track(map[string]interface{}{
		"subtype":   "task_notification",
		"task_id":   "task-2",
		"status":    "completed",
		"task_type": "local_workflow",
	})
	if tracker.hasInflight() {
		t.Error("task_notification must clear the task")
	}

	// background_tasks_changed is deliberately ignored in both directions: it
	// cannot add, and not even an empty snapshot clears a tracked task (#1088).
	tracker.track(map[string]interface{}{
		"type":    "system",
		"subtype": "background_tasks_changed",
		"tasks": []interface{}{
			map[string]interface{}{"task_id": "task-3", "task_type": "local_agent"},
		},
	})
	if tracker.hasInflight() {
		t.Error("background_tasks_changed must not add tasks")
	}
	tracker.track(taskStartedFrame("task-4", "local_agent"))
	tracker.track(map[string]interface{}{
		"type":    "system",
		"subtype": "background_tasks_changed",
		"tasks":   []interface{}{},
	})
	if !tracker.hasInflight() {
		t.Error("background_tasks_changed must not clear tracked tasks")
	}
}

// TestResultWithInflightTaskKeepsStdinOpen verifies that a result frame with
// tasks in flight does not close stdin, and that the first result after the
// task drains does — mirroring python
// test_result_with_inflight_task_keeps_stdin_open, parametrized over both
// drain frames (#1088).
func TestResultWithInflightTaskKeepsStdinOpen(t *testing.T) {
	drainFrames := map[string]map[string]interface{}{
		"task_notification": {
			"type":        "system",
			"subtype":     "task_notification",
			"task_id":     "task-1",
			"status":      "completed",
			"output_file": "/tmp/task-1.output",
			"summary":     "done",
			"uuid":        "uuid-tn1",
			"session_id":  "test",
		},
		"task_updated_terminal_patch": {
			"type":    "system",
			"subtype": "task_updated",
			"task_id": "task-1",
			"patch":   map[string]interface{}{"status": "completed"},
		},
	}

	for name, drainFrame := range drainFrames {
		t.Run(name, func(t *testing.T) {
			originalMakeTransport := makeTransport
			defer func() { makeTransport = originalMakeTransport }()

			mockTrans := newMockTransport()
			handleInitialization(mockTrans, nil)

			endInputCalled := make(chan struct{})
			mockTrans.EndInputFunc = func() error {
				select {
				case <-endInputCalled:
				default:
					close(endInputCalled)
				}
				return nil
			}

			makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
				return mockTrans, nil
			}

			// proceed gates the drain phase: the test closes it after
			// asserting stdin stayed open across the intermediate result.
			proceed := make(chan struct{})
			go func() {
				time.Sleep(10 * time.Millisecond)
				mockTrans.readCh <- map[string]interface{}{
					"type": "assistant",
					"message": map[string]interface{}{
						"role":  "assistant",
						"model": "claude-sonnet-4-5",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "Working on it"},
						},
					},
				}
				mockTrans.readCh <- taskStartedFrame("task-1", "local_agent")
				mockTrans.readCh <- resultFrame("uuid-r1")
				<-proceed
				mockTrans.readCh <- drainFrame
				mockTrans.readCh <- resultFrame("uuid-r2")
			}()

			ctx := context.Background()
			messages, errs := Query(ctx, "Hello", nil)

			var resultCount int
			done := make(chan struct{})
			go func() {
				defer close(done)
				for msg := range messages {
					if _, ok := msg.(*ResultMessage); ok {
						resultCount++
						if resultCount == 1 {
							// Intermediate result: give the loop a chance to
							// (incorrectly) close stdin, then assert it did
							// not and let the drain phase run.
							time.Sleep(50 * time.Millisecond)
							select {
							case <-endInputCalled:
								t.Error("EndInput called while a task was in flight")
							default:
							}
							close(proceed)
						}
					}
				}
				for range errs {
				}
			}()

			// The final result arrives with no tasks in flight and must close
			// stdin before the stream ends.
			select {
			case <-endInputCalled:
			case <-time.After(5 * time.Second):
				t.Fatal("EndInput was never called after the final result")
			}
			mockTrans.Close()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Query() did not complete within 5 seconds")
			}

			if resultCount != 2 {
				t.Errorf("Expected 2 result messages, got %d", resultCount)
			}
		})
	}
}

// TestResultWithNoInflightTasksClosesStdin verifies the unchanged behavior:
// a result frame with no tasks in flight closes stdin (#1088).
func TestResultWithNoInflightTasksClosesStdin(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	endInputCalled := make(chan struct{})
	mockTrans.EndInputFunc = func() error {
		select {
		case <-endInputCalled:
		default:
			close(endInputCalled)
		}
		return nil
	}

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- resultFrame("uuid-r1")
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range messages {
		}
		for range errs {
		}
	}()

	select {
	case <-endInputCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("EndInput was never called after a result with no tasks in flight")
	}
	mockTrans.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query() did not complete within 5 seconds")
	}
}

// --- verbatim_prompts (Python SDK v0.2.165, tests/test_verbatim_prompts.py) ---

// verbatimUserFrames decodes the captured transport writes and returns the
// frames of type "user" (initializes control_requests are dropped).
func verbatimUserFrames(t *testing.T, written []string) []map[string]interface{} {
	t.Helper()
	var frames []map[string]interface{}
	for _, w := range written {
		var frame map[string]interface{}
		if err := json.Unmarshal([]byte(w), &frame); err != nil {
			continue
		}
		if frame["type"] == "user" {
			frames = append(frames, frame)
		}
	}
	return frames
}

// waitUntil polls cond until it holds or the timeout hits.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestQueryVerbatimPromptsStringPrompt(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		originalMakeTransport := makeTransport
		defer func() { makeTransport = originalMakeTransport }()

		mockTrans := newMockTransport()
		var written []string
		handleInitialization(mockTrans, &written)

		makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
			if opts.VerbatimPrompts != enabled {
				t.Errorf("expected transport options VerbatimPrompts=%v, got %v", enabled, opts.VerbatimPrompts)
			}
			return mockTrans, nil
		}

		go func() {
			time.Sleep(10 * time.Millisecond)
			mockTrans.readCh <- resultFrame("uuid-vp")
			mockTrans.Close()
		}()

		opts := &ClaudeAgentOptions{VerbatimPrompts: enabled}
		messages, errs := Query(context.Background(), "read @/etc/hostname", opts)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for range messages {
			}
			for range errs {
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Query() did not complete within 5 seconds")
		}

		frames := verbatimUserFrames(t, written)
		if len(frames) != 1 {
			t.Fatalf("enabled=%v: expected 1 user frame, got %d (%v)", enabled, len(frames), written)
		}
		frame := frames[0]
		if enabled {
			if v, ok := frame["client_composed"].(bool); !ok || !v {
				t.Errorf("enabled=%v: expected client_composed=true on the prompt, got %v", enabled, frame["client_composed"])
			}
		} else if _, marked := frame["client_composed"]; marked {
			t.Errorf("enabled=false: expected no client_composed on the prompt, got %v", frame["client_composed"])
		}
		content, _ := frame["message"].(map[string]interface{})
		if content["content"] != "read @/etc/hostname" {
			t.Errorf("expected prompt text preserved, got %v", frame)
		}
	}
}

// syncWriteCapture records transport writes from any goroutine safely; the
// channel-prompt tests drain the prompt channel on a helper goroutine while
// the test polls, so the capture must be mutex-guarded.
type syncWriteCapture struct {
	mu      sync.Mutex
	written []string
}

func (c *syncWriteCapture) add(s string) {
	c.mu.Lock()
	c.written = append(c.written, s)
	c.mu.Unlock()
}

func (c *syncWriteCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.written...)
}

// captureWrites wraps a mock transport (already set up for initialization
// with handleInitialization(m, nil)) so every write is recorded.
func captureWrites(m *MockTransport) *syncWriteCapture {
	c := &syncWriteCapture{}
	orig := m.WriteFunc
	m.WriteFunc = func(data string) error {
		c.add(data)
		if orig != nil {
			return orig(data)
		}
		return nil
	}
	return c
}

// TestQueryVerbatimPromptsChannelPrompt covers the channel form. The real
// stamping for channel prompts happens in SubprocessTransport.streamInput
// (covered by TestStreamInputStampsChannelPrompts in internal/transport);
// this test drains the prompt channel exactly the way the subprocess
// transport does — stamping each message with the TransportOptions the SDK
// handed over — so it verifies Query()'s half of the contract: the option is
// propagated to the transport and every channel prompt carries the stamp.
func TestQueryVerbatimPromptsChannelPrompt(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		originalMakeTransport := makeTransport
		defer func() { makeTransport = originalMakeTransport }()

		mockTrans := newMockTransport()
		handleInitialization(mockTrans, nil)
		capture := captureWrites(mockTrans)

		makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
			if opts.VerbatimPrompts != enabled {
				t.Errorf("expected transport options VerbatimPrompts=%v, got %v", enabled, opts.VerbatimPrompts)
			}
			if ch, ok := prompt.(chan map[string]interface{}); ok {
				// Mimic SubprocessTransport.streamInput's contract for the
				// mock: stamp and forward every channel message.
				go func() {
					for msg := range ch {
						data, err := json.Marshal(transport.StampUserMessage(msg, opts.VerbatimPrompts))
						if err != nil {
							continue
						}
						mockTrans.Write(string(data) + "\n")
					}
				}()
			}
			return mockTrans, nil
		}

		first := map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"role": "user", "content": "one"},
		}
		second := map[string]interface{}{
			"type":            "user",
			"client_composed": false,
			"message":         map[string]interface{}{"role": "user", "content": "two"},
		}
		promptCh := make(chan map[string]interface{})

		messages, errs := Query(context.Background(), promptCh, &ClaudeAgentOptions{VerbatimPrompts: enabled})

		promptCh <- first
		promptCh <- second
		close(promptCh)

		waitUntil(t, 5*time.Second, func() bool {
			return len(verbatimUserFrames(t, capture.snapshot())) == 2
		}, "channel prompts were not all written to the transport")

		go func() {
			mockTrans.readCh <- resultFrame("uuid-vp-ch")
			mockTrans.Close()
		}()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for range messages {
			}
			for range errs {
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Query() did not complete within 5 seconds")
		}

		frames := verbatimUserFrames(t, capture.snapshot())
		if len(frames) != 2 {
			t.Fatalf("enabled=%v: expected 2 user frames, got %d", enabled, len(frames))
		}
		for i, frame := range frames {
			_, marked := frame["client_composed"]
			if enabled {
				if v, ok := frame["client_composed"].(bool); !ok || !v {
					t.Errorf("enabled=true: frame %d expected client_composed=true, got %v", i, frame["client_composed"])
				}
			} else if marked && i == 0 {
				t.Errorf("enabled=false: frame %d expected no client_composed, got %v", i, frame["client_composed"])
			}
		}
		if !enabled {
			// The caller's explicit value still rides through unchanged.
			if v, _ := frames[1]["client_composed"].(bool); v {
				t.Errorf("enabled=false: expected caller's client_composed=false preserved, got %v", frames[1]["client_composed"])
			}
		}
		// The caller's maps are never mutated.
		if _, marked := first["client_composed"]; marked {
			t.Errorf("caller's first map must not be mutated, got client_composed=%v", first["client_composed"])
		}
		if v, ok := second["client_composed"].(bool); !ok || v {
			t.Errorf("caller's second map must keep client_composed=false, got %v", second["client_composed"])
		}
	}
}
