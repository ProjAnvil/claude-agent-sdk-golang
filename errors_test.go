package claude

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestCLINotFoundError tests CLINotFoundError creation and behavior.
func TestCLINotFoundError(t *testing.T) {
	err := NewCLINotFoundError("/usr/local/bin/claude")

	if err.CLIPath != "/usr/local/bin/claude" {
		t.Errorf("Expected CLIPath='/usr/local/bin/claude', got %q", err.CLIPath)
	}

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Expected non-empty error message")
	}

	// Test error type checking
	if !IsCLINotFoundError(err) {
		t.Error("IsCLINotFoundError should return true")
	}

	// Test errors.As
	var target *CLINotFoundError
	if !errors.As(err, &target) {
		t.Error("errors.As should work with CLINotFoundError")
	}
}

// TestCLIConnectionError tests CLIConnectionError creation and behavior.
func TestCLIConnectionError(t *testing.T) {
	cause := errors.New("connection refused")
	err := NewCLIConnectionError("failed to connect", cause)

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Expected non-empty error message")
	}

	// Test unwrap
	if errors.Unwrap(err) != cause {
		t.Error("Unwrap should return the cause error")
	}

	// Test error type checking
	if !IsCLIConnectionError(err) {
		t.Error("IsCLIConnectionError should return true")
	}
}

// TestProcessError tests ProcessError creation and behavior.
func TestProcessError(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		exitCode int
		stderr   string
	}{
		{"with exit code", "process failed", 1, ""},
		{"with stderr", "process failed", 2, "error output"},
		{"with both", "process failed", 127, "command not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewProcessError(tt.message, tt.exitCode, tt.stderr)

			if err.ExitCode != tt.exitCode {
				t.Errorf("Expected ExitCode=%d, got %d", tt.exitCode, err.ExitCode)
			}
			if err.Stderr != tt.stderr {
				t.Errorf("Expected Stderr=%q, got %q", tt.stderr, err.Stderr)
			}

			errMsg := err.Error()
			if errMsg == "" {
				t.Error("Expected non-empty error message")
			}

			if tt.exitCode != 0 {
				expected := fmt.Sprintf("exit code: %d", tt.exitCode)
				if !strings.Contains(errMsg, expected) {
					t.Errorf("Expected error message to contain %q, got %q", expected, errMsg)
				}
			}
			if tt.stderr != "" {
				if !strings.Contains(errMsg, tt.stderr) {
					t.Errorf("Expected error message to contain stderr %q, got %q", tt.stderr, errMsg)
				}
			}

			// Test error type checking
			if !IsProcessError(err) {
				t.Error("IsProcessError should return true")
			}
		})
	}
}

// TestCLIJSONDecodeError tests CLIJSONDecodeError creation and behavior.
func TestCLIJSONDecodeError(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"short line", `{"invalid": json`},
		{"long line", `{"this": "is", "a": "very", "long": "json", "line": "that", "should": "be", "truncated": "in", "the": "error", "message": "because", "it": "exceeds", "the": "maximum", "length"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New("json parse error")
			err := NewCLIJSONDecodeError(tt.line, cause)

			if err.Line != tt.line {
				t.Errorf("Expected Line=%q, got %q", tt.line, err.Line)
			}

			errMsg := err.Error()
			if errMsg == "" {
				t.Error("Expected non-empty error message")
			}

			// Test unwrap
			if errors.Unwrap(err) != cause {
				t.Error("Unwrap should return the cause error")
			}

			// Test error type checking
			if !IsCLIJSONDecodeError(err) {
				t.Error("IsCLIJSONDecodeError should return true")
			}
		})
	}
}

// TestMessageParseError tests MessageParseError creation and behavior.
func TestMessageParseError(t *testing.T) {
	data := map[string]interface{}{
		"type": "unknown",
		"data": "test",
	}
	err := NewMessageParseError("unknown message type", data)

	if err.Data == nil {
		t.Error("Expected Data to be set")
	}

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Expected non-empty error message")
	}

	// Test error type checking
	if !IsMessageParseError(err) {
		t.Error("IsMessageParseError should return true")
	}
}

// TestTimeoutError tests TimeoutError creation and behavior.
func TestTimeoutError(t *testing.T) {
	err := NewTimeoutError("interrupt")

	if err.RequestType != "interrupt" {
		t.Errorf("Expected RequestType='interrupt', got %q", err.RequestType)
	}

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Expected non-empty error message")
	}

	// Test error type checking
	if !IsTimeoutError(err) {
		t.Error("IsTimeoutError should return true")
	}
}

// TestBufferOverflowError tests BufferOverflowError creation and behavior.
func TestBufferOverflowError(t *testing.T) {
	err := NewBufferOverflowError(2000000, 1000000)

	if err.BufferSize != 2000000 {
		t.Errorf("Expected BufferSize=2000000, got %d", err.BufferSize)
	}
	if err.Limit != 1000000 {
		t.Errorf("Expected Limit=1000000, got %d", err.Limit)
	}

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Expected non-empty error message")
	}

	// Test error type checking
	if !IsBufferOverflowError(err) {
		t.Error("IsBufferOverflowError should return true")
	}
}

// TestErrorTypeChecking tests that error type checking functions work correctly.
func TestErrorTypeChecking(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		checkFunc func(error) bool
		expected  bool
	}{
		{"CLINotFoundError positive", NewCLINotFoundError(""), IsCLINotFoundError, true},
		{"CLINotFoundError negative", NewProcessError("", 1, ""), IsCLINotFoundError, false},
		{"CLIConnectionError positive", NewCLIConnectionError("", nil), IsCLIConnectionError, true},
		{"CLIConnectionError negative", NewTimeoutError(""), IsCLIConnectionError, false},
		{"ProcessError positive", NewProcessError("", 1, ""), IsProcessError, true},
		{"ProcessError negative", NewCLINotFoundError(""), IsProcessError, false},
		{"CLIJSONDecodeError positive", NewCLIJSONDecodeError("", nil), IsCLIJSONDecodeError, true},
		{"CLIJSONDecodeError negative", NewMessageParseError("", nil), IsCLIJSONDecodeError, false},
		{"MessageParseError positive", NewMessageParseError("", nil), IsMessageParseError, true},
		{"MessageParseError negative", NewTimeoutError(""), IsMessageParseError, false},
		{"TimeoutError positive", NewTimeoutError(""), IsTimeoutError, true},
		{"TimeoutError negative", NewBufferOverflowError(0, 0), IsTimeoutError, false},
		{"BufferOverflowError positive", NewBufferOverflowError(0, 0), IsBufferOverflowError, true},
		{"BufferOverflowError negative", NewCLINotFoundError(""), IsBufferOverflowError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.checkFunc(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestClaudeSDKErrorUnwrap tests the Unwrap functionality.
func TestClaudeSDKErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := NewClaudeSDKError("wrapper error", cause)

	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Errorf("Expected unwrapped error to be %v, got %v", cause, unwrapped)
	}

	// Test with nil cause
	errNoCause := NewClaudeSDKError("no cause", nil)
	if errors.Unwrap(errNoCause) != nil {
		t.Error("Expected Unwrap to return nil for error with no cause")
	}
}
