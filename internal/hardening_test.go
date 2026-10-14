package internal

// Hardening wave tests: (1) inflight-task ledger cap, (2) hookCallbacks
// synchronization under q.mu, (3) CanUseTool nil/typed-nil containment,
// (4) unknown/duplicate control_response handling and Close releasing
// parked readers. Python citations refer to claude-agent-sdk-python v0.2.165
// (see .superpowers/sdd/HARDENING/research.md).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fix 1 — inflight-task ledger cap (maxTrackedTasks)
// ---------------------------------------------------------------------------

// captureHandler records every slog record so tests can assert on warnings
// ("logged ONCE" semantics for the overflow warning).
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return true
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(name string) slog.Handler       { return h }

// warnCount reports how many Warn-level records carry exactly message.
func (h *captureHandler) warnCount(message string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, r := range h.records {
		if r.Level == slog.LevelWarn && r.Message == message {
			count++
		}
	}
	return count
}

const overflowWarnMessage = "task ledger overflow; hostile CLI or >10000 concurrent tasks — run-end may end early"

// TestTaskLedgerCapTracksUpToCapThenOverflows unit-tests the cap: entries
// are tracked up to maxTrackedTasks, the first distinct task past it is not
// tracked, flips taskLedgerOverflowed, and reports the transition in the
// return value — the overflow warning itself now lives at readMessages'
// call site (logged outside q.mu), so this direct-call unit test must NOT
// produce one.
func TestTaskLedgerCapTracksUpToCapThenOverflows(t *testing.T) {
	logs := &captureHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	q := NewQuery(QueryConfig{Transport: newMockTransport()})

	q.mu.Lock()
	for i := 0; i < maxTrackedTasks; i++ {
		if q.trackSystemFrame(runEndTaskStartedFrame(fmt.Sprintf("task-%d", i))) {
			q.mu.Unlock()
			t.Fatalf("task-%d under the cap must not report overflow", i)
		}
	}
	if len(q.inflightTasks) != maxTrackedTasks {
		q.mu.Unlock()
		t.Fatalf("expected %d tracked tasks at the cap, got %d", maxTrackedTasks, len(q.inflightTasks))
	}
	if q.taskLedgerOverflowed {
		q.mu.Unlock()
		t.Fatal("overflow flag must not be set while entries fit under the cap")
	}

	// Re-announcing an already-tracked task at the cap is a no-op, not
	// overflow: the entry count would not grow.
	if q.trackSystemFrame(runEndTaskStartedFrame("task-0")) {
		q.mu.Unlock()
		t.Fatal("re-announcing a tracked task must not report the overflow")
	}
	if q.taskLedgerOverflowed {
		q.mu.Unlock()
		t.Fatal("re-announcing a tracked task must not trip the overflow")
	}

	// The 10001st distinct task is refused and flags the overflow.
	newlyOverflowed := q.trackSystemFrame(runEndTaskStartedFrame("task-over"))
	overflowed := q.taskLedgerOverflowed
	tracked, _ := q.inflightTasks["task-over"]
	q.mu.Unlock()

	if !newlyOverflowed {
		t.Error("the first task past the cap must report the overflow transition")
	}
	if !overflowed {
		t.Error("expected taskLedgerOverflowed after refusing a task past the cap")
	}
	if tracked {
		t.Error("a task past the cap must not be added to the ledger")
	}

	// A second overflow does not warn again (flag already set) and is not
	// re-reported.
	q.mu.Lock()
	newlyOverflowed2 := q.trackSystemFrame(runEndTaskStartedFrame("task-over-2"))
	over2, _ := q.inflightTasks["task-over-2"]
	q.mu.Unlock()
	if newlyOverflowed2 {
		t.Error("the second task past the cap must not re-report the overflow")
	}
	if over2 {
		t.Error("a second task past the cap must not be added either")
	}

	if got := logs.warnCount(overflowWarnMessage); got != 0 {
		t.Errorf("trackSystemFrame must not log the overflow itself (readMessages logs it outside q.mu); got %d warnings", got)
	}
}

