package claude

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// permissionGatedState records what the permission-gated mock transport saw.
type permissionGatedState struct {
	mu            sync.Mutex
	writes        []string
	ended         bool
	callbackCalls []string
}

// makePermissionGatedTransport builds a mock transport that enforces the real
// CLI contract for can_use_tool (mirrors the Python
// _make_permission_gated_transport):
//
//   - The can_use_tool control_request is only emitted after the SDK has
//     written the user message.
//   - The assistant/result frames are only emitted after the SDK has written
//     the permission control_response.
//   - Any write after EndInput() fails, like a closed pipe would.
func makePermissionGatedTransport() (*MockTransport, *permissionGatedState) {
	state := &permissionGatedState{}
	mockT := newMockTransport()

	mockT.WriteFunc = func(data string) error {
		state.mu.Lock()
		if state.ended {
			state.mu.Unlock()
			return errors.New("stdin closed")
		}
		state.writes = append(state.writes, data)
		state.mu.Unlock()

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil
		}
		switch payload["type"] {
		case "user":
			// The CLI asks for permission only after receiving the prompt.
			mockT.readCh <- map[string]interface{}{
				"type":       "control_request",
				"request_id": "perm_1",
				"request": map[string]interface{}{
					"subtype":     "can_use_tool",
					"tool_name":   "Write",
					"input":       map[string]interface{}{"file_path": "/tmp/x", "content": "hi"},
					"tool_use_id": "toolu_1",
				},
			}
		case "control_response":
			// The CLI cannot make progress until the permission verdict
			// arrives; once it does, the turn completes.
			mockT.readCh <- map[string]interface{}{
				"type": "assistant",
				"message": map[string]interface{}{
					"role":  "assistant",
					"model": "claude-sonnet-4-5",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "done"},
					},
				},
			}
			mockT.readCh <- resultFrame("uuid-r1")
		}
		return nil
	}

	mockT.EndInputFunc = func() error {
		state.mu.Lock()
		alreadyEnded := state.ended
		state.ended = true
		state.mu.Unlock()
		if !alreadyEnded {
			// Like the real CLI: exit once stdin is closed.
			go func() {
				time.Sleep(10 * time.Millisecond)
				mockT.shutdown()
			}()
		}
		return nil
	}
	mockT.CloseFunc = func() error {
		_ = mockT.EndInputFunc()
		return nil
	}

	// Chain the initialize auto-responder after the gating WriteFunc.
	handleInitialization(mockT, nil)
	return mockT, state
}

// allowAllRecording returns a CanUseTool callback that allows everything and
// records the tool names it was asked about.
func allowAllRecording(state *permissionGatedState) CanUseToolFunc {
	return func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		state.mu.Lock()
		state.callbackCalls = append(state.callbackCalls, toolName)
		state.mu.Unlock()
		return &PermissionResultAllow{}, nil
	}
}

// runCanUseToolQuery runs Query with a can_use_tool callback against the
// permission-gated transport and returns the messages, state, and the
// transport options the factory was called with.
func runCanUseToolQuery(t *testing.T, prompt interface{}) ([]Message, []error, *permissionGatedState, *transport.TransportOptions) {
	t.Helper()
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockT, state := makePermissionGatedTransport()

	var capturedOpts *transport.TransportOptions
	makeTransport = func(p interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		// Channel prompts are streamed to stdin by the real transport's
		// streamInput; the mock must do the same for the user message to
		// reach the CLI (and trigger the permission request).
		if ch, ok := p.(chan map[string]interface{}); ok {
			go func() {
				for msg := range ch {
					data, err := json.Marshal(msg)
					if err != nil {
						continue
					}
					if err := mockT.Write(string(data) + "\n"); err != nil {
						return
					}
				}
			}()
		}
		return mockT, nil
	}

	ctx := context.Background()
	messages, errs := Query(ctx, prompt, &ClaudeAgentOptions{
		CanUseTool: allowAllRecording(state),
	})

	done := make(chan struct{})
	var msgs []Message
	var errors []error
	go func() {
		msgs, errors = collectQuery(messages, errs)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query() did not complete within 5 seconds")
	}
	return msgs, errors, state, capturedOpts
}

