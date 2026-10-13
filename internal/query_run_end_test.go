package internal

// Run-end state machine tests (#1190), mirroring Python's
// TestStdinStaysOpenUntilIdle, TestRunEndCeiling and TestRunEndCeilingFromEnv
// (tests/test_query.py). The harness drives a Query against the recording
// mock transport the way Python's _run_query_over drives query() over a
// scripted CLI: frames are fed one at a time, and between frames the test
// asserts on the recorded EndInput (stdin close) events.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// fastRunEndCeilingMS is the small ceiling the TestRunEndCeiling group uses
// (Python uses 100ms); open-assertions settle for settleMS first, well below
// it, and proof windows wait outlastCeilingMS.
const (
	fastRunEndCeilingMS = 150
	settleMS            = 50 * time.Millisecond
	outlastCeilingMS    = 400 * time.Millisecond
)

// --- frame builders (mirrors _session_state/_assistant/_make_result/...) ---

func sessionStateFrame(state string, sdkHostOnly bool) map[string]interface{} {
	frame := map[string]interface{}{
		"type":       "system",
		"subtype":    "session_state_changed",
		"state":      state,
		"uuid":       "uuid-state-" + state,
		"session_id": "test",
	}
	if sdkHostOnly {
		frame["sdk_host_only"] = true
	}
	return frame
}

func runEndAssistantFrame(parentToolUseID string) map[string]interface{} {
	frame := map[string]interface{}{
		"type":    "assistant",
		"message": map[string]interface{}{"role": "assistant"},
		"uuid":    "uuid-assistant",
	}
	if parentToolUseID != "" {
		frame["parent_tool_use_id"] = parentToolUseID
	}
	return frame
}

func runEndResultFrame(uuid string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "result",
		"subtype":     "success",
		"is_error":    false,
		"num_turns":   float64(1),
		"session_id":  "sess-123",
		"uuid":        uuid,
		"duration_ms": float64(10),
	}
}

func runEndTaskStartedFrame(taskID string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "system",
		"subtype":     "task_started",
		"task_id":     taskID,
		"task_type":   "local_agent",
		"description": "background subagent",
		"uuid":        "uuid-ts-" + taskID,
		"session_id":  "test",
	}
}

func runEndTaskNotificationFrame(taskID string) map[string]interface{} {
	return map[string]interface{}{
		"type":       "system",
		"subtype":    "task_notification",
		"task_id":    taskID,
		"status":     "completed",
		"uuid":       "uuid-tn-" + taskID,
		"session_id": "test",
	}
}

func runEndUserPrompt(text string) map[string]interface{} {
	return map[string]interface{}{
		"type":               "user",
		"session_id":         "",
		"message":            map[string]interface{}{"role": "user", "content": text},
		"parent_tool_use_id": nil,
	}
}

// --- harness ---

// runEndHarness drives one Query over the recording mock transport.
type runEndHarness struct {
	t  *testing.T
	tr *mockTransport
	q  *Query
	// forwardedStateFrames records the session_state_changed states that
	// reached callers (unmarked frames only; marked ones are dropped).
	forwardedStateFrames []string
}

// newRunEndQuery builds a Query with a hook configured (bidirectional needs,
// like Python's _run_query_over which always runs with a PreToolUse hook) and
// an explicit ceiling, then starts its read loop.
func newRunEndQuery(t *testing.T, ceilingMS int) *runEndHarness {
	t.Helper()
	tr := newMockTransport()
	hook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{}, nil
	}
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: true,
		Hooks: map[string][]HookMatcherInternal{
			"PreToolUse": {{Matcher: ".*", Hooks: []HookCallback{hook}}},
		},
	})
	// Python passes run_end_ceiling_ms straight to the Query ctor; Go's
	// NewQuery treats 0 as "unset", so set the explicit test value directly.
	q.runEndCeilingMS = ceilingMS
	q.Start()
	h := &runEndHarness{t: t, tr: tr, q: q}
	t.Cleanup(func() { _ = q.Close() })
	return h
}

