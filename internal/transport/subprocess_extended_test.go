package transport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildCommand_Extended tests additional command building options.
func TestBuildCommand_Extended(t *testing.T) {
	tests := []struct {
		name     string
		opts     *TransportOptions
		expected []string
		missing  []string
	}{
		{
			name: "system prompt preset",
			opts: &TransportOptions{
				SystemPromptPreset: &SystemPromptPreset{
					Type:   "preset",
					Preset: "claude_code",
				},
			},
			// Should NOT have --system-prompt
			missing: []string{"--system-prompt"},
		},
		{
			name: "system prompt preset with append",
			opts: &TransportOptions{
				SystemPromptPreset: &SystemPromptPreset{
					Type:   "preset",
					Preset: "claude_code",
					Append: "Be concise.",
				},
			},
			expected: []string{"--append-system-prompt", "Be concise."},
			missing:  []string{"--system-prompt"},
		},
		{
			name: "fallback model",
			opts: &TransportOptions{
				Model:         "opus",
				FallbackModel: "sonnet",
			},
			expected: []string{"--model", "opus", "--fallback-model", "sonnet"},
		},
		{
			name: "max thinking tokens",
			opts: &TransportOptions{
				MaxThinkingTokens: 5000,
			},
			expected: []string{"--max-thinking-tokens", "5000"},
		},
		{
			name: "thinking config adaptive",
			opts: &TransportOptions{
				Thinking: &ThinkingConfig{
					Type: "adaptive",
				},
			},
			expected: []string{"--thinking", "adaptive"},
		},
		{
			name: "thinking config enabled",
			opts: &TransportOptions{
				Thinking: &ThinkingConfig{
					Type:         "enabled",
					BudgetTokens: 10000,
				},
			},
			expected: []string{"--max-thinking-tokens", "10000"},
		},
		{
			name: "thinking config disabled",
			opts: &TransportOptions{
				Thinking: &ThinkingConfig{
					Type: "disabled",
				},
			},
			expected: []string{"--thinking", "disabled"},
		},
		{
			name: "add dirs",
			opts: &TransportOptions{
				AddDirs: []string{"/path/to/dir1", "/path/to/dir2"},
			},
			expected: []string{"--add-dir", "/path/to/dir1", "--add-dir", "/path/to/dir2"},
		},
		{
			name: "session continuation",
			opts: &TransportOptions{
				ContinueConversation: true,
				Resume:               "session-123",
			},
			expected: []string{"--continue", "--resume", "session-123"},
		},
		{
			name: "settings file",
			opts: &TransportOptions{
				Settings: "/path/to/settings.json",
			},
			expected: []string{"--settings", "/path/to/settings.json"},
		},
		{
			name: "extra args",
			opts: &TransportOptions{
				ExtraArgs: map[string]string{
					"new-flag":       "value",
					"boolean-flag":   "",
					"another-option": "test-value",
				},
			},
			expected: []string{"--new-flag", "value", "--boolean-flag", "--another-option", "test-value"},
		},
		{
			name: "agents",
			opts: &TransportOptions{
				Agents: map[string]interface{}{
					"test-agent": map[string]interface{}{
						"description": "A test agent",
					},
				},
			},
			expected: []string{"--agents"}, // Just check flag presence, content is JSON
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, err := NewSubprocessTransport("test", tt.opts)
			if err != nil {
				t.Fatalf("NewSubprocessTransport failed: %v", err)
			}

			cmd := transport.buildCommand(context.Background())
			args := cmd.Args[1:] // Skip command
			argsStr := strings.Join(args, " ")

			// Check expected flags
			for i := 0; i < len(tt.expected); i++ {
				flag := tt.expected[i]
				if !strings.Contains(argsStr, flag) {
					t.Errorf("Expected flag/value %q not found in args: %s", flag, argsStr)
				}
			}

			// Check missing flags
			for _, flag := range tt.missing {
				if strings.Contains(argsStr, flag) {
					t.Errorf("Expected flag %q to be MISSING, but found in args: %s", flag, argsStr)
				}
			}
		})
	}
}

