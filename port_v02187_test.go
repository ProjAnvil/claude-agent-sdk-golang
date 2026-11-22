package claude

import (
	"testing"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
)

// TestOnFailureReachesInternalQueryConfig proves the public
// ClaudeAgentOptions hook matcher field is wired through to the internal
// initialize handshake (Python SDK v0.2.177, #1460).
func TestOnFailureReachesInternalQueryConfig(t *testing.T) {
	opts := DefaultOptions()
	opts.Hooks = map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{Matcher: "Bash", OnFailure: "block"}},
	}
	cfg, err := createInternalQueryConfig(opts, nil)
	if err != nil {
		t.Fatalf("createInternalQueryConfig failed: %v", err)
	}
	matchers := cfg.Hooks[string(HookEventPreToolUse)]
	if len(matchers) != 1 || matchers[0].OnFailure != "block" {
		t.Fatalf("Expected OnFailure=block in the internal config, got %+v", matchers)
	}
}

// TestClientAppReachesTransportOptions proves the public option is wired to
// the CLI subprocess environment (Python SDK v0.2.181, #1432).
func TestClientAppReachesTransportOptions(t *testing.T) {
	label := "my-app/1.0.0"
	opts := DefaultOptions()
	opts.ClientApp = &label
	transportOpts := convertToTransportOptions(opts)
	if transportOpts.ClientApp == nil || *transportOpts.ClientApp != label {
		t.Fatalf("Expected ClientApp=%q, got %v", label, transportOpts.ClientApp)
	}
}

// TestUpdatedPermissionsReachInternalResult proves the public
// PermissionResultAllow.UpdatedPermissions field is no longer dropped when
// Query configures the CanUseTool callback (Python SDK #1502).
func TestUpdatedPermissionsReachInternalResult(t *testing.T) {
	opts := DefaultOptions()
	opts.CanUseTool = func(string, map[string]interface{}, ToolPermissionContext) (PermissionResult, error) {
		return &PermissionResultAllow{
			UpdatedPermissions: []PermissionUpdate{
				{
					Type:        "addRules",
					Behavior:    "allow",
					Rules:       []PermissionRuleValue{{ToolName: "WebSearch"}},
					Destination: "localSettings",
				},
			},
		}, nil
	}
	cfg, err := createInternalQueryConfig(opts, nil)
	if err != nil {
		t.Fatalf("createInternalQueryConfig failed: %v", err)
	}
	result, err := cfg.CanUseTool("WebSearch", map[string]interface{}{}, internal.ToolPermissionContext{})
	if err != nil {
		t.Fatalf("CanUseTool failed: %v", err)
	}
	allow, ok := result.(*internal.PermissionResultAllow)
	if !ok {
		t.Fatalf("Expected *internal.PermissionResultAllow, got %T", result)
	}
	updates := allow.UpdatedPermissions
	if len(updates) != 1 {
		t.Fatalf("Expected 1 forwarded update, got %d", len(updates))
	}
	if updates[0].Type != "addRules" || updates[0].Destination != "localSettings" ||
		len(updates[0].Rules) != 1 || updates[0].Rules[0].ToolName != "WebSearch" {
		t.Errorf("Forwarded update lost fields: %+v", updates[0])
	}
}