// send delivers one frame and blocks until the read loop has processed it.
// Forwarded frames are awaited by uuid on rawMessages; marked
// session_state_changed frames (dropped before forwarding) are awaited on the
// session-state snapshot.
func (h *runEndHarness) send(frame map[string]interface{}) {
	h.t.Helper()
	h.tr.messages <- frame

	ftype, _ := frame["type"].(string)
	subtype, _ := frame["subtype"].(string)
	if hostOnly, _ := frame["sdk_host_only"].(bool); ftype == "system" && subtype == "session_state_changed" && hostOnly {
		want, _ := frame["state"].(string)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			h.q.mu.Lock()
			got := h.q.sessionState
			h.q.mu.Unlock()
			if got != nil && *got == want {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		h.t.Fatalf("dropped session_state_changed frame %q was never processed", want)
		return
	}

	uuid, _ := frame["uuid"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case got, ok := <-h.q.RawMessages():
			if !ok {
				h.t.Fatal("rawMessages closed while waiting for frame to be forwarded")
			}
			if g, _ := got["uuid"].(string); g == uuid {
				if subtype == "session_state_changed" {
					// The frame reached the caller: it was unmarked.
					state, _ := frame["state"].(string)
					h.forwardedStateFrames = append(h.forwardedStateFrames, state)
				}
				return
			}
			// A different frame (only the prompt echo could interleave);
			// keep draining until the awaited uuid shows up.
		case <-time.After(50 * time.Millisecond):
		}
	}
	h.t.Fatalf("frame %q (%s/%s) was never forwarded", uuid, ftype, subtype)
}

// letWaiterRun gives a woken stdin-closing waiter goroutine time to (wrongly)
// call EndInput — the Go equivalent of Python's _let_waiter_run event-loop
// yields. Assertions that stdin is still open must outlast this.
func (h *runEndHarness) letWaiterRun() {
	time.Sleep(settleMS)
}

// stdinOpen reports whether EndInput has not been called yet.
func (h *runEndHarness) stdinOpen() bool {
	return h.tr.endInputCalls() == 0
}

// waitEndInput blocks until EndInput was called or fails the test.
func (h *runEndHarness) waitEndInput(within time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if h.tr.endInputCalls() > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatal("stdin was never closed (EndInput never called)")
}

// writePrompt mimics the public string-prompt path (Python client.py): write
// the stamped user message, then wait for the run to end before closing
// stdin.
func (h *runEndHarness) writePrompt(text string) {
	h.t.Helper()
	data, err := json.Marshal(transport.StampUserMessage(runEndUserPrompt(text), h.q.verbatimPrompts))
	if err != nil {
		h.t.Fatalf("marshal prompt: %v", err)
	}
	if err := h.q.Write(string(data) + "\n"); err != nil {
		h.t.Fatalf("write prompt: %v", err)
	}
	go h.q.WaitForResultAndEndInput()
}

// userWrites returns the user frames written to the transport so far.
func (h *runEndHarness) userWrites() []map[string]interface{} {
	h.t.Helper()
	var frames []map[string]interface{}
	for _, w := range h.tr.getWritten() {
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

// waitUserWrites blocks until n user frames have been written.
func (h *runEndHarness) waitUserWrites(n int) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.userWrites()) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("expected %d user writes, got %d", n, len(h.userWrites()))
}

// ---------------------------------------------------------------------------
// TestStdinStaysOpenUntilIdle
// ---------------------------------------------------------------------------

