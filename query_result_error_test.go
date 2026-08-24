package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// collectQuery drains both Query channels and returns messages and errors.
func collectQuery(messages <-chan Message, errs <-chan error) ([]Message, []error) {
	var msgs []Message
	var errors []error
	for messages != nil || errs != nil {
		select {
		case msg, ok := <-messages:
			if !ok {
				messages = nil
				continue
			}
			msgs = append(msgs, msg)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			errors = append(errors, err)
		}
	}
	return msgs, errors
}

// errorResultFrame returns a complete result frame with is_error=true and the
// given extra fields merged in (mirrors _error_result in python tests).
func errorResultFrame(extra map[string]interface{}) map[string]interface{} {
	frame := map[string]interface{}{
		"type":            "result",
		"subtype":         "error_during_execution",
		"duration_ms":     float64(100),
		"duration_api_ms": float64(50),
		"is_error":        true,
		"num_turns":       float64(1),
		"session_id":      "s",
	}
	for k, v := range extra {
		frame[k] = v
	}
	return frame
}

// TestQueryErrorResultRaisesResultError verifies that when the CLI emits a
// result with is_error=true and then exits non-zero, Query surfaces a typed
// *ResultError carrying the payload and exit code, with the original
// ProcessError chained as cause. Mirrors the Python #1205 read-loop tests.
func TestQueryErrorResultRaisesResultError(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- errorResultFrame(map[string]interface{}{
			"subtype":   "error_max_turns",
			"num_turns": float64(60),
			"errors":    []interface{}{"Reached maximum number of turns (60)"},
		})
		// The CLI then exits 1 on purpose (for shell-script consumers).
		mockTrans.errCh <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
		mockTrans.Close()
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)
	msgs, errors := collectQuery(messages, errs)

	if len(msgs) != 1 {
		t.Errorf("Expected 1 message (the error result), got %d", len(msgs))
	}
	if len(errors) != 1 {
		t.Fatalf("Expected 1 error, got %d: %v", len(errors), errors)
	}

	err := errors[0]
	if !IsResultError(err) {
		t.Fatalf("Expected ResultError, got %T: %v", err, err)
	}
	if !IsProcessError(err) {
		t.Errorf("ResultError must also satisfy IsProcessError: %v", err)
	}
	re, ok := err.(*ResultError)
	if !ok {
		t.Fatalf("Expected *ResultError, got %T", err)
	}
	if re.ExitCode != 1 {
		t.Errorf("Expected ExitCode=1, got %d", re.ExitCode)
	}
	if re.Subtype != "error_max_turns" {
		t.Errorf("Expected Subtype=error_max_turns, got %q", re.Subtype)
	}
	if len(re.Errors) != 1 || re.Errors[0] != "Reached maximum number of turns (60)" {
		t.Errorf("Expected Errors=[Reached maximum number of turns (60)], got %v", re.Errors)
	}
	if !strings.Contains(re.Error(), "Claude Code returned an error result: Reached maximum number of turns (60)") {
		t.Errorf("Expected actionable error text, got: %v", re)
	}
	if re.Data["subtype"] != "error_max_turns" {
		t.Errorf("Expected raw payload preserved, got %v", re.Data)
	}
	// The original exit error is chained as the cause.
	var cause *ProcessError
	if re.Cause == nil {
		t.Error("Expected chained cause")
	} else {
		if pe, isPE := re.Cause.(*ProcessError); isPE {
			cause = pe
		}
		if cause == nil || !strings.Contains(cause.Error(), "Command failed") {
			t.Errorf("Expected cause to be the wrapped ProcessError, got: %v", re.Cause)
		}
	}
}

// TestQueryDrainsErrorsAfterStreamClose verifies that errors the internal
// read loop delivered just before closing the message stream are not lost
// when the top-level select observes the close first: every error the
// transport reported must reach the caller.
func TestQueryDrainsErrorsAfterStreamClose(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	const numErrors = 8
	go func() {
		time.Sleep(10 * time.Millisecond)
		// No result frame: the CLI crashes during startup. The read loop
		// forwards all of these and then closes the message stream, so the
		// top-level select sees a closed stream with errors still buffered.
		for i := 0; i < numErrors; i++ {
			mockTrans.errCh <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
		}
		mockTrans.Close()
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)
	_, errors := collectQuery(messages, errs)

	if len(errors) != numErrors {
		t.Errorf("Expected all %d errors to be drained, got %d", numErrors, len(errors))
	}
}

// TestQueryAPIErrorResultUsesResultText verifies that a run ending on an API
// failure (subtype "success", is_error=true, empty errors[], prose in
// "result") surfaces that prose — never "returned an error result: success".
// Mirrors the Python test_api_error_result_uses_result_text_not_success_subtype.
func TestQueryAPIErrorResultUsesResultText(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- errorResultFrame(map[string]interface{}{
			"subtype":         "success",
			"errors":          []interface{}{},
			"result":          "API Error: Stream idle timeout - no chunks received",
			"terminal_reason": "api_error",
		})
		mockTrans.errCh <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
		mockTrans.Close()
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)
	_, errors := collectQuery(messages, errs)

	if len(errors) != 1 {
		t.Fatalf("Expected 1 error, got %d: %v", len(errors), errors)
	}
	err := errors[0]
	if !IsResultError(err) {
		t.Fatalf("Expected ResultError, got %T: %v", err, err)
	}
	text := err.Error()
	if !strings.Contains(text, "Claude Code returned an error result: API Error: Stream idle timeout - no chunks received") {
		t.Errorf("Expected API error prose, got: %v", err)
	}
	if strings.Contains(text, "error result: success") {
		t.Errorf("Must not fall back to the success subtype: %v", err)
	}
	re := err.(*ResultError)
	if re.Subtype != "success" {
		t.Errorf("Expected Subtype=success, got %q", re.Subtype)
	}
	if re.TerminalReason != "api_error" {
		t.Errorf("Expected TerminalReason=api_error, got %q", re.TerminalReason)
	}
	if re.Result != "API Error: Stream idle timeout - no chunks received" {
		t.Errorf("Expected Result prose, got %q", re.Result)
	}
	if len(re.Errors) != 0 {
		t.Errorf("Expected empty Errors, got %v", re.Errors)
	}
	if re.SessionID != "s" {
		t.Errorf("Expected SessionID=s, got %q", re.SessionID)
	}
}

// TestQueryAPIErrorResultWithoutTextUsesHTTPStatus verifies that when neither
// errors[] nor result carry text, the message falls back to the HTTP status.
// Mirrors the Python test_api_error_result_without_text_uses_http_status.
func TestQueryAPIErrorResultWithoutTextUsesHTTPStatus(t *testing.T) {
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockTrans, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockTrans.readCh <- errorResultFrame(map[string]interface{}{
			"subtype":          "success",
			"errors":           []interface{}{},
			"result":           "",
			"api_error_status": float64(529),
		})
		mockTrans.errCh <- &transport.ProcessError{Message: "Command failed with exit code 1", ExitCode: 1}
		mockTrans.Close()
	}()

	ctx := context.Background()
	messages, errs := Query(ctx, "Hello", nil)
	_, errors := collectQuery(messages, errs)

	if len(errors) != 1 {
		t.Fatalf("Expected 1 error, got %d: %v", len(errors), errors)
	}
	if !strings.Contains(errors[0].Error(), "Claude Code returned an error result: API error (HTTP 529)") {
		t.Errorf("Expected HTTP status fallback, got: %v", errors[0])
	}
}
