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

// ---------------------------------------------------------------------------
// Tests ported from Python SDK v0.2.133..v0.2.143 (#1205)
// ---------------------------------------------------------------------------

// TestResultErrorCarriesPayload tests that ResultError is a ProcessError that
// exposes the result payload (ported from Python SDK #1205, commit 90ab957).
func TestResultErrorCarriesPayload(t *testing.T) {
	data := map[string]interface{}{
		"type":             "result",
		"subtype":          "success",
		"is_error":         true,
		"errors":           []interface{}{},
		"result":           "API Error: Stream idle timeout - no chunks received",
		"api_error_status": nil,
		"terminal_reason":  "api_error",
		"session_id":       "s-1",
	}
	err := NewResultError("Claude Code returned an error result: x", data, 1)

	// Behaves as a ProcessError (Python: subclass relationship).
	if !IsProcessError(err) {
		t.Error("IsProcessError should return true for ResultError")
	}
	if !IsResultError(err) {
		t.Error("IsResultError should return true")
	}
	// The base ClaudeSDKError fields are reachable through the embedding.
	if err.Message != "Claude Code returned an error result: x" {
		t.Errorf("Unexpected base Message: %q", err.Message)
	}

	if err.ExitCode != 1 {
		t.Errorf("Expected ExitCode=1, got %d", err.ExitCode)
	}
	if fmt.Sprintf("%p", err.Data) != fmt.Sprintf("%p", data) {
		t.Error("Expected Data to be the same map passed in")
	}
	if err.Subtype != "success" {
		t.Errorf("Expected Subtype 'success', got %q", err.Subtype)
	}
	if len(err.Errors) != 0 {
		t.Errorf("Expected empty Errors, got %v", err.Errors)
	}
	if err.Result != "API Error: Stream idle timeout - no chunks received" {
		t.Errorf("Unexpected Result: %q", err.Result)
	}
	if err.APIErrorStatus != nil {
		t.Errorf("Expected nil APIErrorStatus, got %v", *err.APIErrorStatus)
	}
	if err.TerminalReason != "api_error" {
		t.Errorf("Expected TerminalReason 'api_error', got %q", err.TerminalReason)
	}
	if err.SessionID != "s-1" {
		t.Errorf("Expected SessionID 's-1', got %q", err.SessionID)
	}
	if !strings.Contains(err.Error(), "exit code: 1") {
		t.Errorf("Expected error text to contain 'exit code: 1', got %q", err.Error())
	}
}

// TestResultErrorToleratesMissingOrMalformedFields tests that malformed
// payload fields degrade to zero values.
func TestResultErrorToleratesMissingOrMalformedFields(t *testing.T) {
	err := NewResultError("boom", map[string]interface{}{
		"errors":           float64(42),
		"api_error_status": "500",
	}, 0)
	if err.Subtype != "" {
		t.Errorf("Expected empty Subtype, got %q", err.Subtype)
	}
	if len(err.Errors) != 0 {
		t.Errorf("Expected empty Errors, got %v", err.Errors)
	}
	if err.Result != "" {
		t.Errorf("Expected empty Result, got %q", err.Result)
	}
	if err.APIErrorStatus != nil {
		t.Errorf("Expected nil APIErrorStatus, got %v", *err.APIErrorStatus)
	}
	if err.TerminalReason != "" {
		t.Errorf("Expected empty TerminalReason, got %q", err.TerminalReason)
	}
	if err.SessionID != "" {
		t.Errorf("Expected empty SessionID, got %q", err.SessionID)
	}
	if err.ExitCode != 0 {
		t.Errorf("Expected ExitCode=0, got %d", err.ExitCode)
	}

	nilData := NewResultError("boom", nil, 0)
	if nilData.Data == nil || len(nilData.Data) != 0 {
		t.Errorf("Expected non-nil empty Data, got %v", nilData.Data)
	}
}

// TestResultErrorNormalizesErrors tests that a bare-string "errors" is kept
// and blank entries are dropped, so the structured field agrees with the text
// the reader builds from it.
func TestResultErrorNormalizesErrors(t *testing.T) {
	bare := NewResultError("m", map[string]interface{}{"errors": "boom"}, 0)
	if len(bare.Errors) != 1 || bare.Errors[0] != "boom" {
		t.Errorf("Expected [boom], got %v", bare.Errors)
	}

	mixed := NewResultError("m", map[string]interface{}{
		"errors": []interface{}{" ", "x ", float64(3)},
	}, 0)
	if len(mixed.Errors) != 1 || mixed.Errors[0] != "x" {
		t.Errorf("Expected [x], got %v", mixed.Errors)
	}

	// []string payloads (Go-side construction) are accepted too.
	goSlice := NewResultError("m", map[string]interface{}{"errors": []string{" a ", ""}}, 0)
	if len(goSlice.Errors) != 1 || goSlice.Errors[0] != "a" {
		t.Errorf("Expected [a], got %v", goSlice.Errors)
	}
}

// TestResultErrorAPIErrorStatus tests that api_error_status is extracted from
// both JSON-decoded (float64) and Go-native (int) payloads.
func TestResultErrorAPIErrorStatus(t *testing.T) {
	for _, raw := range []interface{}{float64(529), 529} {
		err := NewResultError("m", map[string]interface{}{"api_error_status": raw}, 1)
		if err.APIErrorStatus == nil || *err.APIErrorStatus != 529 {
			t.Errorf("api_error_status=%v: expected 529, got %v", raw, err.APIErrorStatus)
		}
	}
}

// TestResultErrorWrapped tests errors.As behavior through a wrapped error and
// that unrelated errors are not classified as ResultError.
func TestResultErrorWrapped(t *testing.T) {
	err := NewResultError("boom", map[string]interface{}{"subtype": "error_max_turns"}, 1)
	wrapped := fmt.Errorf("query failed: %w", err)

	var resultErr *ResultError
	if !errors.As(wrapped, &resultErr) {
		t.Fatal("errors.As should resolve *ResultError through wrapping")
	}
	if resultErr.Subtype != "error_max_turns" {
		t.Errorf("Expected Subtype 'error_max_turns', got %q", resultErr.Subtype)
	}
	if !IsResultError(wrapped) {
		t.Error("IsResultError should return true through wrapping")
	}
	if !IsProcessError(wrapped) {
		t.Error("IsProcessError should return true for a wrapped ResultError")
	}

	if IsResultError(NewProcessError("plain", 1, "")) {
		t.Error("IsResultError should return false for a plain ProcessError")
	}
	if IsResultError(errors.New("other")) {
		t.Error("IsResultError should return false for unrelated errors")
	}
}