// TestMaybeEndRunStandDownAndOverflow covers both ledger states: below the
// cap a non-empty ledger keeps the run open; once overflowed it is an
// under-count and the run must end anyway.
func TestMaybeEndRunStandDownAndOverflow(t *testing.T) {
	q := NewQuery(QueryConfig{Transport: newMockTransport()})

	q.mu.Lock()
	q.inflightTasks["task-1"] = true
	q.maybeEndRun()
	stoodDown := !q.runEnded

	q.taskLedgerOverflowed = true
	q.maybeEndRun()
	endedAnyway := q.runEnded
	q.mu.Unlock()

	if !stoodDown {
		t.Error("below the cap a tracked task in flight must keep the run open")
	}
	if !endedAnyway {
		t.Error("an overflowed ledger must not withhold the run end")
	}
}

// TestEndRunAtCeilingOverflowEndsRun mirrors TestCeilingLeavesTrackedAgentAlone
// with an overflowed ledger: the ceiling no longer stands down for a ledger it
// cannot trust.
func TestEndRunAtCeilingOverflowEndsRun(t *testing.T) {
	q := NewQuery(QueryConfig{Transport: newMockTransport()})

	q.mu.Lock()
	q.inflightTasks["task-1"] = true
	q.taskLedgerOverflowed = true
	generation := q.ceilingGeneration
	q.mu.Unlock()

	q.endRunAtCeiling(generation)

	q.mu.Lock()
	ended := q.runEnded
	q.mu.Unlock()
	if !ended {
		t.Error("the ceiling must end the run despite a non-empty overflowed ledger")
	}
}

