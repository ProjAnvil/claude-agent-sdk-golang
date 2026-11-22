package claude

import (
	"reflect"
	"testing"
)

// TestParseUserMessageUUIDs mirrors Python's test_parse_user_message_uuids
// (v0.2.183, #1505): assistant, stream_event and result frames surface the
// answered user message uuids exactly as reported.
func TestParseUserMessageUUIDs(t *testing.T) {
	frames := []struct {
		name  string
		frame map[string]interface{}
	}{
		{
			name: "assistant",
			frame: map[string]interface{}{
				"type":    "assistant",
				"message": map[string]interface{}{"content": []interface{}{}, "model": "claude-opus-4-6"},
			},
		},
		{
			name: "stream_event",
			frame: map[string]interface{}{
				"type":       "stream_event",
				"uuid":       "event-1",
				"session_id": "session_123",
				"event":      map[string]interface{}{"type": "message_start"},
			},
		},
		{
			name: "result_success",
			frame: map[string]interface{}{
				"type": "result", "subtype": "success",
				"duration_ms": float64(1000), "duration_api_ms": float64(500),
				"is_error": false, "num_turns": float64(2), "session_id": "session_123",
			},
		},
		{
			name: "result_error",
			frame: map[string]interface{}{
				"type": "result", "subtype": "error_during_execution",
				"duration_ms": float64(1000), "duration_api_ms": float64(500),
				"is_error": true, "num_turns": float64(2), "session_id": "session_123",
			},
		},
	}
	cases := []struct {
		name   string
		single string
		list   []string
	}{
		{name: "absent"},
		{name: "single-only", single: "user-1"},
		{name: "merged", single: "user-2", list: []string{"user-1", "user-2"}},
	}

	for _, f := range frames {
		for _, c := range cases {
			t.Run(f.name+"/"+c.name, func(t *testing.T) {
				data := make(map[string]interface{}, len(f.frame)+2)
				for k, v := range f.frame {
					data[k] = v
				}
				if c.single != "" {
					data["user_message_uuid"] = c.single
				}
				if c.list != nil {
					data["user_message_uuids"] = c.list
				}

				msg, err := ParseMessage(data)
				if err != nil {
					t.Fatalf("ParseMessage failed: %v", err)
				}
				single, list := userMessageUUIDsOf(msg)
				if single != c.single {
					t.Errorf("UserMessageUUID = %q, want %q", single, c.single)
				}
				if !reflect.DeepEqual(list, c.list) {
					t.Errorf("UserMessageUUIDs = %v, want %v", list, c.list)
				}
			})
		}
	}
}

// userMessageUUIDsOf reads the two fields from the three message types that
// carry them.
func userMessageUUIDsOf(msg Message) (string, []string) {
	switch m := msg.(type) {
	case *AssistantMessage:
		return m.UserMessageUUID, m.UserMessageUUIDs
	case *ResultMessage:
		return m.UserMessageUUID, m.UserMessageUUIDs
	case *StreamEvent:
		return m.UserMessageUUID, m.UserMessageUUIDs
	default:
		return "", nil
	}
}

// TestParseResultMessageEmptyUserMessageUUIDs mirrors Python's
// test_parse_result_message_empty_user_message_uuids: an empty list stays
// distinguishable from an absent one.
func TestParseResultMessageEmptyUserMessageUUIDs(t *testing.T) {
	msg, err := ParseMessage(map[string]interface{}{
		"type": "result", "subtype": "error_during_execution",
		"duration_ms": float64(1000), "duration_api_ms": float64(500),
		"is_error": true, "num_turns": float64(2), "session_id": "session_123",
		"user_message_uuids": []interface{}{},
	})
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	result, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("Expected *ResultMessage, got %T", msg)
	}
	if result.UserMessageUUID != "" {
		t.Errorf("Expected UserMessageUUID to be empty, got %q", result.UserMessageUUID)
	}
	if result.UserMessageUUIDs == nil || len(result.UserMessageUUIDs) != 0 {
		t.Errorf("Expected a non-nil empty UserMessageUUIDs slice, got %v", result.UserMessageUUIDs)
	}
}
