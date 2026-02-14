package claude

import (
	"errors"
	"fmt"
)

// ClaudeSDKError is the base error type for all Claude SDK errors.
type ClaudeSDKError struct {
	Message string
	Cause   error
}

func (e *ClaudeSDKError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *ClaudeSDKError) Unwrap() error {
	return e.Cause
}

// NewClaudeSDKError creates a new ClaudeSDKError.
func NewClaudeSDKError(message string, cause error) *ClaudeSDKError {
	return &ClaudeSDKError{Message: message, Cause: cause}
}

// CLINotFoundError indicates that the Claude Code CLI is not installed or not found.
type CLINotFoundError struct {
	ClaudeSDKError
	CLIPath string
}

func (e *CLINotFoundError) Error() string {
	if e.CLIPath != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.CLIPath)
	}
	return e.Message
}

// NewCLINotFoundError creates a new CLINotFoundError.
func NewCLINotFoundError(cliPath string) *CLINotFoundError {
	return &CLINotFoundError{
		ClaudeSDKError: ClaudeSDKError{
			Message: "Claude Code not found. Install with:\n" +
				"  curl -fsSL https://claude.ai/install.sh | bash\n\n" +
				"If already installed, provide the path via ClaudeAgentOptions:\n" +
				"  &ClaudeAgentOptions{CLIPath: \"/path/to/claude\"}",
		},
		CLIPath: cliPath,
	}
}

// CLIConnectionError indicates connection or transport issues with the CLI.
type CLIConnectionError struct {
	ClaudeSDKError
}

// NewCLIConnectionError creates a new CLIConnectionError.
func NewCLIConnectionError(message string, cause error) *CLIConnectionError {
	return &CLIConnectionError{
		ClaudeSDKError: ClaudeSDKError{Message: message, Cause: cause},
	}
}

// ProcessError indicates that the CLI process failed with a non-zero exit code.
type ProcessError struct {
	ClaudeSDKError
	ExitCode int
	Stderr   string
}

func (e *ProcessError) Error() string {
	msg := e.Message
	if e.ExitCode != 0 {
		msg = fmt.Sprintf("%s (exit code: %d)", msg, e.ExitCode)
	}
	if e.Stderr != "" {
		msg = fmt.Sprintf("%s\nError output: %s", msg, e.Stderr)
	}
	return msg
}

// NewProcessError creates a new ProcessError.
func NewProcessError(message string, exitCode int, stderr string) *ProcessError {
	return &ProcessError{
		ClaudeSDKError: ClaudeSDKError{Message: message},
		ExitCode:       exitCode,
		Stderr:         stderr,
	}
}

// CLIJSONDecodeError indicates that JSON parsing from CLI output failed.
type CLIJSONDecodeError struct {
	ClaudeSDKError
	Line string
}

func (e *CLIJSONDecodeError) Error() string {
	truncated := e.Line
	if len(truncated) > 100 {
		truncated = truncated[:100] + "..."
	}
	return fmt.Sprintf("failed to decode JSON: %s", truncated)
}

// NewCLIJSONDecodeError creates a new CLIJSONDecodeError.
func NewCLIJSONDecodeError(line string, cause error) *CLIJSONDecodeError {
	return &CLIJSONDecodeError{
		ClaudeSDKError: ClaudeSDKError{Message: "failed to decode JSON", Cause: cause},
		Line:           line,
	}
}

// MessageParseError indicates that message parsing failed due to unknown type or structure.
type MessageParseError struct {
	ClaudeSDKError
	Data map[string]interface{}
}

func (e *MessageParseError) Error() string {
	return e.Message
}

// NewMessageParseError creates a new MessageParseError.
func NewMessageParseError(message string, data map[string]interface{}) *MessageParseError {
	return &MessageParseError{
		ClaudeSDKError: ClaudeSDKError{Message: message},
		Data:           data,
	}
}

// TimeoutError indicates that a control request timed out.
type TimeoutError struct {
	ClaudeSDKError
	RequestType string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("control request timeout: %s", e.RequestType)
}

// NewTimeoutError creates a new TimeoutError.
func NewTimeoutError(requestType string) *TimeoutError {
	return &TimeoutError{
		ClaudeSDKError: ClaudeSDKError{Message: "control request timeout"},
		RequestType:    requestType,
	}
}

// BufferOverflowError indicates that a message exceeded the buffer size limit.
type BufferOverflowError struct {
	ClaudeSDKError
	BufferSize int
	Limit      int
}

func (e *BufferOverflowError) Error() string {
	return fmt.Sprintf("JSON message exceeded maximum buffer size of %d bytes (current: %d)", e.Limit, e.BufferSize)
}

// NewBufferOverflowError creates a new BufferOverflowError.
func NewBufferOverflowError(bufferSize, limit int) *BufferOverflowError {
	return &BufferOverflowError{
		ClaudeSDKError: ClaudeSDKError{Message: "buffer overflow"},
		BufferSize:     bufferSize,
		Limit:          limit,
	}
}

// Error type checking helpers

// IsCLINotFoundError checks if the error is a CLINotFoundError.
func IsCLINotFoundError(err error) bool {
	var e *CLINotFoundError
	return errors.As(err, &e)
}

// IsCLIConnectionError checks if the error is a CLIConnectionError.
func IsCLIConnectionError(err error) bool {
	var e *CLIConnectionError
	return errors.As(err, &e)
}

// IsProcessError checks if the error is a ProcessError.
func IsProcessError(err error) bool {
	var e *ProcessError
	return errors.As(err, &e)
}

// IsCLIJSONDecodeError checks if the error is a CLIJSONDecodeError.
func IsCLIJSONDecodeError(err error) bool {
	var e *CLIJSONDecodeError
	return errors.As(err, &e)
}

// IsMessageParseError checks if the error is a MessageParseError.
func IsMessageParseError(err error) bool {
	var e *MessageParseError
	return errors.As(err, &e)
}

// IsTimeoutError checks if the error is a TimeoutError.
func IsTimeoutError(err error) bool {
	var e *TimeoutError
	return errors.As(err, &e)
}

// IsBufferOverflowError checks if the error is a BufferOverflowError.
func IsBufferOverflowError(err error) bool {
	var e *BufferOverflowError
	return errors.As(err, &e)
}
