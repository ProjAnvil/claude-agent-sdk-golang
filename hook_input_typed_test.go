package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// hookFrame builds a raw hook input frame with the shared base fields.
func hookFrame(event string, extra map[string]interface{}) map[string]interface{} {
	frame := map[string]interface{}{
		"hook_event_name": event,
		"session_id":      "s1",
		"transcript_path": "/tmp/t.jsonl",
		"cwd":             "/work",
		"permission_mode": "default",
		"prompt_id":       "p1",
		"effort":          map[string]interface{}{"level": "high"},
	}
	for k, v := range extra {
		frame[k] = v
	}
	return frame
}

// baseOf extracts the shared fields from a typed input through JSON, so the
// test does not depend on the concrete struct layout.
func baseOf(t *testing.T, typed TypedHookInput) BaseHookInput {
	t.Helper()
	raw, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("marshal typed input: %v", err)
	}
	var base BaseHookInput
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("unmarshal base: %v", err)
	}
	return base
}

// TestParseTypedHookInputEvents covers all ten per-event inputs from the
// Python SDK's HookInput union.
func TestParseTypedHookInputEvents(t *testing.T) {
	tests := []struct {
		event string
		extra map[string]interface{}
		want  TypedHookInput
	}{
		{
			event: "PreToolUse",
			extra: map[string]interface{}{"tool_name": "Bash", "tool_input": map[string]interface{}{"command": "ls"}, "tool_use_id": "tu1", "agent_id": "a1", "agent_type": "general-purpose"},
			want:  PreToolUseHookInput{},
		},
		{
			event: "PostToolUse",
			extra: map[string]interface{}{"tool_name": "Bash", "tool_input": map[string]interface{}{}, "tool_response": "ok", "tool_use_id": "tu1", "duration_ms": float64(12)},
			want:  PostToolUseHookInput{},
		},
		{
			event: "PostToolUseFailure",
			extra: map[string]interface{}{"tool_name": "Bash", "tool_input": map[string]interface{}{}, "tool_use_id": "tu1", "error": "boom", "is_interrupt": true, "duration_ms": float64(3)},
			want:  PostToolUseFailureHookInput{},
		},
		{
			event: "UserPromptSubmit",
			extra: map[string]interface{}{"prompt": "hello"},
			want:  UserPromptSubmitHookInput{},
		},
		{
			event: "Stop",
			extra: map[string]interface{}{
				"stop_hook_active": true, "last_assistant_message": "done",
				"background_tasks": []interface{}{map[string]interface{}{"id": "t1", "type": "shell", "status": "running", "description": "d"}},
				"session_crons":    []interface{}{map[string]interface{}{"id": "c1", "schedule": "0 9 * * *", "recurring": true, "prompt": "wake"}},
			},
			want: StopHookInput{},
		},
		{
			event: "SubagentStop",
			extra: map[string]interface{}{"stop_hook_active": false, "agent_id": "a1", "agent_transcript_path": "/tmp/a.jsonl", "agent_type": "code-reviewer"},
			want:  SubagentStopHookInput{},
		},
		{
			event: "PreCompact",
			extra: map[string]interface{}{"trigger": "auto", "custom_instructions": "keep summary"},
			want:  PreCompactHookInput{},
		},
		{
			event: "Notification",
			extra: map[string]interface{}{"message": "m", "title": "T", "notification_type": "info"},
			want:  NotificationHookInput{},
		},
		{
			event: "SubagentStart",
			extra: map[string]interface{}{"agent_id": "a1", "agent_type": "general-purpose"},
			want:  SubagentStartHookInput{},
		},
		{
			event: "PermissionRequest",
			extra: map[string]interface{}{"tool_name": "Bash", "tool_input": map[string]interface{}{}, "permission_suggestions": []interface{}{map[string]interface{}{"type": "addRules"}}},
			want:  PermissionRequestHookInput{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			got, err := ParseTypedHookInput(hookFrame(tt.event, tt.extra))
			if err != nil {
				t.Fatalf("ParseTypedHookInput failed: %v", err)
			}
			if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
				t.Fatalf("Got %T, want %T", got, tt.want)
			}
			base := baseOf(t, got)
			if base.SessionID != "s1" || base.TranscriptPath != "/tmp/t.jsonl" ||
				base.CWD != "/work" || base.PermissionMode != "default" || base.PromptID != "p1" {
				t.Errorf("Base fields not carried: %+v", base)
			}
			if base.Effort == nil || base.Effort.Level != EffortLevel("high") {
				t.Errorf("Effort not carried: %+v", base.Effort)
			}
		})
	}
}