// TestTaskSettledBeforeResultKeepsStdinOpenUntilIdle mirrors Python
// test_task_settled_before_result_keeps_stdin_open_until_idle.
func TestTaskSettledBeforeResultKeepsStdinOpenUntilIdle(t *testing.T) {
	h := newRunEndQuery(t, DefaultRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndTaskStartedFrame("task-1"))
	h.send(runEndTaskNotificationFrame("task-1"))
	h.send(runEndResultFrame("uuid-r1"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed after the first result; a settled task still wakes a follow-up turn")
	}
	h.send(runEndResultFrame("uuid-r2"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed after the second result; the CLI still reports running")
	}
	h.send(sessionStateFrame("idle", true))
	h.waitEndInput(2 * time.Second)
	if got := h.tr.endInputCalls(); got != 1 {
		t.Errorf("expected exactly 1 EndInput, got %d", got)
	}
	// The CLI marked the state frames sdk_host_only, so the caller never
	// sees them.
	if len(h.forwardedStateFrames) != 0 {
		t.Errorf("marked session_state frames leaked to the caller: %v", h.forwardedStateFrames)
	}
}

// TestOnlyMarkedStateFramesAreDropped mirrors Python
// test_only_marked_frames_are_dropped: marked frames were sent only because
// the SDK asked; unmarked ones mean the caller opted in
// (CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS), and both drive the run end the
// same way.
func TestOnlyMarkedStateFramesAreDropped(t *testing.T) {
	for _, sdkHostOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "marked", false: "unmarked"}[sdkHostOnly], func(t *testing.T) {
			h := newRunEndQuery(t, DefaultRunEndCeilingMS)
			h.writePrompt("Hello")

			h.send(sessionStateFrame("running", sdkHostOnly))
			h.send(runEndResultFrame("uuid-r1"))
			h.letWaiterRun()
			if !h.stdinOpen() {
				t.Error("stdin closed at the result while the CLI reports running")
			}
			h.send(sessionStateFrame("idle", sdkHostOnly))
			h.waitEndInput(2 * time.Second)

			want := []string(nil)
			if !sdkHostOnly {
				want = []string{"running", "idle"}
			}
			if got := h.forwardedStateFrames; !equalStrings(got, want) {
				t.Errorf("forwarded state frames = %v, want %v", got, want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNoStateClosesAtFirstResult mirrors Python
// test_no_state_frames_closes_stdin_at_the_first_result: a CLI too old to
// honor CLAUDE_CODE_SDK_READS_SESSION_STATE sends no frames; the result is
// then all there is to go on.
func TestNoStateClosesAtFirstResult(t *testing.T) {
	h := newRunEndQuery(t, 0)
	h.writePrompt("Hello")

	h.send(runEndAssistantFrame(""))
	h.send(runEndResultFrame("uuid-r1"))
	h.waitEndInput(2 * time.Second)
}

// TestIdleJustBeforeResultEndsRunAtThatResult mirrors Python
// test_idle_just_before_result_ends_the_run_at_the_result.
func TestIdleJustBeforeResultEndsRunAtThatResult(t *testing.T) {
	h := newRunEndQuery(t, 0)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(sessionStateFrame("idle", true))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("idle before any result must not end the run")
	}
	h.send(runEndResultFrame("uuid-r1"))
	h.waitEndInput(2 * time.Second)
}

// TestIdleBeforeAnyResultDoesNotEndRun mirrors Python
// test_idle_before_any_result_does_not_end_the_run.
func TestIdleBeforeAnyResultDoesNotEndRun(t *testing.T) {
	h := newRunEndQuery(t, 0)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("idle", true))
	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed after a result whose turn never saw its own idle")
	}
	h.send(sessionStateFrame("idle", true))
	h.waitEndInput(2 * time.Second)
}

// TestIdleWithTrackedTaskInFlightKeepsStdinOpen mirrors Python
// test_idle_with_a_tracked_task_in_flight_keeps_stdin_open: a CLI that
// reports "idle" at every turn end still defers to the task ledger (#1088).
func TestIdleWithTrackedTaskInFlightKeepsStdinOpen(t *testing.T) {
	h := newRunEndQuery(t, 0)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndTaskStartedFrame("task-1"))
	h.send(runEndResultFrame("uuid-r1"))
	h.send(sessionStateFrame("idle", true))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed at idle while a tracked task was in flight")
	}
	h.send(runEndTaskNotificationFrame("task-1"))
	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r2"))
	h.send(sessionStateFrame("idle", true))
	h.waitEndInput(2 * time.Second)
}

// TestChannelPromptWaitsForIdle mirrors Python test_async_iterable_prompt_
// waits_for_idle.
func TestChannelPromptWaitsForIdle(t *testing.T) {
	h := newRunEndQuery(t, 0)
	promptCh := make(chan map[string]interface{})
	go h.q.StreamInput(promptCh)
	promptCh <- runEndUserPrompt("Hello")
	close(promptCh)
	h.waitUserWrites(1)

	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed at the result while the CLI reports running")
	}
	h.send(sessionStateFrame("idle", true))
	h.waitEndInput(2 * time.Second)
}

// TestLaterChannelPromptWaitsForOwnRun mirrors Python
// test_stream_last_prompt_waits_for_its_own_run: a prompt written after an
// earlier prompt's run ended owes a run of its own.
func TestLaterChannelPromptWaitsForOwnRun(t *testing.T) {
	h := newRunEndQuery(t, 0)
	promptCh := make(chan map[string]interface{})
	go h.q.StreamInput(promptCh)

	promptCh <- runEndUserPrompt("first")
	h.waitUserWrites(1)
	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.send(sessionStateFrame("idle", true))
	h.letWaiterRun()

	// The first run ended; the second prompt reopens the run before it is
	// written, so the wait that follows is for the second run, not a
	// channel snapshot of the ended first one.
	promptCh <- runEndUserPrompt("second")
	close(promptCh)
	h.waitUserWrites(2)
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed right after the second prompt; it owes a run of its own")
	}
	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r2"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed at the second result while the CLI reports running")
	}
	h.send(sessionStateFrame("idle", true))
	h.waitEndInput(2 * time.Second)
}

