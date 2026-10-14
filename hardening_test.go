package claude

// Hardening wave tests for the public surface: the CanUseTool conversion
// sites (query.go's createInternalQueryConfig and client.go's Connect) must
// turn a nil / typed-nil / unsupported permission result into an explicit
// error instead of feeding a nil interface into the internal handler, whose
// goroutine has no recover (Python raises TypeError in the same spot and
// answers the CLI with an error control_response, query.py:635-638,
// 694-704).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
)

// TestCreateInternalQueryConfigCanUseToolNilAndTypedNil covers query.go's
// conversion: every failure shape maps to errCanUseToolNilResult.
func TestCreateInternalQueryConfigCanUseToolNilAndTypedNil(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			switch toolName {
			case "nil-result":
				return nil, nil
			case "typed-nil-allow":
				return (*PermissionResultAllow)(nil), nil
			case "typed-nil-deny":
				return (*PermissionResultDeny)(nil), nil
			default:
				return &fakePermissionResult{}, nil
			}
		},
	}

	cfg, err := createInternalQueryConfig(opts, nil)
	if err != nil {
		t.Fatalf("createInternalQueryConfig failed: %v", err)
	}
	for _, toolName := range []string{"nil-result", "typed-nil-allow", "typed-nil-deny", "unsupported"} {
		result, err := cfg.CanUseTool(toolName, nil, internal.ToolPermissionContext{})
		if err == nil || result != nil {
			t.Errorf("%s: expected an explicit error and nil result, got (%v, %v)", toolName, result, err)
			continue
		}
		if err != errCanUseToolNilResult {
			t.Errorf("%s: error = %v, want errCanUseToolNilResult", toolName, err)
		}
	}
}

// TestClientCanUseToolNilResultErrorControlResponse drives client.go's
// conversion through a connected client: the CLI receives an error
// control_response (no synthesized deny) and the client stays usable.
func TestClientCanUseToolNilResultErrorControlResponse(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			switch toolName {
			case "TypedNilAllow":
				return (*PermissionResultAllow)(nil), nil
			case "Alive":
				return &PermissionResultAllow{UpdatedInput: input}, nil
			default:
				return nil, nil
			}
		},
	}
	_, mockT, writes := connectedClient(t, opts, nil)

	feedControlRequest(mockT, "req_nil_perm", map[string]interface{}{
		"subtype":   "can_use_tool",
		"tool_name": "Bash",
		"input":     map[string]interface{}{"command": "ls"},
	})
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_nil_perm"`) &&
			strings.Contains(w, "CanUseTool returned a nil or unsupported permission result")
	})

	// The typed-nil shape fails the same way, through the same conversion.
	feedControlRequest(mockT, "req_typed_nil_perm", map[string]interface{}{
		"subtype":   "can_use_tool",
		"tool_name": "TypedNilAllow",
		"input":     map[string]interface{}{"command": "ls"},
	})
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_typed_nil_perm"`) &&
			strings.Contains(w, "CanUseTool returned a nil or unsupported permission result")
	})

	// Per-request assertion: every control_response the client sent for the
	// nil-result requests must be an error response — an allow or deny would
	// mean the nil result was answered as a permission decision, and how the
	// tool call proceeds must stay the CLI's decision (Python parity).
	// Scanning only these request ids (not all writes) keeps unrelated
	// traffic from tripping the check.
	for _, requestID := range []string{"req_nil_perm", "req_typed_nil_perm"} {
		responses := 0
		for _, w := range writes.snapshot() {
			var msg map[string]interface{}
			if json.Unmarshal([]byte(w), &msg) != nil {
				continue
			}
			if msg["type"] != "control_response" {
				continue
			}
			resp, _ := msg["response"].(map[string]interface{})
			if resp == nil || resp["request_id"] != requestID {
				continue
			}
			responses++
			if subtype, _ := resp["subtype"].(string); subtype != "error" {
				t.Errorf("%s: control_response subtype = %q, want \"error\" (never an allow/deny)", requestID, subtype)
			}
		}
		if responses == 0 {
			t.Errorf("%s: no control_response captured for the nil-result request", requestID)
		}
	}

	// The client survives: a later allow request still succeeds.
	feedControlRequest(mockT, "req_still_alive", map[string]interface{}{
		"subtype":   "can_use_tool",
		"tool_name": "Alive",
		"input":     map[string]interface{}{},
	})
	waitForWrite(t, writes, func(w string) bool {
		return strings.Contains(w, `"req_still_alive"`) && strings.Contains(w, `"behavior":"allow"`)
	})
}
