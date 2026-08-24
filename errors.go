package claude

import (
	"errors"
	"fmt"
	"strings"
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

// ResultError indicates that the CLI exited after reporting a terminal error
// result.
//
// The CLI ends a failed run by emitting a "result" message with
// is_error: true (yielded to you as a ResultMessage) and then exiting
// non-zero. This error replaces the bare "exit code 1" ProcessError for that
// case and carries the result's payload, so callers can branch on *why* the
// run failed without string matching:
//
//	var resultErr *claude.ResultError
//	if errors.As(err, &resultErr) {
//		if resultErr.TerminalReason == "api_error" { // e.g. overloaded / timeout
//			retry()
//		} else if resultErr.Subtype == "error_max_turns" {
//			...
//		}
//	}
//
// It embeds ProcessError, so ExitCode/Stderr and the "(exit code: N)" error
// text behave exactly as for a plain ProcessError, and IsProcessError reports
// true for it (mirroring Python, where ResultError subclasses ProcessError).
type ResultError struct {
	ProcessError
	// Subtype is the result subtype ("error_max_turns",
	// "error_during_execution", ... — or "success" when the agent loop itself
	// completed but the last turn was an API error). Empty when absent or not
	// a string.
	Subtype string
	// Errors holds the error strings reported by the CLI (never nil; may be
	// empty). Bare-string "errors" payloads are wrapped, and non-string or
	// blank entries are dropped, so this field always agrees with the text
	// the SDK builds the error message from.
	Errors []string
	// Result is the result text, if any. For API failures this holds the
	// "API Error: ..." prose.
	Result string
	// APIErrorStatus is the HTTP status of the failing API call, or nil.
	APIErrorStatus *int
	// TerminalReason reports why the run ended (e.g. "api_error",
	// "max_turns"), if reported by the CLI.
	TerminalReason string
	// SessionID is the session the result belongs to, if reported.
	SessionID string
	// Data is the raw "result" message payload as emitted by the CLI.
	Data map[string]interface{}
}

// NewResultError creates a new ResultError from the raw "result" message
// payload. A nil (or otherwise unusable) data map is treated as empty, and
// each structured field is extracted only when it has the expected type, so
// malformed payloads degrade to zero values instead of failing.
func NewResultError(message string, data map[string]interface{}, exitCode int) *ResultError {
	if data == nil {
		data = map[string]interface{}{}
	}
	err := &ResultError{
		ProcessError: ProcessError{
			ClaudeSDKError: ClaudeSDKError{Message: message},
			ExitCode:       exitCode,
		},
		Data:   data,
		Errors: normalizeResultErrors(data["errors"]),
	}
	if subtype, ok := data["subtype"].(string); ok {
		err.Subtype = subtype
	}
	if result, ok := data["result"].(string); ok {
		err.Result = result
	}
	switch status := data["api_error_status"].(type) {
	case float64:
		v := int(status)
		err.APIErrorStatus = &v
	case int:
		v := status
		err.APIErrorStatus = &v
	}
	if reason, ok := data["terminal_reason"].(string); ok {
		err.TerminalReason = reason
	}
	if sessionID, ok := data["session_id"].(string); ok {
		err.SessionID = sessionID
	}
	return err
}

// normalizeResultErrors normalizes the "errors" field of a "result" frame to
// clean strings.
//
// The CLI emits a list of strings; tolerate a bare string (older/buggy
// emitters) and drop non-string or blank entries so the structured
// ResultError.Errors and the error text always agree.
func normalizeResultErrors(raw interface{}) []string {
	var items []interface{}
	switch v := raw.(type) {
	case string:
		items = []interface{}{v}
	case []interface{}:
		items = v
	case []string:
		items = make([]interface{}, len(v))
		for i, s := range v {
			items[i] = s
		}
	default:
		return []string{}
	}
	errs := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				errs = append(errs, trimmed)
			}
		}
	}
	return errs
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

// IsProcessError checks if the error is a ProcessError. ResultError embeds
// ProcessError (mirroring the Python subclass relationship), so it counts.
func IsProcessError(err error) bool {
	var e *ProcessError
	if errors.As(err, &e) {
		return true
	}
	var re *ResultError
	return errors.As(err, &re)
}

// IsResultError checks if the error is a ResultError.
func IsResultError(err error) bool {
	var e *ResultError
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