// TestStreamReopensForWorkAfterIdle mirrors Python
// test_stream_reopens_for_work_the_cli_takes_up_after_idle, parameterized
// over the wake frame: a background task that wakes the CLI after the run
// ended reopens it, as long as the caller's input had not ended yet.
func TestStreamReopensForWorkAfterIdle(t *testing.T) {
	for _, wake := range []string{"running", "requires_action"} {
		t.Run(wake, func(t *testing.T) {
			h := newRunEndQuery(t, 0)
			promptCh := make(chan map[string]interface{})
			go h.q.StreamInput(promptCh)

			promptCh <- runEndUserPrompt("Hello")
			h.waitUserWrites(1)
			h.send(sessionStateFrame("running", true))
			h.send(runEndResultFrame("uuid-r1"))
			h.send(sessionStateFrame("idle", true))
			h.letWaiterRun()

			// Work the CLI took up after the run ended reopens it.
			h.send(sessionStateFrame(wake, true))
			h.letWaiterRun()

			// The caller's input ends now; the wait must be on the reopened
			// run, not the ended one, or stdin would close here.
			close(promptCh)
			h.letWaiterRun()
			if !h.stdinOpen() {
				t.Error("stdin closed when the input stream ended; the reopened run still owes a turn")
			}
			h.send(runEndResultFrame("uuid-r2"))
			h.send(sessionStateFrame("idle", true))
			h.waitEndInput(2 * time.Second)
		})
	}
}

// ---------------------------------------------------------------------------
// TestRunEndCeiling
// ---------------------------------------------------------------------------

// TestCeilingEndsRunWithNoIdle mirrors Python test_ceiling_ends_the_run_with_
// no_idle: a late idle does not close stdin a second time.
func TestCeilingEndsRunWithNoIdle(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed right after the result; the ceiling had not passed")
	}
	h.waitEndInput(2 * time.Second)
	closedAt := h.tr.endInputCalls()

	// A late idle must not close stdin again.
	h.send(sessionStateFrame("idle", true))
	h.letWaiterRun()
	if got := h.tr.endInputCalls(); got != closedAt {
		t.Errorf("late idle closed stdin again: %d EndInput calls after %d", got, closedAt)
	}
}

// TestCeilingMainThreadTurnStopsCeiling mirrors Python
// test_main_thread_turn_stops_the_ceiling: the turn a finished background
// agent woke starts before the ceiling, then runs well past it; its result
// starts the wait between turns over.
func TestCeilingMainThreadTurnStopsCeiling(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.send(runEndAssistantFrame(""))
	time.Sleep(outlastCeilingMS)
	if !h.stdinOpen() {
		t.Error("ceiling fired mid-turn; it counts only the wait between turns")
	}
	h.send(runEndResultFrame("uuid-r2"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed right after the second result; the ceiling restarted")
	}
	h.waitEndInput(2 * time.Second)
}

// TestCeilingSubagentFramesDoNotStopCeiling mirrors Python
// test_subagent_messages_do_not_stop_the_ceiling: only main-thread activity
// is a new turn; a background agent's own messages are the very work the
// ceiling bounds.
func TestCeilingSubagentFramesDoNotStopCeiling(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.send(runEndAssistantFrame("toolu_agent"))
	h.waitEndInput(2 * time.Second)
}

// TestCeilingRequiresActionStopsCeiling mirrors Python
// test_requires_action_stops_the_ceiling: a background agent's permission
// prompt waits on the host well past the ceiling; once answered, the wait
// between turns starts over.
func TestCeilingRequiresActionStopsCeiling(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	h.send(sessionStateFrame("requires_action", true))
	time.Sleep(outlastCeilingMS)
	if !h.stdinOpen() {
		t.Error("ceiling fired while the host was answering a request")
	}
	h.send(sessionStateFrame("running", true))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed right after the answer; the wait between turns restarted")
	}
	h.waitEndInput(2 * time.Second)
}

// TestCeilingNotArmedMidTurn mirrors Python test_ceiling_is_not_armed_mid_
// turn: a "running" after an answered request inside a turn does not start
// the clock; only the wait between turns counts.
func TestCeilingNotArmedMidTurn(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	// The follow-up turn starts, asks for a permission, gets it, then runs a
	// long tool with no main-thread output.
	h.send(runEndAssistantFrame(""))
	h.send(sessionStateFrame("requires_action", true))
	h.send(sessionStateFrame("running", true))
	time.Sleep(outlastCeilingMS)
	if !h.stdinOpen() {
		t.Error("ceiling fired mid-turn (between an answered request and the turn's result)")
	}
	h.send(runEndResultFrame("uuid-r2"))
	h.waitEndInput(2 * time.Second)
}