// TestCanUseToolStringPromptIsSupported mirrors the Python
// test_string_prompt_with_can_use_tool_is_supported (#1204): string prompts
// are streamed over stdin internally, so can_use_tool no longer needs a
// streaming prompt.
func TestCanUseToolStringPromptIsSupported(t *testing.T) {
	msgs, errors, state, capturedOpts := runCanUseToolQuery(t, "write it")

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.callbackCalls) != 1 || state.callbackCalls[0] != "Write" {
		t.Errorf("Expected callback called once with Write, got %v", state.callbackCalls)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages (assistant + result), got %d", len(msgs))
	}
	if _, ok := msgs[0].(*AssistantMessage); !ok {
		t.Errorf("First message should be AssistantMessage, got %T", msgs[0])
	}
	if _, ok := msgs[1].(*ResultMessage); !ok {
		t.Errorf("Second message should be ResultMessage, got %T", msgs[1])
	}
	if !state.ended {
		t.Error("Expected stdin to be closed after the result")
	}
	// The verdict must have gone back over stdin as a control_response.
	var controlResponses []map[string]interface{}
	for _, w := range state.writes {
		if strings.Contains(w, `"control_response"`) {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(w), &payload); err == nil {
				controlResponses = append(controlResponses, payload)
			}
		}
	}
	if len(controlResponses) != 1 {
		t.Fatalf("Expected 1 control_response write, got %d", len(controlResponses))
	}
	resp, _ := controlResponses[0]["response"].(map[string]interface{})
	if resp["subtype"] != "success" {
		t.Errorf("Expected success control_response, got %v", resp["subtype"])
	}
	inner, _ := resp["response"].(map[string]interface{})
	if inner["behavior"] != "allow" {
		t.Errorf("Expected behavior=allow, got %v", inner["behavior"])
	}
	// can_use_tool routes permission prompts over the control protocol.
	if capturedOpts == nil || capturedOpts.PermissionPromptToolName != "stdio" {
		t.Errorf("Expected PermissionPromptToolName=stdio, got %v", capturedOpts)
	}
}

// TestCanUseToolChannelPromptWaitsForResult mirrors the Python
// test_async_iterable_prompt_with_can_use_tool_waits_for_result (#1204): a
// can_use_tool callback alone must hold stdin open until the run-ending
// result, exactly like hooks and SDK MCP servers do.
func TestCanUseToolChannelPromptWaitsForResult(t *testing.T) {
	promptCh := make(chan map[string]interface{}, 1)
	promptCh <- map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"role": "user", "content": "write it"},
	}
	close(promptCh)

	msgs, errors, state, _ := runCanUseToolQuery(t, promptCh)

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.callbackCalls) != 1 || state.callbackCalls[0] != "Write" {
		t.Errorf("Expected callback called once with Write, got %v", state.callbackCalls)
	}
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages (assistant + result), got %d", len(msgs))
	}
	if !state.ended {
		t.Error("Expected stdin to be closed after the result")
	}
}

// TestCanUseToolMutuallyExclusiveWithPermissionPromptTool mirrors the Python
// validation: can_use_tool cannot be combined with
// permission_prompt_tool_name.
func TestCanUseToolMutuallyExclusiveWithPermissionPromptTool(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	factoryCalled := false
	makeTransport = func(p interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		factoryCalled = true
		return newMockTransport(), nil
	}

	noop := func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{}, nil
	}

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", &ClaudeAgentOptions{
		CanUseTool:               noop,
		PermissionPromptToolName: "CustomTool",
	})
	_, errors := collectQuery(messages, errs)

	if len(errors) != 1 {
		t.Fatalf("Expected 1 error, got %d: %v", len(errors), errors)
	}
	if !strings.Contains(errors[0].Error(), "can_use_tool callback cannot be used with permission_prompt_tool_name") {
		t.Errorf("Expected mutual-exclusion error, got: %v", errors[0])
	}
	if factoryCalled {
		t.Error("Transport must not be created when option validation fails")
	}
}

