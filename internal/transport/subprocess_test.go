package transport

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFindCLI tests CLI path discovery.
func TestFindCLI(t *testing.T) {
	// This test will pass if CLI is installed, otherwise skip
	path, err := findCLI()
	if err != nil {
		t.Skip("Claude Code CLI not installed, skipping test")
	}
	if path == "" {
		t.Error("Expected non-empty CLI path")
	}
}

// TestBuildCommand tests command building with various options.
func TestBuildCommand(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		opts     *TransportOptions
		expected []string
	}{
		{
			name:   "basic command",
			prompt: "Hello",
			opts:   &TransportOptions{},
			expected: []string{
				"--output-format", "stream-json",
				"--verbose",
				"--system-prompt", "",
				"--input-format", "stream-json",
			},
		},
		{
			name:   "with system prompt",
			prompt: "Test",
			opts: &TransportOptions{
				SystemPrompt: "You are helpful",
			},
			expected: []string{
				"--system-prompt", "You are helpful",
			},
		},
		{
			name:   "with tools",
			prompt: "Test",
			opts: &TransportOptions{
				Tools: []string{"Read", "Write"},
			},
			expected: []string{
				"--tools", "Read,Write",
			},
		},
		{
			name:   "with max turns",
			prompt: "Test",
			opts: &TransportOptions{
				MaxTurns: 5,
			},
			expected: []string{
				"--max-turns", "5",
			},
		},
		{
			name:   "with model",
			prompt: "Test",
			opts: &TransportOptions{
				Model: "claude-3-5-sonnet-20241022",
			},
			expected: []string{
				"--model", "claude-3-5-sonnet-20241022",
			},
		},
		{
			name:   "with permission mode",
			prompt: "Test",
			opts: &TransportOptions{
				PermissionMode: "acceptEdits",
			},
			expected: []string{
				"--permission-mode", "acceptEdits",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, err := NewSubprocessTransport(tt.prompt, tt.opts)
			if err != nil {
				t.Fatalf("NewSubprocessTransport failed: %v", err)
			}

			cmd := transport.buildCommand(context.Background())
			args := cmd.Args[1:] // Skip the command itself

			// Check that expected args are present
			argsStr := strings.Join(args, " ")
			for i := 0; i < len(tt.expected); i += 2 {
				flag := tt.expected[i]
				if !strings.Contains(argsStr, flag) {
					t.Errorf("Expected flag %q not found in args", flag)
				}
				if i+1 < len(tt.expected) {
					value := tt.expected[i+1]
					if value != "" && !strings.Contains(argsStr, value) {
						t.Errorf("Expected value %q for flag %q not found in args", value, flag)
					}
				}
			}
		})
	}
}

