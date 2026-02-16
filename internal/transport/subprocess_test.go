package transport

import (
	"context"
	"encoding/json"
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
				"--setting-sources", "",
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