// TestCeilingResultWhileAnsweringDoesNotArm mirrors Python
// test_result_while_answering_a_request_does_not_arm: a background agent's
// request the SDK is still answering when the turn's result arrives keeps the
// clock stopped until it is answered.
func TestCeilingResultWhileAnsweringDoesNotArm(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(sessionStateFrame("requires_action", true))
	h.send(runEndResultFrame("uuid-r1"))
	time.Sleep(outlastCeilingMS)
	if !h.stdinOpen() {
		t.Error("ceiling armed by a result that arrived while a request was being answered")
	}
	h.send(sessionStateFrame("running", true))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed right after the answer; the wait between turns restarted")
	}
	h.waitEndInput(2 * time.Second)
}

// TestCeilingLeavesTrackedAgentAlone mirrors Python test_ceiling_leaves_a_
// tracked_agent_alone (#1088): the ceiling does not cut off a tracked
// background agent still in flight; the wait between turns starts over once
// it settles.
func TestCeilingLeavesTrackedAgentAlone(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	h.writePrompt("Hello")

	h.send(sessionStateFrame("running", true))
	h.send(runEndTaskStartedFrame("task-1"))
	h.send(runEndResultFrame("uuid-r1"))
	time.Sleep(outlastCeilingMS)
	if !h.stdinOpen() {
		t.Error("ceiling cut off a tracked background agent still in flight")
	}
	h.send(runEndTaskNotificationFrame("task-1"))
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin closed right after the task settled; the wait between turns restarted")
	}
	h.waitEndInput(2 * time.Second)
}

// TestCeilingZeroAndHugeWaitForIdle mirrors Python
// test_waits_for_idle_with_no_or_a_huge_ceiling: 0 means no ceiling; a huge
// one is honored as a very long wait, not one that fires at once.
func TestCeilingZeroAndHugeWaitForIdle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ceiling int
	}{
		{"zero", 0},
		{"huge", 1 << 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRunEndQuery(t, tc.ceiling)
			h.writePrompt("Hello")

			h.send(sessionStateFrame("running", true))
			h.send(runEndResultFrame("uuid-r1"))
			time.Sleep(outlastCeilingMS)
			if !h.stdinOpen() {
				t.Error("ceiling fired although it was disabled (0) or huge")
			}
			h.send(sessionStateFrame("idle", true))
			h.waitEndInput(2 * time.Second)
		})
	}
}

// TestCeilingEndedStreamReopensForMainThreadTurn mirrors Python
// test_ceiling_ended_stream_reopens_for_a_main_thread_turn: the ceiling ends
// the run while the caller's input is still open, a background agent's turn
// then starts streaming with no state change, and the caller's input ends —
// stdin must stay open until that turn's idle.
func TestCeilingEndedStreamReopensForMainThreadTurn(t *testing.T) {
	h := newRunEndQuery(t, fastRunEndCeilingMS)
	promptCh := make(chan map[string]interface{})
	go h.q.StreamInput(promptCh)

	promptCh <- runEndUserPrompt("Hello")
	h.waitUserWrites(1)
	h.send(sessionStateFrame("running", true))
	h.send(runEndResultFrame("uuid-r1"))
	// The ceiling passes while the caller's input is still open; no waiter
	// exists yet (the prompt channel has not closed), so the run simply
	// ends and stdin stays as it is.
	time.Sleep(outlastCeilingMS)
	// The background agent finishes after all; its turn starts streaming
	// with no state change, and the caller's input ends.
	h.send(runEndAssistantFrame(""))
	h.letWaiterRun()
	close(promptCh)
	h.letWaiterRun()
	if !h.stdinOpen() {
		t.Error("stdin already closed; the reopened turn still needs it")
	}
	h.send(runEndResultFrame("uuid-r2"))
	h.send(sessionStateFrame("idle", true))
	h.waitEndInput(2 * time.Second)
}