// TestBuildMCPConfig tests MCP configuration building.
func TestBuildMCPConfig(t *testing.T) {
	tests := []struct {
		name     string
		servers  map[string]interface{}
		expected string
	}{
		{
			name:     "empty servers",
			servers:  map[string]interface{}{},
			expected: "",
		},
		{
			name: "sdk server",
			servers: map[string]interface{}{
				"calc": map[string]interface{}{
					"type": "sdk",
					"name": "calculator",
				},
			},
			expected: `"calc"`,
		},
		{
			name: "stdio server",
			servers: map[string]interface{}{
				"fs": map[string]interface{}{
					"type":    "stdio",
					"command": "node",
					"args":    []string{"server.js"},
				},
			},
			expected: `"fs"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &SubprocessTransport{
				options: &TransportOptions{
					MCPServers: tt.servers,
				},
			}

			config := transport.buildMCPConfig()

			if tt.expected == "" {
				if config != "" {
					t.Errorf("Expected empty config, got %q", config)
				}
				return
			}

			if !strings.Contains(config, tt.expected) {
				t.Errorf("Expected config to contain %q, got %q", tt.expected, config)
			}

			// Verify it's valid JSON
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(config), &parsed); err != nil {
				t.Errorf("Config is not valid JSON: %v", err)
			}
		})
	}
}

// TestBufferOverflowError tests buffer overflow error handling.
func TestBufferOverflowError(t *testing.T) {
	err := &BufferOverflowError{
		BufferSize: 2000000,
		Limit:      1000000,
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "2000000") {
		t.Errorf("Error message should contain buffer size")
	}
	if !strings.Contains(errMsg, "1000000") {
		t.Errorf("Error message should contain limit")
	}
}

// TestCLINotFoundError tests CLI not found error.
func TestCLINotFoundError(t *testing.T) {
	err := &CLINotFoundError{
		CLIPath: "/usr/local/bin/claude",
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "Claude Code not found") {
		t.Errorf("Error message should mention Claude Code")
	}
	if !strings.Contains(errMsg, "/usr/local/bin/claude") {
		t.Errorf("Error message should contain CLI path")
	}
}

// TestProcessError tests process error formatting.
func TestProcessError(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		stderr   string
	}{
		{"with exit code", 1, ""},
		{"with stderr", 0, "error output"},
		{"with both", 127, "command not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ProcessError{
				Message:  "process failed",
				ExitCode: tt.exitCode,
				Stderr:   tt.stderr,
			}

			errMsg := err.Error()
			if errMsg == "" {
				t.Error("Expected non-empty error message")
			}

			if tt.exitCode != 0 && !strings.Contains(errMsg, "exit code") {
				t.Error("Error message should mention exit code")
			}

			if tt.stderr != "" && !strings.Contains(errMsg, tt.stderr) {
				t.Error("Error message should contain stderr")
			}
		})
	}
}

// TestJSONDecodeError tests JSON decode error formatting.
func TestJSONDecodeError(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"short line", `{"invalid": json`},
		{"long line", strings.Repeat("x", 150)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &JSONDecodeError{
				Line: tt.line,
			}

			errMsg := err.Error()
			if errMsg == "" {
				t.Error("Expected non-empty error message")
			}

			// Long lines should be truncated
			if len(tt.line) > 100 && len(errMsg) > 200 {
				t.Error("Long lines should be truncated in error message")
			}
		})
	}
}

// TestCLIConnectionError tests connection error.
func TestCLIConnectionError(t *testing.T) {
	cause := &ProcessError{Message: "process failed"}
	err := &CLIConnectionError{
		Message: "connection failed",
		Cause:   cause,
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "connection failed") {
		t.Error("Error message should contain message")
	}

	if err.Unwrap() != cause {
		t.Error("Unwrap should return cause")
	}
}

// TestDefaultTransportOptions tests default options.
func TestDefaultTransportOptions(t *testing.T) {
	opts := DefaultTransportOptions()

	if opts == nil {
		t.Fatal("Expected non-nil options")
	}

	// Verify defaults are reasonable
	if opts.MaxBufferSize < 0 {
		t.Error("MaxBufferSize should not be negative")
	}
}

// TestNewSubprocessTransport tests transport creation.
func TestNewSubprocessTransport(t *testing.T) {
	tests := []struct {
		name    string
		prompt  interface{}
		opts    *TransportOptions
		wantErr bool
	}{
		{
			name:    "string prompt",
			prompt:  "Hello",
			opts:    nil,
			wantErr: false,
		},
		{
			name:    "channel prompt",
			prompt:  make(chan map[string]interface{}),
			opts:    &TransportOptions{},
			wantErr: false,
		},
		{
			name:   "with custom CLI path",
			prompt: "Test",
			opts: &TransportOptions{
				CLIPath: "/custom/path/claude",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, err := NewSubprocessTransport(tt.prompt, tt.opts)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				// CLI not found is acceptable in test environment
				if _, ok := err.(*CLINotFoundError); !ok {
					t.Errorf("Unexpected error: %v", err)
				}
				return
			}

			if transport == nil {
				t.Error("Expected non-nil transport")
			}

			if transport.maxBufferSize <= 0 {
				t.Error("Expected positive max buffer size")
			}
		})
	}
}

// TestTransportWriteBeforeConnect tests that writing before connect fails.
func TestTransportWriteBeforeConnect(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	err = transport.Write("test data\n")
	if err == nil {
		t.Error("Expected error when writing before connect")
	}
}

// TestTransportIsReady tests IsReady method.
func TestTransportIsReady(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	if transport.IsReady() {
		t.Error("Transport should not be ready before Connect")
	}
}

// TestTransportClose tests Close method.
func TestTransportClose(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	err = transport.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	if transport.IsReady() {
		t.Error("Transport should not be ready after Close")
	}
}

// TestTransportEndInput tests EndInput method.
func TestTransportEndInput(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	// Should not error even if not connected
	err = transport.EndInput()
	if err != nil {
		t.Errorf("EndInput failed: %v", err)
	}
}

// TestTransportChannels tests message and error channels.
func TestTransportChannels(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	messages := transport.ReadMessages()
	if messages == nil {
		t.Error("Expected non-nil messages channel")
	}

	errors := transport.Errors()
	if errors == nil {
		t.Error("Expected non-nil errors channel")
	}
}

// TestStderrCallback tests stderr callback functionality.
func TestStderrCallback(t *testing.T) {
	var called bool
	var receivedLine string

	opts := &TransportOptions{
		CLIPath: "/fake/path/claude",
		StderrCallback: func(line string) {
			called = true
			receivedLine = line
		},
	}

	transport, err := NewSubprocessTransport("test", opts)
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	if transport.options.StderrCallback == nil {
		t.Error("Expected stderr callback to be set")
	}

	// Test callback
	transport.options.StderrCallback("test line")
	if !called {
		t.Error("Stderr callback was not called")
	}
	if receivedLine != "test line" {
		t.Errorf("Expected 'test line', got %q", receivedLine)
	}
}

// TestConcurrentWrite tests thread-safe write operations.
func TestConcurrentWrite(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	// Simulate concurrent writes (should not panic due to mutex)
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			transport.Write("test\n")
			done <- true
		}()
	}

	// Wait for all goroutines with timeout
	timeout := time.After(1 * time.Second)
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-timeout:
			t.Fatal("Timeout waiting for concurrent writes")
		}
	}
}

// TestCLAUDECODEEnvFiltered tests that the CLAUDECODE env var is filtered out
// from inherited environment so SDK-spawned subprocesses don't think they're
// running inside a Claude Code parent.
func TestCLAUDECODEEnvFiltered(t *testing.T) {
	// Set CLAUDECODE in current env
	os.Setenv("CLAUDECODE", "1")
	defer os.Unsetenv("CLAUDECODE")

	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "CLAUDECODE=") {
			t.Error("CLAUDECODE env var should be filtered out from inherited environment")
			break
		}
	}
}

// TestEntrypointDefaultBeforeUserEnv tests that CLAUDE_CODE_ENTRYPOINT defaults
// to sdk-go but can be overridden by user-provided env.
func TestEntrypointDefaultBeforeUserEnv(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env: map[string]string{
			"CLAUDE_CODE_ENTRYPOINT": "custom-entrypoint",
		},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	// The last occurrence should win; user env is appended after default
	var lastValue string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "CLAUDE_CODE_ENTRYPOINT=") {
			lastValue = strings.TrimPrefix(env, "CLAUDE_CODE_ENTRYPOINT=")
		}
	}

	if lastValue != "custom-entrypoint" {
		t.Errorf("Expected user env to override CLAUDE_CODE_ENTRYPOINT, got '%s'", lastValue)
	}
}

// TestSessionIDFlag tests that --session-id flag is generated.
func TestSessionIDFlag(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:   "/fake/path/claude",
		SessionID: "test-session-123",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--session-id test-session-123") {
		t.Errorf("Expected --session-id flag in args: %s", args)
	}
}

// TestTaskBudgetFlag tests that --task-budget flag is generated.
func TestTaskBudgetFlag(t *testing.T) {
	budget := 1000
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:    "/fake/path/claude",
		TaskBudget: &budget,
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--task-budget 1000") {
		t.Errorf("Expected --task-budget flag in args: %s", args)
	}
}

// TestSystemPromptFileFlag tests that --system-prompt-file flag is generated.
func TestSystemPromptFileFlag(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:          "/fake/path/claude",
		SystemPromptFile: &SystemPromptFile{Type: "file", Path: "/path/to/prompt.md"},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--system-prompt-file /path/to/prompt.md") {
		t.Errorf("Expected --system-prompt-file flag in args: %s", args)
	}
	// Should NOT have --system-prompt when --system-prompt-file is set
	if strings.Contains(args, "--system-prompt \"\"") || strings.Contains(args, "--system-prompt  ") {
		t.Errorf("Should not have --system-prompt when --system-prompt-file is set: %s", args)
	}
}

// TestSettingSourcesOmittedWhenNil tests that --setting-sources is NOT passed
// when SettingSources is nil (fixing the bug where an empty string was passed,
// causing the CLI to misparse subsequent flags).
func TestSettingSourcesOmittedWhenNil(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--setting-sources") {
		t.Errorf("Expected no --setting-sources flag when nil, got: %s", args)
	}
}

// TestSettingSourcesOmittedWhenEmpty tests that --setting-sources= IS passed
// when SettingSources is explicitly set to an empty slice (clears all sources).
// This matches Python SDK 0.1.65 behaviour: explicit empty list → --setting-sources=
func TestSettingSourcesOmittedWhenEmpty(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:        "/fake/path/claude",
		SettingSources: []string{},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--setting-sources=") {
		t.Errorf("Expected --setting-sources= when SettingSources is explicitly empty, got: %s", args)
	}
}

// TestSettingSourcesPassedWhenPopulated tests that --setting-sources=values is passed
// using = syntax (Python SDK 0.1.65 format).
func TestSettingSourcesPassedWhenPopulated(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:        "/fake/path/claude",
		SettingSources: []string{"local", "project"},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--setting-sources=local,project") {
		t.Errorf("Expected --setting-sources=local,project in args: %s", args)
	}
}

// TestSDKVersionAlwaysSet tests that CLAUDE_AGENT_SDK_VERSION is always set
// in the subprocess environment and cannot be overridden by user-provided env —
// matching Python SDK behavior (#756).
func TestSDKVersionAlwaysSet(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	var found string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "CLAUDE_AGENT_SDK_VERSION=") {
			found = strings.TrimPrefix(env, "CLAUDE_AGENT_SDK_VERSION=")
		}
	}

	if found == "" {
		t.Error("CLAUDE_AGENT_SDK_VERSION must always be set in subprocess env")
	}
	if found != sdkVersion {
		t.Errorf("CLAUDE_AGENT_SDK_VERSION=%q, want %q", found, sdkVersion)
	}
}

// TestSDKVersionNotOverridableByUserEnv tests that user-provided env cannot
// override the SDK version — CLAUDE_AGENT_SDK_VERSION is always the SDK's own
// value, matching Python SDK behavior (test_options_env_cannot_override_sdk_version).
func TestSDKVersionNotOverridableByUserEnv(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env:     map[string]string{"CLAUDE_AGENT_SDK_VERSION": "0.0.0"},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	var lastValue string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "CLAUDE_AGENT_SDK_VERSION=") {
			lastValue = strings.TrimPrefix(env, "CLAUDE_AGENT_SDK_VERSION=")
		}
	}

	if lastValue != sdkVersion {
		t.Errorf("User env should not override CLAUDE_AGENT_SDK_VERSION: got %q, want %q", lastValue, sdkVersion)
	}
}

// TestMAXMCPOutputTokensPassthrough tests that MAX_MCP_OUTPUT_TOKENS set in
// options.Env is passed through to the CLI subprocess (layer-1 threshold).
func TestMAXMCPOutputTokensPassthrough(t *testing.T) {
	transport, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env:     map[string]string{"MAX_MCP_OUTPUT_TOKENS": "500000"},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}

	cmd := transport.buildCommand(context.Background())

	for _, env := range cmd.Env {
		if env == "MAX_MCP_OUTPUT_TOKENS=500000" {
			return // pass
		}
	}
	t.Error("MAX_MCP_OUTPUT_TOKENS was not passed to the CLI subprocess")
}

// ---- Tests for new subprocess behaviour added in v0.1.58–v0.1.65 ----

// TestBuildCommand_ThinkingDisplayForwarded verifies that --thinking-display is
// emitted when Thinking.Display is set and type is not "disabled".
func TestBuildCommand_ThinkingDisplayForwarded(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Thinking: &ThinkingConfig{
			Type:    "adaptive",
			Display: "summarized",
		},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--thinking-display summarized") {
		t.Errorf("Expected --thinking-display summarized in args: %s", args)
	}
}

// TestBuildCommand_ThinkingWithoutDisplay verifies that --thinking-display is
// NOT emitted when Thinking.Display is empty.
func TestBuildCommand_ThinkingWithoutDisplay(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Thinking: &ThinkingConfig{
			Type: "adaptive",
		},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--thinking-display") {
		t.Errorf("Did not expect --thinking-display in args: %s", args)
	}
}

// TestBuildCommand_ThinkingDisabledNoDisplay verifies that --thinking-display is
// NOT emitted when Thinking.Type is "disabled" even if Display is set.
func TestBuildCommand_ThinkingDisabledNoDisplay(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Thinking: &ThinkingConfig{
			Type:    "disabled",
			Display: "omitted",
		},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--thinking-display") {
		t.Errorf("Did not expect --thinking-display when thinking is disabled: %s", args)
	}
}

// TestBuildCommand_SkillsAllInjectsSkillTool verifies that Skills="all" injects
// "Skill" into AllowedTools and defaults setting_sources to user,project.
func TestBuildCommand_SkillsAllInjectsSkillTool(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Skills:  "all",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "Skill") {
		t.Errorf("Expected Skill in --allowedTools: %s", args)
	}
	if !strings.Contains(args, "--setting-sources=user,project") {
		t.Errorf("Expected --setting-sources=user,project for skills: %s", args)
	}
}

// TestBuildCommand_SkillsListInjectsSkillNameTool verifies that Skills=[]string
// injects "Skill(name)" entries into AllowedTools.
func TestBuildCommand_SkillsListInjectsSkillNameTool(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
		Skills:  []string{"my-skill", "other-skill"},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "Skill(my-skill)") {
		t.Errorf("Expected Skill(my-skill) in --allowedTools: %s", args)
	}
	if !strings.Contains(args, "Skill(other-skill)") {
		t.Errorf("Expected Skill(other-skill) in --allowedTools: %s", args)
	}
}

// TestBuildCommand_SkillsNilNoSkillTool verifies that nil Skills does NOT
// inject any Skill tool or change setting_sources.
func TestBuildCommand_SkillsNilNoSkillTool(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "Skill") {
		t.Errorf("Did not expect Skill tool when Skills is nil: %s", args)
	}
	if strings.Contains(args, "--setting-sources") {
		t.Errorf("Did not expect --setting-sources when Skills is nil and SettingSources unset: %s", args)
	}
}

// TestBuildCommand_SkillsUserSettingSourcesNotOverridden verifies that
// explicit SettingSources is respected even when Skills is set.
func TestBuildCommand_SkillsUserSettingSourcesNotOverridden(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:        "/fake/path/claude",
		Skills:         "all",
		SettingSources: []string{"user"},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--setting-sources=user") {
		t.Errorf("Expected --setting-sources=user (user-supplied override): %s", args)
	}
	// Must not silently override to user,project
	if strings.Contains(args, "--setting-sources=user,project") {
		t.Errorf("Expected user-supplied SettingSources to take precedence: %s", args)
	}
}

// TestBuildCommand_SessionMirrorFlag verifies that --session-mirror is emitted
// when SessionStore is set.
func TestBuildCommand_SessionMirrorFlag(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:      "/fake/path/claude",
		SessionStore: struct{}{}, // non-nil sentinel
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--session-mirror") {
		t.Errorf("Expected --session-mirror when SessionStore is set: %s", args)
	}
}

// TestBuildCommand_SessionMirrorNotEmittedWhenNil verifies that --session-mirror
// is NOT emitted when SessionStore is nil.
func TestBuildCommand_SessionMirrorNotEmittedWhenNil(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--session-mirror") {
		t.Errorf("Did not expect --session-mirror when SessionStore is nil: %s", args)
	}
}

// TestSettingSourcesNilOmitted verifies that --setting-sources is NOT emitted
// when SettingSources is nil and Skills is also nil.
func TestSettingSourcesNilOmitted(t *testing.T) {
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath: "/fake/path/claude",
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--setting-sources") {
		t.Errorf("Expected no --setting-sources when SettingSources is nil: %s", args)
	}
}
