package claude

import (
	"testing"
)

// TestRateLimitEventHandling verifies that rate_limit_event and unknown
// message types don't crash the parser.
//
// CLI v2.1.45+ emits `rate_limit_event` messages when rate limit status changes
// for claude.ai subscription users. The Go SDK's message parser now handles
// this by returning nil for unknown message types, and the caller filters them out.
// This makes the SDK forward-compatible with new CLI message types.
//
// See: https://github.com/anthropics/claude-agent-sdk-python/issues/583

func TestRateLimitEventReturnsNil(t *testing.T) {
	// rate_limit_event should be parsed into a RateLimitEvent
	data := map[string]interface{}{
		"type": "rate_limit_event",
		"rate_limit_info": map[string]interface{}{
			"status":           "allowed_warning",
			"resetsAt":         1700000000.0,
			"rateLimitType":    "five_hour",
			"utilization":      0.85,
			"isUsingOverage":   false,
		},
		"uuid":       "550e8400-e29b-41d4-a716-446655440000",
		"session_id": "test-session-id",
	}

	result, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Expected no error for rate_limit_event, got: %v", err)
	}
	rle, ok := result.(*RateLimitEvent)
	if !ok {
		t.Fatalf("Expected *RateLimitEvent, got %T", result)
	}
	if rle.RateLimitInfo.Status != RateLimitStatusAllowedWarning {
		t.Errorf("Expected status 'allowed_warning', got '%s'", rle.RateLimitInfo.Status)
	}
	if rle.RateLimitInfo.RateLimitType != RateLimitTypeFiveHour {
		t.Errorf("Expected rate limit type 'five_hour', got '%s'", rle.RateLimitInfo.RateLimitType)
	}
	if rle.UUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("Expected UUID '550e8400-e29b-41d4-a716-446655440000', got '%s'", rle.UUID)
	}
}

func TestRateLimitEventRejectedReturnsNil(t *testing.T) {
	// Hard rate limit (status=rejected) should also be parsed
	data := map[string]interface{}{
		"type": "rate_limit_event",
		"rate_limit_info": map[string]interface{}{
			"status":                "rejected",
			"resetsAt":              1700003600.0,
			"rateLimitType":         "seven_day",
			"isUsingOverage":        false,
			"overageStatus":         "rejected",
			"overageDisabledReason": "out_of_credits",
		},
		"uuid":       "660e8400-e29b-41d4-a716-446655440001",
		"session_id": "test-session-id",
	}

	result, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Expected no error for rate_limit_event rejected, got: %v", err)
	}
	rle, ok := result.(*RateLimitEvent)
	if !ok {
		t.Fatalf("Expected *RateLimitEvent, got %T", result)
	}
	if rle.RateLimitInfo.Status != RateLimitStatusRejected {
		t.Errorf("Expected status 'rejected', got '%s'", rle.RateLimitInfo.Status)
	}
	if rle.RateLimitInfo.OverageStatus != "rejected" {
		t.Errorf("Expected overage status 'rejected', got '%s'", rle.RateLimitInfo.OverageStatus)
	}
}

func TestUnknownMessageTypeReturnsNil(t *testing.T) {
	// Any unknown message type should return nil for forward compatibility
	data := map[string]interface{}{
		"type":       "some_future_event_type",
		"uuid":       "770e8400-e29b-41d4-a716-446655440002",
		"session_id": "test-session-id",
	}

	result, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Expected no error for unknown message type, got: %v", err)
	}
	if result != nil {
		t.Error("Expected nil message for unknown message type")
	}
}

func TestKnownMessageTypesStillParsed(t *testing.T) {
	// Known message types should still be parsed normally
	data := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "hello",
				},
			},
			"model": "claude-sonnet-4-6-20250929",
		},
	}

	result, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Expected no error for known message type, got: %v", err)
	}
	if result == nil {
		t.Fatal("Expected non-nil message for known message type")
	}

	assistantMsg, ok := result.(*AssistantMessage)
	if !ok {
		t.Fatalf("Expected *AssistantMessage, got %T", result)
	}

	if len(assistantMsg.Content) == 0 {
		t.Fatal("Expected content in assistant message")
	}

	textBlock, ok := assistantMsg.Content[0].(*TextBlock)
	if !ok {
		t.Fatalf("Expected *TextBlock, got %T", assistantMsg.Content[0])
	}

	if textBlock.Text != "hello" {
		t.Errorf("Expected text 'hello', got '%s'", textBlock.Text)
	}
}

// TestRateLimitEventMinimalFields tests that only status is required.
func TestRateLimitEventMinimalFields(t *testing.T) {
	data := map[string]interface{}{
		"type": "rate_limit_event",
		"rate_limit_info": map[string]interface{}{
			"status": "allowed",
		},
		"uuid":       "minimal-uuid",
		"session_id": "minimal-session",
	}

	result, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	rle, ok := result.(*RateLimitEvent)
	if !ok {
		t.Fatalf("Expected *RateLimitEvent, got %T", result)
	}
	if rle.RateLimitInfo.Status != RateLimitStatusAllowed {
		t.Errorf("Expected status 'allowed', got '%s'", rle.RateLimitInfo.Status)
	}
	// Optional fields should be zero values
	if rle.RateLimitInfo.RateLimitType != "" {
		t.Errorf("Expected empty RateLimitType, got '%s'", rle.RateLimitInfo.RateLimitType)
	}
	if rle.RateLimitInfo.Utilization != nil {
		t.Errorf("Expected nil Utilization, got %v", rle.RateLimitInfo.Utilization)
	}
}