// TestTypedHookInputEventSpecificFields verifies the discriminator and the
// event-only fields survive narrowing.
func TestTypedHookInputEventSpecificFields(t *testing.T) {
	t.Run("PreToolUse", func(t *testing.T) {
		got, err := ParseTypedHookInput(hookFrame("PreToolUse", map[string]interface{}{
			"tool_name": "Bash", "tool_input": map[string]interface{}{"command": "ls"},
			"tool_use_id": "tu1", "agent_id": "a1", "agent_type": "general-purpose",
		}))
		if err != nil {
			t.Fatal(err)
		}
		v, ok := got.(PreToolUseHookInput)
		if !ok {
			t.Fatalf("Got %T", got)
		}
		if v.HookEventName != HookEventPreToolUse || v.ToolName != "Bash" ||
			v.ToolUseID != "tu1" || v.AgentID != "a1" || v.AgentType != "general-purpose" {
			t.Errorf("Event fields wrong: %+v", v)
		}
		if v.ToolInput["command"] != "ls" {
			t.Errorf("ToolInput wrong: %v", v.ToolInput)
		}
	})

	t.Run("SubagentStop", func(t *testing.T) {
		got, err := ParseTypedHookInput(hookFrame("SubagentStop", map[string]interface{}{
			"stop_hook_active": true, "agent_id": "a1",
			"agent_transcript_path": "/tmp/a.jsonl", "agent_type": "code-reviewer",
			"last_assistant_message": "done",
		}))
		if err != nil {
			t.Fatal(err)
		}
		v, ok := got.(SubagentStopHookInput)
		if !ok {
			t.Fatalf("Got %T", got)
		}
		if v.HookEventName != HookEventSubagentStop || v.AgentID != "a1" ||
			v.AgentTranscriptPath != "/tmp/a.jsonl" || v.AgentType != "code-reviewer" ||
			v.LastAssistantMessage != "done" || !v.StopHookActive {
			t.Errorf("Event fields wrong: %+v", v)
		}
	})

	t.Run("Stop background tasks", func(t *testing.T) {
		got, err := ParseTypedHookInput(hookFrame("Stop", map[string]interface{}{
			"stop_hook_active": false,
			"background_tasks": []interface{}{map[string]interface{}{"id": "t1", "type": "shell", "status": "running", "description": "d"}},
			"session_crons":    []interface{}{map[string]interface{}{"id": "c1", "schedule": "0 9 * * *", "recurring": true, "prompt": "wake"}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		v, ok := got.(StopHookInput)
		if !ok {
			t.Fatalf("Got %T", got)
		}
		if len(v.BackgroundTasks) != 1 || v.BackgroundTasks[0].ID != "t1" {
			t.Errorf("BackgroundTasks wrong: %+v", v.BackgroundTasks)
		}
		if len(v.SessionCrons) != 1 || v.SessionCrons[0].Schedule != "0 9 * * *" {
			t.Errorf("SessionCrons wrong: %+v", v.SessionCrons)
		}
	})
}

// TestHookInputTypedMatchesParse keeps the two narrowing paths aligned.
func TestHookInputTypedMatchesParse(t *testing.T) {
	frame := hookFrame("Notification", map[string]interface{}{
		"message": "m", "notification_type": "info", "title": "T",
	})
	raw, _ := json.Marshal(frame)
	var flat HookInput
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatal(err)
	}
	fromFlat, err := flat.Typed()
	if err != nil {
		t.Fatal(err)
	}
	fromParse, err := ParseTypedHookInput(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromFlat, fromParse) {
		t.Errorf("Typed() = %+v, ParseTypedHookInput = %+v", fromFlat, fromParse)
	}
}

// TestHookInputTypedUnknownEvent rejects events with no typed input.
func TestHookInputTypedUnknownEvent(t *testing.T) {
	_, err := ParseTypedHookInput(hookFrame("SessionStart", nil))
	if err == nil {
		t.Fatal("Expected an error for an event with no typed input")
	}
	if !strings.Contains(err.Error(), "SessionStart") {
		t.Errorf("Expected the event name in the error, got %q", err.Error())
	}
}

// TestTypedHookCallback adapts a typed callback to HookCallback.
func TestTypedHookCallback(t *testing.T) {
	var seen TypedHookInput
	cb := TypedHookCallback(func(input TypedHookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		seen = input
		if toolUseID != "tu1" {
			t.Errorf("toolUseID = %q", toolUseID)
		}
		return HookOutput{}, nil
	})
	flat := HookInput{
		HookEventName: "PreToolUse", ToolName: "Bash",
		ToolInput: map[string]interface{}{}, ToolUseID: "tu1",
	}
	if _, err := cb(flat, "tu1", HookContext{}); err != nil {
		t.Fatalf("callback failed: %v", err)
	}
	if _, ok := seen.(PreToolUseHookInput); !ok {
		t.Errorf("Expected PreToolUseHookInput, got %T", seen)
	}

	called := false
	cb2 := TypedHookCallback(func(TypedHookInput, string, HookContext) (HookOutput, error) {
		called = true
		return HookOutput{}, nil
	})
	if _, err := cb2(HookInput{HookEventName: "SessionStart"}, "", HookContext{}); err == nil {
		t.Error("Expected an error for an untyped event")
	}
	if called {
		t.Error("The typed callback must not run for an untyped event")
	}
}

// TestAllTypedHookInputsImplementInterface guards the discriminated union.
func TestAllTypedHookInputsImplementInterface(t *testing.T) {
	inputs := []TypedHookInput{
		PreToolUseHookInput{}, PostToolUseHookInput{}, PostToolUseFailureHookInput{},
		UserPromptSubmitHookInput{}, StopHookInput{}, SubagentStopHookInput{},
		PreCompactHookInput{}, NotificationHookInput{}, SubagentStartHookInput{},
		PermissionRequestHookInput{},
	}
	if len(inputs) != 10 {
		t.Errorf("Expected 10 typed inputs, got %d", len(inputs))
	}
}