// TestNoCeilingArmedOnceStdinClosed mirrors Python test_no_ceiling_is_armed_
// once_stdin_is_closed: frames still arrive while the CLI winds down after
// stdin closed; a ceiling armed then would outlive the run it bounds.
func TestNoCeilingArmedOnceStdinClosed(t *testing.T) {
	tr := newMockTransport()
	hook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{}, nil
	}
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: true,
		Hooks: map[string][]HookMatcherInternal{
			"PreToolUse": {{Matcher: ".*", Hooks: []HookCallback{hook}}},
		},
	})
	defer func() { _ = q.Close() }()
	q.runEndCeilingMS = 50

	q.mu.Lock()
	q.onSessionState("running")
	q.resultReceived = true
	q.armRunEndCeiling()
	timerArmed := q.ceilingTimer != nil
	q.mu.Unlock()
	if !timerArmed {
		t.Fatal("expected a ceiling timer to be armed before the run ended")
	}

	q.mu.Lock()
	q.endRun()
	q.mu.Unlock()
	q.WaitForResultAndEndInput()
	if got := tr.endInputCalls(); got != 1 {
		t.Fatalf("expected 1 EndInput, got %d", got)
	}

	// Work after stdin closed neither reopens the run nor arms a ceiling
	// for it.
	q.mu.Lock()
	q.onSessionState("running")
	q.armRunEndCeiling()
	armed := q.ceilingTimer != nil
	runEnded := q.runEnded
	q.mu.Unlock()
	if armed {
		t.Error("ceiling armed after stdin closed (runFinal)")
	}
	if !runEnded {
		t.Error("run reopened after stdin closed")
	}
}

// ---------------------------------------------------------------------------
// TestRunEndCeilingFromEnv
// ---------------------------------------------------------------------------

