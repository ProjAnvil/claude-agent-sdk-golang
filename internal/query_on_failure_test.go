package internal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// runInitializeWithReply runs Query.Initialize against a stubbed control
// channel and returns the initialize request body and the returned error.
// The caller controls the initialize response so the fail-closed guards can
// be exercised (Python SDK #1460).
func runInitializeWithReply(t *testing.T, cfg QueryConfig, reply map[string]interface{}) (map[string]interface{}, error) {
	t.Helper()
	if reply == nil {
		reply = map[string]interface{}{"status": "initialized"}
	}
	mockTrans := newMockTransport()
	cfg.Transport = mockTrans
	cfg.IsStreamingMode = true
	q := NewQuery(cfg)
	q.Start()

	go func() {
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
			written := mockTrans.getWritten()
			if len(written) == 0 {
				continue
			}
			var req map[string]interface{}
			if err := json.Unmarshal([]byte(written[0]), &req); err != nil {
				continue
			}
			reqID, _ := req["request_id"].(string)
			if reqID == "" {
				continue
			}
			mockTrans.messages <- map[string]interface{}{
				"type": "control_response",
				"response": map[string]interface{}{
					"subtype":    "success",
					"request_id": reqID,
					"response":   reply,
				},
			}
			return
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := q.Initialize(ctx)

	written := mockTrans.getWritten()
	if len(written) == 0 {
		t.Fatal("Expected a written initialize request")
	}
	var outer map[string]interface{}
	if err := json.Unmarshal([]byte(written[0]), &outer); err != nil {
		t.Fatalf("Failed to parse initialize request: %v", err)
	}
	inner, ok := outer["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected a request wrapper, got %v", outer)
	}
	return inner, err
}

// hookMatcherConfig extracts the first matcher of an event from an
// initialize request body.
func hookMatcherConfig(t *testing.T, body map[string]interface{}, event string) map[string]interface{} {
	t.Helper()
	hooks, ok := body["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected hooks in initialize request, got %v", body)
	}
	matchers, ok := hooks[event].([]interface{})
	if !ok || len(matchers) == 0 {
		t.Fatalf("Expected matchers for %s, got %v", event, hooks[event])
	}
	matcher, ok := matchers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected a matcher object, got %T", matchers[0])
	}
	return matcher
}

func hooksWithOnFailure(value string) map[string][]HookMatcherInternal {
	matcher := HookMatcherInternal{
		Matcher: ".*",
		Hooks: []HookCallback{
			func(HookInput, string, HookContext) (HookOutput, error) { return HookOutput{}, nil },
		},
		OnFailure: value,
	}
	return map[string][]HookMatcherInternal{"PreToolUse": {matcher}}
}

// TestInitializeSendsHookOnFailure mirrors Python's
// test_initialize_sends_hook_on_failure.
func TestInitializeSendsHookOnFailure(t *testing.T) {
	for _, value := range []string{"block", "continue"} {
		t.Run(value, func(t *testing.T) {
			body, err := runInitializeWithReply(t,
				QueryConfig{Hooks: hooksWithOnFailure(value)},
				map[string]interface{}{"hook_on_failure_supported": true})
			if err != nil {
				t.Fatalf("Initialize failed: %v", err)
			}
			got := hookMatcherConfig(t, body, "PreToolUse")["onFailure"]
			if got != value {
				t.Errorf("Expected onFailure=%q, got %v", value, got)
			}
		})
	}
}

// TestInitializeOmitsHookOnFailureWhenUnset mirrors Python's
// test_initialize_omits_hook_on_failure_when_unset.
func TestInitializeOmitsHookOnFailureWhenUnset(t *testing.T) {
	body, err := runInitializeWithReply(t, QueryConfig{Hooks: hooksWithOnFailure("")}, nil)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if _, present := hookMatcherConfig(t, body, "PreToolUse")["onFailure"]; present {
		t.Error("Expected onFailure to be absent when unset")
	}
}

// TestInitializeRefusesBlockWithoutCLISupport mirrors Python's
// test_initialize_refuses_block_when_cli_lacks_on_failure and
// test_initialize_refuses_unknown_on_failure_when_cli_lacks_support. An
// unknown value must be treated as "block" because the CLI does so.
func TestInitializeRefusesBlockWithoutCLISupport(t *testing.T) {
	for _, value := range []string{"block", "Block"} {
		t.Run(value, func(t *testing.T) {
			_, err := runInitializeWithReply(t, QueryConfig{Hooks: hooksWithOnFailure(value)}, nil)
			if err == nil {
				t.Fatal("Expected Initialize to refuse a block guard on an unsupported CLI")
			}
			var connErr *transport.CLIConnectionError
			if !errors.As(err, &connErr) {
				t.Fatalf("Expected *transport.CLIConnectionError, got %T: %v", err, err)
			}
			if !strings.Contains(connErr.Message, "on_failure") {
				t.Errorf("Expected the error to mention on_failure, got %q", connErr.Message)
			}
		})
	}
}

// TestInitializeRefusesBlockWhenHooksNotApplied mirrors Python's
// test_initialize_refuses_block_when_hooks_were_not_applied.
func TestInitializeRefusesBlockWhenHooksNotApplied(t *testing.T) {
	_, err := runInitializeWithReply(t,
		QueryConfig{Hooks: hooksWithOnFailure("block")},
		map[string]interface{}{
			"hook_on_failure_supported": true,
			"hooks_applied":             false,
		})
	if err == nil {
		t.Fatal("Expected Initialize to refuse when hooks were not registered")
	}
	var connErr *transport.CLIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Expected *transport.CLIConnectionError, got %T: %v", err, err)
	}
	if !strings.Contains(connErr.Message, "did not register") {
		t.Errorf("Expected the error to explain unregistered hooks, got %q", connErr.Message)
	}
}

// TestInitializeAllowsContinueWithoutCLISupport mirrors Python's
// test_initialize_allows_continue_when_cli_lacks_on_failure.
func TestInitializeAllowsContinueWithoutCLISupport(t *testing.T) {
	body, err := runInitializeWithReply(t, QueryConfig{Hooks: hooksWithOnFailure("continue")}, nil)
	if err != nil {
		t.Fatalf("Initialize must allow continue on an older CLI, got: %v", err)
	}
	if got := hookMatcherConfig(t, body, "PreToolUse")["onFailure"]; got != "continue" {
		t.Errorf("Expected onFailure=continue, got %v", got)
	}
}