// TestOverflowedLedgerRunEndsAtResult drives the real read loop: a flooded
// (overflowed) ledger does not hold the run open at the result frame. The
// flood flows through readMessages (not direct trackSystemFrame calls)
// because the overflow warning now fires at the read loop's call site,
// outside q.mu — this is also what pins its exactly-once semantics.
func TestOverflowedLedgerRunEndsAtResult(t *testing.T) {
	logs := &captureHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	h := newRunEndQuery(t, 0)
	h.writePrompt("Hello")

	// Flood the ledger past its cap through the read loop. Each send blocks
	// until readMessages has processed the frame, so the overflow transition
	// (and its single Warn) has happened by the time the loop ends.
	for i := 0; i < maxTrackedTasks+5; i++ {
		h.send(runEndTaskStartedFrame(fmt.Sprintf("flood-%d", i)))
	}
	h.q.mu.Lock()
	overflowed := h.q.taskLedgerOverflowed
	h.q.mu.Unlock()
	if !overflowed {
		t.Fatal("expected the ledger to overflow from the flood")
	}

	// The run ends at the result despite the non-empty (unreliable) ledger.
	h.send(runEndResultFrame("uuid-r1"))
	h.waitEndInput(2 * time.Second)

	if got := logs.warnCount(overflowWarnMessage); got != 1 {
		t.Errorf("expected exactly 1 overflow warning, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Fix 2 — hookCallbacks synchronization (q.mu) + unknown-ID error response
// ---------------------------------------------------------------------------

// TestUnsolicitedHookCallbackRacingInitialize races an unsolicited
// hook_callback control_request (a buggy or hostile CLI firing before the
// initialize handshake completes) against Initialize's registration loop.
// q.mu serializes map access (Python relies on asyncio's single thread);
// the unknown ID yields an error control_response — never a panic, never
// silence (query.py:643-646, 694-704).
func TestUnsolicitedHookCallbackRacingInitialize(t *testing.T) {
	const iterations = 25
	for i := 0; i < iterations; i++ {
		tr := newMockTransport()
		hookRan := make(chan struct{}, 1)
		hook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
			select {
			case hookRan <- struct{}{}:
			default:
			}
			return HookOutput{Decision: "allow"}, nil
		}
		q := NewQuery(QueryConfig{
			Transport:       tr,
			IsStreamingMode: true,
			Hooks: map[string][]HookMatcherInternal{
				"PreToolUse": {{Matcher: ".*", Hooks: []HookCallback{hook}}},
			},
		})
		q.Start()

		// Unsolicited hook_callback for an ID Initialize never registers.
		requestID := fmt.Sprintf("req_unsolicited_%d", i)
		tr.messages <- map[string]interface{}{
			"type":       "control_request",
			"request_id": requestID,
			"request": map[string]interface{}{
				"subtype":     "hook_callback",
				"callback_id": "hook_9999",
				"tool_use_id": "tool_1",
				"input":       map[string]interface{}{"hook_event_name": "PreToolUse"},
			},
		}

		// Reply to the initialize request wherever it lands in the write
		// log — the unsolicited request's error response may precede it
		// (sendInitializeResponse only inspects the first write).
		go func() {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				for _, w := range tr.getWritten() {
					var msg map[string]interface{}
					if json.Unmarshal([]byte(w), &msg) != nil {
						continue
					}
					if msg["type"] != "control_request" {
						continue
					}
					req, _ := msg["request"].(map[string]interface{})
					if req == nil || req["subtype"] != "initialize" {
						continue
					}
					reqID, _ := msg["request_id"].(string)
					if reqID == "" {
						continue
					}
					tr.messages <- map[string]interface{}{
						"type": "control_response",
						"response": map[string]interface{}{
							"subtype":    "success",
							"request_id": reqID,
							"response":   map[string]interface{}{"status": "initialized"},
						},
					}
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()

		// Initialize concurrently with the read loop dispatching the
		// unsolicited request.
		if _, err := q.Initialize(context.Background()); err != nil {
			t.Fatalf("iteration %d: Initialize failed: %v", i, err)
		}

		// The CLI received an error control_response for the unknown ID.
		deadline := time.Now().Add(2 * time.Second)
		found := false
		for !found && time.Now().Before(deadline) {
			for _, w := range tr.getWritten() {
				var msg map[string]interface{}
				if err := json.Unmarshal([]byte(w), &msg); err != nil {
					continue
				}
				if msg["type"] != "control_response" {
					continue
				}
				resp, _ := msg["response"].(map[string]interface{})
				if resp == nil || resp["request_id"] != requestID {
					continue
				}
				if resp["subtype"] != "error" {
					t.Fatalf("iteration %d: expected error control_response, got %v", i, resp)
				}
				if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "no hook callback found for ID: hook_9999") {
					t.Fatalf("iteration %d: unexpected error text: %q", i, errMsg)
				}
				found = true
			}
			time.Sleep(2 * time.Millisecond)
		}
		if !found {
			t.Fatalf("iteration %d: no error control_response for the unsolicited hook_callback", i)
		}

		// Registration still works: the legitimately-registered callback
		// (hook_0) answers a later request with a success response.
		tr.messages <- map[string]interface{}{
			"type":       "control_request",
			"request_id": fmt.Sprintf("req_legit_%d", i),
			"request": map[string]interface{}{
				"subtype":     "hook_callback",
				"callback_id": "hook_0",
				"tool_use_id": "tool_2",
				"input":       map[string]interface{}{"hook_event_name": "PreToolUse"},
			},
		}
		select {
		case <-hookRan:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: registered hook callback never ran", i)
		}

		_ = q.Close()
	}
}

// ---------------------------------------------------------------------------
// Fix 3 — CanUseTool nil / typed-nil / unsupported result containment
// ---------------------------------------------------------------------------

// fakeInternalPermission is an internal.PermissionResult implementation the
// handler does not know: under the old IsAllow-then-assert code it panicked
// on the type assertion.
type fakeInternalPermission struct{}

func (fakeInternalPermission) IsAllow() bool { return true }

// TestHandleCanUseToolRejectsNilAndUnsupportedResults covers every failure
// shape at the handler level: each becomes the explicit error Python raises
// as TypeError (query.py:635-638), never a panic.
func TestHandleCanUseToolRejectsNilAndUnsupportedResults(t *testing.T) {
	q := NewQuery(QueryConfig{
		Transport:       newMockTransport(),
		IsStreamingMode: true,
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			switch toolName {
			case "nil-result":
				return nil, nil
			case "typed-nil-allow":
				return (*PermissionResultAllow)(nil), nil
			case "typed-nil-deny":
				return (*PermissionResultDeny)(nil), nil
			default:
				return fakeInternalPermission{}, nil
			}
		},
	})

	request := map[string]interface{}{"tool_name": "", "input": map[string]interface{}{}}
	for _, toolName := range []string{"nil-result", "typed-nil-allow", "typed-nil-deny", "unsupported"} {
		request["tool_name"] = toolName
		response, err := q.handleCanUseTool(context.Background(), request)
		if err == nil || response != nil {
			t.Errorf("%s: expected an error and no response, got (%v, %v)", toolName, response, err)
			continue
		}
		if err.Error() != "CanUseTool returned a nil or unsupported permission result" {
			t.Errorf("%s: unexpected error text %q", toolName, err.Error())
		}
	}
}

// TestCanUseToolNilResultSendsErrorControlResponseAndContinues drives the
// full path through the read loop: the spawned handler goroutine has no
// recover, so before the fix a (nil, nil) callback killed the process. Now
// the CLI receives an error control_response (Python parity — no deny is
// synthesized) and the query keeps processing frames.
func TestCanUseToolNilResultSendsErrorControlResponseAndContinues(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{
		Transport:       tr,
		IsStreamingMode: true,
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			return nil, nil
		},
	})
	q.Start()
	t.Cleanup(func() { _ = q.Close() })

	tr.messages <- map[string]interface{}{
		"type":       "control_request",
		"request_id": "req_nil_perm",
		"request": map[string]interface{}{
			"subtype":   "can_use_tool",
			"tool_name": "Bash",
			"input":     map[string]interface{}{"command": "ls"},
		},
	}

	// Error control_response reaches the CLI.
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for !found && time.Now().Before(deadline) {
		for _, w := range tr.getWritten() {
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(w), &msg); err != nil {
				continue
			}
			if msg["type"] != "control_response" {
				continue
			}
			resp, _ := msg["response"].(map[string]interface{})
			if resp == nil || resp["request_id"] != "req_nil_perm" {
				continue
			}
			if resp["subtype"] != "error" {
				t.Fatalf("expected error control_response, got %v", resp)
			}
			if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "CanUseTool returned a nil or unsupported permission result") {
				t.Fatalf("unexpected error text: %q", errMsg)
			}
			found = true
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !found {
		t.Fatal("no error control_response for the nil CanUseTool result")
	}

	// The query is still alive: a following regular frame is forwarded.
	tr.messages <- map[string]interface{}{
		"type": "assistant",
		"uuid": "uuid-after-nil-perm",
		"message": map[string]interface{}{
			"role": "assistant",
		},
	}
	select {
	case got, ok := <-q.RawMessages():
		if !ok {
			t.Fatal("rawMessages closed; query did not survive the nil result")
		}
		if got["uuid"] != "uuid-after-nil-perm" {
			t.Fatalf("expected the frame after the nil result, got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("query stopped processing frames after the nil CanUseTool result")
	}
}

// ---------------------------------------------------------------------------
// Fix 4 — unknown/duplicate control_response + Close releasing parked readers
// ---------------------------------------------------------------------------

func controlResponseFrame(requestID, subtype string) map[string]interface{} {
	return map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    subtype,
			"request_id": requestID,
			"response":   map[string]interface{}{"status": "ok"},
		},
	}
}

