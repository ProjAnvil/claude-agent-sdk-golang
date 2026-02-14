package claude

import (
	"context"
	"testing"
)

// TestNewClient tests client creation.
func TestNewClient(t *testing.T) {
	tests := []struct {
		name string
		opts *ClaudeAgentOptions
	}{
		{"nil options", nil},
		{"empty options", &ClaudeAgentOptions{}},
		{"with system prompt", &ClaudeAgentOptions{SystemPrompt: "You are helpful"}},
		{"with tools", &ClaudeAgentOptions{AllowedTools: []string{"Read", "Write"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.opts)
			if client == nil {
				t.Fatal("Expected non-nil client")
			}
			if client.options == nil {
				t.Error("Expected non-nil options")
			}
		})
	}
}

// TestClientNotConnected tests operations before Connect.
func TestClientNotConnected(t *testing.T) {
	client := NewClient(nil)

	ctx := context.Background()

	// Query should fail
	_, err := client.Query(ctx, "test")
	if err == nil {
		t.Error("Expected error when querying before Connect")
	}
	if !IsCLIConnectionError(err) {
		t.Errorf("Expected CLIConnectionError, got %T", err)
	}

	// ReceiveResponse should fail
	_, err = client.ReceiveResponse(ctx)
	if err == nil {
		t.Error("Expected error when receiving before Connect")
	}

	// Interrupt should fail
	err = client.Interrupt(ctx)
	if err == nil {
		t.Error("Expected error when interrupting before Connect")
	}

	// SetPermissionMode should fail
	err = client.SetPermissionMode(ctx, PermissionModeDefault)
	if err == nil {
		t.Error("Expected error when setting permission mode before Connect")
	}

	// SetModel should fail
	err = client.SetModel(ctx, "claude-3-5-sonnet-20241022")
	if err == nil {
		t.Error("Expected error when setting model before Connect")
	}
}

// TestClientClose tests closing the client.
func TestClientClose(t *testing.T) {
	client := NewClient(nil)

	err := client.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Multiple closes should not error
	err = client.Close()
	if err != nil {
		t.Errorf("Second Close failed: %v", err)
	}
}

// TestPermissionModeConstants tests permission mode constants.
func TestPermissionModeConstants(t *testing.T) {
	modes := []PermissionMode{
		PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModePlan,
		PermissionModeBypassPermissions,
	}

	for _, mode := range modes {
		if mode == "" {
			t.Error("Permission mode should not be empty")
		}
	}
}