// TestRunEndCeilingFromEnv mirrors Python TestRunEndCeilingFromEnv: the
// variable is read as the CLI will see it — options.Env overrides the
// inherited environment; 0 means no limit; anything that is not a plain
// non-negative integer falls back to the default.
func TestRunEndCeilingFromEnv(t *testing.T) {
	tests := []struct {
		name       string
		optionsEnv map[string]string
		ambient    string
		hasAmbient bool
		expected   int
	}{
		{name: "default", optionsEnv: map[string]string{}, expected: DefaultRunEndCeilingMS},
		{name: "zero", optionsEnv: map[string]string{runEndCeilingEnv: "0"}, expected: 0},
		{name: "whitespace", optionsEnv: map[string]string{runEndCeilingEnv: " 250 "}, expected: 250},
		{name: "ambient", optionsEnv: map[string]string{}, ambient: "1234", hasAmbient: true, expected: 1234},
		{name: "options_over_ambient", optionsEnv: map[string]string{runEndCeilingEnv: "250"}, ambient: "1234", hasAmbient: true, expected: 250},
		{name: "not_a_number", optionsEnv: map[string]string{runEndCeilingEnv: "abc"}, expected: DefaultRunEndCeilingMS},
		{name: "exponent_notation", optionsEnv: map[string]string{runEndCeilingEnv: "1e6"}, expected: DefaultRunEndCeilingMS},
		{name: "negative", optionsEnv: map[string]string{runEndCeilingEnv: "-5"}, expected: DefaultRunEndCeilingMS},
		{name: "empty_option_wins", optionsEnv: map[string]string{runEndCeilingEnv: ""}, ambient: "1234", hasAmbient: true, expected: DefaultRunEndCeilingMS},
		{name: "huge_2pow31", optionsEnv: map[string]string{runEndCeilingEnv: "2147483648"}, expected: 2147483648},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.hasAmbient {
				t.Setenv(runEndCeilingEnv, tt.ambient)
			} else {
				os.Unsetenv(runEndCeilingEnv)
			}
			if got := RunEndCeilingMS(tt.optionsEnv); got != tt.expected {
				t.Errorf("RunEndCeilingMS(%v) = %d, want %d", tt.optionsEnv, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task ledger (ported from the public taskLifecycleTracker unit tests)
// ---------------------------------------------------------------------------

// TestTrackSystemFrame unit-tests the ledger transitions, mirroring the
// Python test_track_task_lifecycle_unit and
// test_shell_and_monitor_tasks_never_defer_the_close (#1088).
func TestTrackSystemFrame(t *testing.T) {
	q := NewQuery(QueryConfig{Transport: newMockTransport()})

	// task_started with a deferring task_type adds the task.
	q.trackSystemFrame(runEndTaskStartedFrame("task-1"))
	if len(q.inflightTasks) == 0 {
		t.Error("Expected task-1 in flight after task_started(local_agent)")
	}

	// Non-terminal patch does not clear the task.
	q.trackSystemFrame(map[string]interface{}{
		"subtype": "task_updated",
		"task_id": "task-1",
		"patch":   map[string]interface{}{"status": "running"},
	})
	if len(q.inflightTasks) == 0 {
		t.Error("Non-terminal task_updated patch must not clear the task")
	}

	// Patch without a map payload is ignored.
	q.trackSystemFrame(map[string]interface{}{
		"subtype": "task_updated",
		"task_id": "task-1",
		"patch":   nil,
	})
	if len(q.inflightTasks) == 0 {
		t.Error("task_updated with nil patch must not clear the task")
	}

	// Terminal patch clears it.
	q.trackSystemFrame(map[string]interface{}{
		"subtype": "task_updated",
		"task_id": "task-1",
		"patch":   map[string]interface{}{"status": "completed"},
	})
	if len(q.inflightTasks) != 0 {
		t.Error("Terminal task_updated patch must clear the task")
	}

	// Draining an unknown/already-cleared task is a no-op, not an error.
	q.trackSystemFrame(map[string]interface{}{
		"subtype":   "task_notification",
		"task_id":   "task-1",
		"status":    "completed",
		"task_type": "local_agent",
	})
	if len(q.inflightTasks) != 0 {
		t.Error("Draining an unknown task must be a no-op")
	}

	// Frames without a task_id are ignored.
	q.trackSystemFrame(map[string]interface{}{"subtype": "task_started", "task_type": "local_agent"})
	if len(q.inflightTasks) != 0 {
		t.Error("Frame without task_id must be ignored")
	}

	// Only delegated agent work defers the close: background shells,
	// monitors and teammates can run forever, so tracking one would withhold
	// the stdin close permanently rather than briefly (#1088).
	for _, taskType := range []string{"local_bash", "monitor_mcp", "monitor_ws", "in_process_teammate"} {
		frame := runEndTaskStartedFrame(taskType + "-1")
		frame["task_type"] = taskType
		q.trackSystemFrame(frame)
		if len(q.inflightTasks) != 0 {
			t.Errorf("task_started(%s) must not be tracked", taskType)
		}
	}

	// A start frame with no task_type at all is not assumed to be an agent.
	q.trackSystemFrame(map[string]interface{}{"subtype": "task_started", "task_id": "unknown-1"})
	if len(q.inflightTasks) != 0 {
		t.Error("task_started without task_type must not be tracked")
	}

	// Agent work still defers.
	started := runEndTaskStartedFrame("task-2")
	started["task_type"] = "local_workflow"
	q.trackSystemFrame(started)
	if len(q.inflightTasks) == 0 {
		t.Error("Expected task-2 in flight after task_started(local_workflow)")
	}

	// task_notification drains it.
	q.trackSystemFrame(map[string]interface{}{
		"subtype":   "task_notification",
		"task_id":   "task-2",
		"status":    "completed",
		"task_type": "local_workflow",
	})
	if len(q.inflightTasks) != 0 {
		t.Error("task_notification must clear the task")
	}

	// background_tasks_changed is deliberately ignored in both directions: it
	// cannot add, and not even an empty snapshot clears a tracked task
	// (#1088).
	q.trackSystemFrame(map[string]interface{}{
		"type":    "system",
		"subtype": "background_tasks_changed",
		"tasks": []interface{}{
			map[string]interface{}{"task_id": "task-3", "task_type": "local_agent"},
		},
	})
	if len(q.inflightTasks) != 0 {
		t.Error("background_tasks_changed must not add tasks")
	}
	q.trackSystemFrame(runEndTaskStartedFrame("task-4"))
	q.trackSystemFrame(map[string]interface{}{
		"type":    "system",
		"subtype": "background_tasks_changed",
		"tasks":   []interface{}{},
	})
	if len(q.inflightTasks) == 0 {
		t.Error("background_tasks_changed must not clear tracked tasks")
	}
}

// ---------------------------------------------------------------------------
// StreamInput verbatim composition (follow-up owed from Tasks 3+4): drive the
// REAL StreamInput drain so the verbatim stamping composition is covered
// end-to-end, mirroring internal/transport's streamInput contract.
// ---------------------------------------------------------------------------

func TestStreamInputStampsPromptsThroughRealDrain(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			tr := newMockTransport()
			q := NewQuery(QueryConfig{
				Transport:       tr,
				IsStreamingMode: true,
				VerbatimPrompts: enabled,
			})
			promptCh := make(chan map[string]interface{})
			go q.StreamInput(promptCh)

			first := runEndUserPrompt("one")
			second := map[string]interface{}{
				"type":            "user",
				"client_composed": false,
				"message":         map[string]interface{}{"role": "user", "content": "two"},
				"uuid":            "uuid-user-2",
			}
			promptCh <- first
			promptCh <- second

			deadline := time.Now().Add(2 * time.Second)
			var written []string
			for time.Now().Before(deadline) {
				written = tr.getWritten()
				var userCount int
				for _, w := range written {
					var frame map[string]interface{}
					if json.Unmarshal([]byte(w), &frame) == nil && frame["type"] == "user" {
						userCount++
					}
				}
				if userCount == 2 {
					break
				}
				time.Sleep(2 * time.Millisecond)
			}

			var frames []map[string]interface{}
			for _, w := range written {
				var frame map[string]interface{}
				if json.Unmarshal([]byte(w), &frame) == nil && frame["type"] == "user" {
					frames = append(frames, frame)
				}
			}
			if len(frames) != 2 {
				t.Fatalf("expected 2 user writes through the real drain, got %d (%v)", len(frames), written)
			}
			if enabled {
				for i, frame := range frames {
					if v, ok := frame["client_composed"].(bool); !ok || !v {
						t.Errorf("frame %d: expected client_composed=true, got %v", i, frame["client_composed"])
					}
				}
			} else {
				if _, marked := frames[0]["client_composed"]; marked {
					t.Errorf("frame 0: expected no client_composed, got %v", frames[0]["client_composed"])
				}
				// The caller's explicit value rides through unchanged.
				if v, _ := frames[1]["client_composed"].(bool); v {
					t.Errorf("frame 1: expected caller's client_composed=false preserved, got %v", frames[1]["client_composed"])
				}
			}
			// The caller's maps are never mutated.
			if _, marked := first["client_composed"]; marked {
				t.Errorf("caller's first map must not be mutated, got client_composed=%v", first["client_composed"])
			}
			if v, ok := second["client_composed"].(bool); !ok || v {
				t.Errorf("caller's second map must keep client_composed=false, got %v", second["client_composed"])
			}

			// Closing the channel ends the stream: with no bidirectional
			// needs stdin closes right away (Python behavior), via
			// WaitForResultAndEndInput.
			close(promptCh)
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && tr.endInputCalls() == 0 {
				time.Sleep(2 * time.Millisecond)
			}
			if tr.endInputCalls() != 1 {
				t.Fatalf("expected stdin to close once after the stream ended, got %d", tr.endInputCalls())
			}
			_ = q.Close()
		})
	}
}

// TestStreamInputEmptyChannelClosesStdinImmediately mirrors the TypeScript
// SDK's messageCount guard: nothing was sent, so no result will arrive to
// release the hold; close immediately.
func TestStreamInputEmptyChannelClosesStdinImmediately(t *testing.T) {
	tr := newMockTransport()
	hook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		return HookOutput{}, nil
	}
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: true,
		Hooks:           map[string][]HookMatcherInternal{"PreToolUse": {{Matcher: ".*", Hooks: []HookCallback{hook}}}},
	})
	defer func() { _ = q.Close() }()

	done := make(chan struct{})
	empty := make(chan map[string]interface{})
	close(empty)
	go func() {
		q.StreamInput(empty)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamInput did not finish on an empty (already closed) channel")
	}
	if tr.endInputCalls() != 1 {
		t.Errorf("expected EndInput once for an empty stream, got %d", tr.endInputCalls())
	}
}