// TestUnknownAndDuplicateControlResponsesDoNotStallReadLoop mirrors Python's
// membership guard (query.py:401): an unknown response is silently skipped,
// a resolved ID is removed on first delivery so its duplicate is skipped
// too, and the read loop keeps processing later frames — before the fix the
// duplicate parked it forever on the full 1-buffered channel.
func TestUnknownAndDuplicateControlResponsesDoNotStallReadLoop(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{Transport: tr, IsStreamingMode: true})
	q.Start()
	t.Cleanup(func() { _ = q.Close() })

	// Unknown request_id: dropped on the floor, no crash.
	tr.messages <- controlResponseFrame("no-such-request", "success")

	// A registered pending response is resolved exactly once; the duplicate
	// must not stall the loop (old behavior: blocking send on a full
	// 1-buffered channel).
	requestID := "req_known"
	responseCh := make(chan controlResult, 1)
	q.mu.Lock()
	q.pendingResponses[requestID] = responseCh
	q.mu.Unlock()

	tr.messages <- controlResponseFrame(requestID, "success")
	tr.messages <- controlResponseFrame(requestID, "success") // duplicate

	select {
	case result := <-responseCh:
		if result.err != nil {
			t.Fatalf("first response should succeed, got %v", result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first control response was never delivered")
	}
	select {
	case result := <-responseCh:
		t.Errorf("duplicate delivered a second result: %v", result)
	default:
	}

	// The entry was removed on delivery: a late duplicate finds nothing.
	q.mu.Lock()
	_, stillPending := q.pendingResponses[requestID]
	q.mu.Unlock()
	if stillPending {
		t.Error("pending entry must be removed on first delivery")
	}

	// The read loop survived: a following regular frame is forwarded.
	tr.messages <- map[string]interface{}{
		"type":    "assistant",
		"uuid":    "uuid-after-duplicates",
		"message": map[string]interface{}{"role": "assistant"},
	}
	select {
	case got, ok := <-q.RawMessages():
		if !ok || got["uuid"] != "uuid-after-duplicates" {
			t.Fatalf("expected the frame after the duplicate responses, got %v (ok=%v)", got, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read loop stalled on the duplicate control_response")
	}
}

// allStacks returns a dump of all goroutine stacks.
func allStacks() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}

// stackBlocksByID parses a goroutine dump into {goroutine ID: stack block},
// keyed by ID so leak assertions can diff against a baseline snapshot taken
// before the action under test. A global substring scan would flake if any
// earlier test leaked a same-named goroutine; the diff scopes the assertion
// to goroutines that appeared during this test.
func stackBlocksByID(dump string) map[string]string {
	blocks := map[string]string{}
	for _, block := range strings.Split(dump, "\n\ngoroutine ") {
		block = strings.TrimPrefix(block, "goroutine ")
		id := block
		if i := strings.IndexAny(id, " \n"); i >= 0 {
			id = id[:i]
		}
		if id != "" {
			blocks[id] = block
		}
	}
	return blocks
}

// goroutineParkedInSend reports whether a goroutine that appeared after
// baseline is blocked at a channel send with wantFunc on its stack. Both
// park statuses are accepted: a plain blocking send parks as "[chan send]",
// a select-guarded send parks as "[select]".
func goroutineParkedInSend(baseline map[string]string, wantFunc string) bool {
	for id, block := range stackBlocksByID(allStacks()) {
		if _, old := baseline[id]; old {
			continue
		}
		if !strings.Contains(block, wantFunc) {
			continue
		}
		if strings.Contains(block, "[chan send") || strings.Contains(block, "[select") {
			return true
		}
	}
	return false
}

// noNewGoroutineInFunc reports whether no goroutine that appeared after
// baseline is running wantFunc — i.e. every goroutine the test spawned for
// it has exited (pre-existing same-named goroutines in the baseline do not
// count against it).
func noNewGoroutineInFunc(baseline map[string]string, wantFunc string) bool {
	for id, block := range stackBlocksByID(allStacks()) {
		if _, old := baseline[id]; old {
			continue
		}
		if strings.Contains(block, wantFunc) {
			return false
		}
	}
	return true
}

// TestCloseReleasesParkedRawMessagesSender verifies the goroutine-leak
// escape hatch: with no consumer draining rawMessages, the read loop parks
// on the full buffer; Close() must release it so the goroutine can exit.
// Python's close() cancels and reaps the reader (query.py:1194-1196).
//
// rawMessages itself is never drained — a receive there would free buffer
// space and release the parked sender even in the unfixed code, hiding the
// leak. The reader's park and exit are observed via the goroutine dump,
// scoped by a baseline snapshot taken before Start (goleak-style, no new
// dependency): confirming the [chan send] park BEFORE Close rules out the
// race where the reader exits via the closed-flag check without ever
// parking, and the baseline diff keeps an earlier test's leaked goroutine
// from flaking the scan.
func TestCloseReleasesParkedRawMessagesSender(t *testing.T) {
	tr := newMockTransport()
	q := NewQuery(QueryConfig{Transport: tr, IsStreamingMode: true})
	baseline := stackBlocksByID(allStacks())
	q.Start()

	frame := func(i int) map[string]interface{} {
		return map[string]interface{}{
			"type":    "assistant",
			"uuid":    fmt.Sprintf("uuid-park-%d", i),
			"message": map[string]interface{}{"role": "assistant"},
		}
	}

	// Fill rawMessages' 100-slot buffer.
	for i := 0; i < 100; i++ {
		tr.messages <- frame(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(q.rawMessages) < 100 {
		if time.Now().After(deadline) {
			t.Fatal("rawMessages buffer never filled")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// One more frame: the reader takes it from the transport but parks on
	// the send — nobody drains rawMessages.
	tr.messages <- frame(100)

	// Confirm the reader is parked INSIDE the send before closing.
	parked := false
	parkDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(parkDeadline) {
		if goroutineParkedInSend(baseline, "(*Query).readMessages") {
			parked = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !parked {
		t.Fatal("reader never parked on the full rawMessages channel")
	}

	// Close releases the parked sender; the reader goroutine exits and its
	// frame disappears from the dump of goroutines new since the baseline.
	if err := q.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	exited := false
	for time.Now().Before(deadline) {
		if noNewGoroutineInFunc(baseline, "(*Query).readMessages") {
			exited = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !exited {
		t.Fatal("reader goroutine did not exit after Close; it is parked on a full rawMessages channel")
	}
}