// TestBuildCommand_Sandbox tests sandbox options.
func TestBuildCommand_Sandbox(t *testing.T) {
	sandbox := &SandboxSettings{
		Enabled:                  true,
		AutoAllowBashIfSandboxed: true,
		Network: &SandboxNetworkConfig{
			AllowLocalBinding: true,
			AllowUnixSockets:  []string{"/var/run/docker.sock"},
		},
	}

	opts := &TransportOptions{
		Sandbox: sandbox,
	}

	transport, err := NewSubprocessTransport("test", opts)
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())
	args := cmd.Args[1:]
	argsStr := strings.Join(args, " ")

	if !strings.Contains(argsStr, "--sandbox") {
		t.Error("Expected --sandbox flag")
	}

	// Verify JSON content
	found := false
	for i, arg := range args {
		if arg == "--sandbox" && i+1 < len(args) {
			found = true
			jsonStr := args[i+1]
			var parsed SandboxSettings
			if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
				t.Fatalf("Failed to parse sandbox JSON: %v", err)
			}

			if !parsed.Enabled {
				t.Error("Expected Enabled=true")
			}
			if !parsed.AutoAllowBashIfSandboxed {
				t.Error("Expected AutoAllowBashIfSandboxed=true")
			}
			if parsed.Network == nil || !parsed.Network.AllowLocalBinding {
				t.Error("Expected Network.AllowLocalBinding=true")
			}
		}
	}

	if !found {
		t.Error("Could not find --sandbox value")
	}
}

// TestConnect_EnvVars tests that environment variables are passed to the subprocess.
func TestConnect_EnvVars(t *testing.T) {
	// Use a dummy command that exists
	cliPath := "echo"

	opts := &TransportOptions{
		CLIPath: cliPath,
		Env: map[string]string{
			"MY_TEST_VAR": "test-value",
		},
	}

	transport, err := NewSubprocessTransport("test", opts)
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	// We can't easily start the process and inspect it without it finishing immediately or blocking.
	// However, we can inspect how Connect *sets up* the command if we could peek inside.
	// Since we can't peek inside easily without exporting fields or refactoring,
	// we will try to start it and see if we can check the Env on the process struct *after* Connect returns (if it succeeds).
	// But `Connect` runs `cmd.Start()`.

	// A better approach for unit testing without side effects (running processes) would be to
	// allow dependency injection of the command creator, but that requires refactoring.
	// For now, we will assume that if we can run a simple command, we can check the process state.

	// Actually, `SubprocessTransport` is in package `transport`. We are in package `transport` (same package test).
	// So we can access private fields like `process`.

	// Let's rely on the fact that we can access `transport.process`.
	// But `Connect` starts the process. If we use `cat` or `sleep`, it might stay running long enough?
	// Or we can use `sh -c 'env'`.

	// Let's try to run `env` and capture output to verify variables are passed.
	// But `SubprocessTransport` expects JSON output. `env` will output text and cause JSONDecodeError.
	// That's fine! We can check the error or just the Env slice on the command struct if it's accessible.

	ctx := context.Background()
	// Mock CLI path to something that exists.
	// We don't want to depend on "claude" being installed.
	// We'll use "true" or "echo", but "env" is better if we want to verify output,
	// BUT `SubprocessTransport` is designed to talk to Claude Code which speaks JSON.

	// Strategy: Use `Connect` which calls `buildCommand` and sets `t.process`.
	// Even if `cmd.Start()` fails or succeeds, `t.process` is set.
	// Wait, if `Start()` fails, `Connect` returns error.

	// If we use "echo", it starts and exits immediately.
	transport.cliPath = "echo" // Override

	err = transport.Connect(ctx)
	// It might error because stdin/stdout pipes might fail if process exits too fast, or it might succeed.
	// We verify `transport.process.Env`.

	if transport.process == nil {
		t.Fatal("Expected transport.process to be set even if Connect fails or finishes")
	}

	env := transport.process.Env
	found := false
	foundEntrypoint := false
	for _, e := range env {
		if strings.HasPrefix(e, "MY_TEST_VAR=") {
			if e == "MY_TEST_VAR=test-value" {
				found = true
			}
		}
		if strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") {
			if e == "CLAUDE_CODE_ENTRYPOINT=sdk-go" {
				foundEntrypoint = true
			}
		}
	}

	if !found {
		t.Error("MY_TEST_VAR not found in process environment")
	}
	if !foundEntrypoint {
		t.Error("CLAUDE_CODE_ENTRYPOINT=sdk-go not found in process environment")
	}
}