// TestStreamInputStopsWritingOnceClosed mirrors Python's `if self._closed:
// break` per-prompt check.
func TestStreamInputStopsWritingOnceClosed(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{Transport: tr, IsStreamingMode: true})
	promptCh := make(chan map[string]interface{}, 2)
	promptCh <- runEndUserPrompt("one")
	go q.StreamInput(promptCh)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(tr.getWritten()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if len(tr.getWritten()) == 0 {
		t.Fatal("first prompt was never written")
	}

	// Close the query; the queued prompt must not be written afterwards.
	_ = q.Close()
	promptCh <- runEndUserPrompt("two")
	time.Sleep(100 * time.Millisecond)
	for _, w := range tr.getWritten() {
		if strings.Contains(w, "two") {
			t.Error("prompt written after the query closed")
		}
	}
}

// TestRunEndedChannelSwap verifies the exported RunEnded snapshot semantics:
// waiters hold the channel they snapshot, and a reopened run hands out a
// fresh one.
func TestRunEndedChannelSwap(t *testing.T) {
	q := NewQuery(QueryConfig{Transport: newMockTransport()})
	defer func() { _ = q.Close() }()

	first := q.RunEnded()
	second := q.RunEnded()
	if first != second {
		t.Error("RunEnded must snapshot the same channel while the run is open")
	}

	q.mu.Lock()
	q.endRun()
	q.mu.Unlock()
	select {
	case <-first:
	default:
		t.Error("ended run must close the snapshot channel")
	}

	q.mu.Lock()
	q.reopenRun()
	q.mu.Unlock()
	fresh := q.RunEnded()
	if fresh == first {
		t.Error("reopenRun must swap in a fresh channel")
	}
	select {
	case <-fresh:
		t.Error("the fresh channel must be open")
	default:
	}
}