// TestForwardSubagentTextOptionReachesInitialize mirrors the Python
// test_forward_subagent_text_option_reaches_initialize (#1206):
// ClaudeAgentOptions.ForwardSubagentText is plumbed through Query() into the
// initialize request as the forwardSubagentText capability.
func TestForwardSubagentTextOptionReachesInitialize(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			originalMakeTransport := makeTransport
			defer func() { makeTransport = originalMakeTransport }()

			mockTrans := newMockTransport()
			var writes []string
			handleInitialization(mockTrans, &writes)

			makeTransport = func(p interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
				return mockTrans, nil
			}

			go func() {
				time.Sleep(10 * time.Millisecond)
				mockTrans.readCh <- resultFrame("uuid-r1")
				mockTrans.Close()
			}()

			ctx := context.Background()
			messages, errs := Query(ctx, "Hello", &ClaudeAgentOptions{ForwardSubagentText: enabled})
			collectQuery(messages, errs)

			if len(writes) == 0 {
				t.Fatal("Expected the initialize control request to be written")
			}
			var req map[string]interface{}
			if err := json.Unmarshal([]byte(writes[0]), &req); err != nil {
				t.Fatalf("Failed to parse written message: %v", err)
			}
			inner, _ := req["request"].(map[string]interface{})
			if inner["subtype"] != "initialize" {
				t.Fatalf("Expected subtype=initialize, got %v", inner["subtype"])
			}
			val, present := inner["forwardSubagentText"]
			if enabled {
				if !present || val != true {
					t.Errorf("Expected forwardSubagentText=true, got %v (present=%v)", val, present)
				}
			} else if present {
				t.Errorf("Expected forwardSubagentText to be absent, got %v", val)
			}
		})
	}
}

// TestConvertToTransportOptionsTruncatingResume verifies ResumeSessionAt /
// ResumeDropsTurn pass through to the transport options (#1198).
func TestConvertToTransportOptionsTruncatingResume(t *testing.T) {
	drops := "ce0a8011-2c8d-40f2-86e5-d6e1b0c041c0"
	opts := &ClaudeAgentOptions{
		Resume:          "abc123",
		ForkSession:     true,
		ResumeSessionAt: "0d78eb23-2d48-4741-b970-4ed0a3356cce",
		ResumeDropsTurn: &drops,
	}
	transportOpts := convertToTransportOptions(opts)
	if transportOpts.ResumeSessionAt != opts.ResumeSessionAt {
		t.Errorf("Expected ResumeSessionAt=%q, got %q", opts.ResumeSessionAt, transportOpts.ResumeSessionAt)
	}
	if transportOpts.ResumeDropsTurn == nil || *transportOpts.ResumeDropsTurn != drops {
		t.Errorf("Expected ResumeDropsTurn=%q, got %v", drops, transportOpts.ResumeDropsTurn)
	}

	// An explicitly empty ResumeDropsTurn pointer is preserved (forwarded to
	// the CLI) rather than dropped.
	empty := ""
	transportOpts = convertToTransportOptions(&ClaudeAgentOptions{ResumeDropsTurn: &empty})
	if transportOpts.ResumeDropsTurn == nil || *transportOpts.ResumeDropsTurn != "" {
		t.Errorf("Expected empty ResumeDropsTurn pointer preserved, got %v", transportOpts.ResumeDropsTurn)
	}

	// Nil stays nil.
	transportOpts = convertToTransportOptions(&ClaudeAgentOptions{})
	if transportOpts.ResumeDropsTurn != nil {
		t.Errorf("Expected nil ResumeDropsTurn, got %v", transportOpts.ResumeDropsTurn)
	}
}
