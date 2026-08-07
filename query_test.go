package claude

import (
	"context"
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
