package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// errors.go Error() methods
// ---------------------------------------------------------------------------

func TestClaudeSDKErrorErrorMethod(t *testing.T) {
	withCause := NewClaudeSDKError("base message", errors.New("root cause"))
	if got := withCause.Error(); got != "base message: root cause" {
		t.Errorf("with cause: got %q", got)
	}
	if !errors.Is(withCause, withCause.Cause) {
		t.Error("Unwrap should expose the cause")
	}

	withoutCause := NewClaudeSDKError("base message", nil)
	if got := withoutCause.Error(); got != "base message" {
		t.Errorf("without cause: got %q", got)
	}
}

func TestCLINotFoundErrorErrorMethod(t *testing.T) {
	withPath := NewCLINotFoundError("/custom/claude")
	if got := withPath.Error(); !strings.HasSuffix(got, ": /custom/claude") {
		t.Errorf("with path: got %q", got)
	}

	withoutPath := NewCLINotFoundError("")
	if got := withoutPath.Error(); strings.HasSuffix(got, ": ") || !strings.Contains(got, "Claude Code not found") {
		t.Errorf("without path: got %q", got)
	}
}

// ---------------------------------------------------------------------------
// session_summary.go helpers
// ---------------------------------------------------------------------------

func TestExtractInt64Coverage(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]interface{}
		key  string
		want int64
	}{
		{"missing key", map[string]interface{}{}, "ts", 0},
		{"int64 value", map[string]interface{}{"ts": int64(42)}, "ts", 42},
		{"float64 value", map[string]interface{}{"ts": float64(42.9)}, "ts", 42},
		{"int value", map[string]interface{}{"ts": 42}, "ts", 42},
		{"string value", map[string]interface{}{"ts": "42"}, "ts", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractInt64(tt.m, tt.key); got != tt.want {
				t.Errorf("extractInt64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExtractTimestampFieldCoverage(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]interface{}
		want int64
	}{
		{"RFC3339Nano", map[string]interface{}{"ts": "2024-01-02T03:04:05.123456789Z"}, 1704164645123},
		{"RFC3339", map[string]interface{}{"ts": "2024-01-02T03:04:05Z"}, 1704164645000},
		{"invalid format", map[string]interface{}{"ts": "not-a-time"}, 0},
		{"empty string", map[string]interface{}{"ts": ""}, 0},
		{"non-string", map[string]interface{}{"ts": 123}, 0},
		{"missing key", map[string]interface{}{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTimestampField(tt.m, "ts"); got != tt.want {
				t.Errorf("extractTimestampField() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// warnings.go containsString
// ---------------------------------------------------------------------------

func TestContainsStringCoverage(t *testing.T) {
	if !containsString([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for present element")
	}
	if containsString([]string{"a", "b", "c"}, "z") {
		t.Error("expected false for absent element")
	}
	if containsString(nil, "a") {
		t.Error("expected false for nil slice")
	}
}

// ---------------------------------------------------------------------------
// session_store.go GetEntries
// ---------------------------------------------------------------------------

func TestInMemorySessionStoreGetEntries(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()

	key := SessionKey{ProjectKey: "proj", SessionID: "550e8400-e29b-41d4-a716-446655440000"}
	entries := []SessionStoreEntry{
		{"type": "user", "uuid": "u1"},
		{"type": "assistant", "uuid": "u2"},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got := store.GetEntries(key)
	if len(got) != 2 {
		t.Fatalf("GetEntries returned %d entries, want 2", len(got))
	}
	if got[0]["uuid"] != "u1" || got[1]["uuid"] != "u2" {
		t.Errorf("GetEntries returned wrong entries: %v", got)
	}

	// Appending to the returned slice must not affect the store (the slice
	// header is a copy even though entry maps are shared).
	got = append(got, SessionStoreEntry{"uuid": "extra"})
	if again := store.GetEntries(key); len(again) != 2 {
		t.Errorf("GetEntries should return a copy of the slice, store now has %d entries", len(again))
	}

	// Unknown key returns empty.
	if empty := store.GetEntries(SessionKey{ProjectKey: "proj", SessionID: "missing"}); len(empty) != 0 {
		t.Errorf("GetEntries for unknown key returned %d entries, want 0", len(empty))
	}

	// Subpath keys are stored independently.
	subKey := SessionKey{ProjectKey: "proj", SessionID: key.SessionID, Subpath: "subagents/agent-1"}
	if err := store.Append(ctx, subKey, []SessionStoreEntry{{"type": "user", "uuid": "s1"}}); err != nil {
		t.Fatalf("Append subpath failed: %v", err)
	}
	if got := store.GetEntries(subKey); len(got) != 1 || got[0]["uuid"] != "s1" {
		t.Errorf("GetEntries for subpath key returned %v", got)
	}
}

// ---------------------------------------------------------------------------
// parser.go task lifecycle messages + parseIntField
// ---------------------------------------------------------------------------

func TestParseIntFieldCoverage(t *testing.T) {
	m := map[string]interface{}{
		"float": float64(7.9),
		"int":   7,
		"str":   "7",
	}
	if got := parseIntField(m, "float"); got != 7 {
		t.Errorf("float64: got %d", got)
	}
	if got := parseIntField(m, "int"); got != 7 {
		t.Errorf("int: got %d", got)
	}
	if got := parseIntField(m, "str"); got != 0 {
		t.Errorf("string: got %d", got)
	}
	if got := parseIntField(m, "missing"); got != 0 {
		t.Errorf("missing: got %d", got)
	}
}

func TestParseTaskStartedMessageCoverage(t *testing.T) {
	valid := map[string]interface{}{
		"type":        "system",
		"subtype":     "task_started",
		"task_id":     "task-1",
		"description": "doing work",
		"uuid":        "uuid-1",
		"session_id":  "sess-1",
		"tool_use_id": "tool-1",
		"task_type":   "local_agent",
	}
	msg, err := ParseMessage(valid)
	if err != nil {
		t.Fatalf("valid task_started: %v", err)
	}
	started, ok := msg.(*TaskStartedMessage)
	if !ok {
		t.Fatalf("expected *TaskStartedMessage, got %T", msg)
	}
	if started.TaskID != "task-1" || started.ToolUseID != "tool-1" || started.TaskType != "local_agent" {
		t.Errorf("unexpected fields: %+v", started)
	}

	// Each required field missing must produce a parse error.
	for _, field := range []string{"task_id", "description", "uuid", "session_id"} {
		broken := map[string]interface{}{"type": "system", "subtype": "task_started"}
		for k, v := range valid {
			if k != field {
				broken[k] = v
			}
		}
		if _, err := ParseMessage(broken); err == nil {
			t.Errorf("missing %s: expected error", field)
		}
	}
}

func TestParseTaskProgressMessageCoverage(t *testing.T) {
	valid := map[string]interface{}{
		"type":        "system",
		"subtype":     "task_progress",
		"task_id":     "task-1",
		"description": "halfway",
		"uuid":        "uuid-1",
		"session_id":  "sess-1",
		"usage": map[string]interface{}{
			"total_tokens": float64(100),
			"tool_uses":    float64(3),
			"duration_ms":  float64(500),
		},
		"tool_use_id":    "tool-1",
		"last_tool_name": "Bash",
	}
	msg, err := ParseMessage(valid)
	if err != nil {
		t.Fatalf("valid task_progress: %v", err)
	}
	progress, ok := msg.(*TaskProgressMessage)
	if !ok {
		t.Fatalf("expected *TaskProgressMessage, got %T", msg)
	}
	if progress.Usage.TotalTokens != 100 || progress.Usage.ToolUses != 3 || progress.Usage.DurationMS != 500 {
		t.Errorf("unexpected usage: %+v", progress.Usage)
	}
	if progress.LastToolName != "Bash" {
		t.Errorf("unexpected last tool name: %q", progress.LastToolName)
	}

	for _, field := range []string{"task_id", "description", "uuid", "session_id", "usage"} {
		broken := map[string]interface{}{"type": "system", "subtype": "task_progress"}
		for k, v := range valid {
			if k != field {
				broken[k] = v
			}
		}
		if _, err := ParseMessage(broken); err == nil {
			t.Errorf("missing %s: expected error", field)
		}
	}
}

func TestParseTaskNotificationMessageCoverage(t *testing.T) {
	valid := map[string]interface{}{
		"type":        "system",
		"subtype":     "task_notification",
		"task_id":     "task-1",
		"status":      "completed",
		"output_file": "/tmp/out.txt",
		"summary":     "done",
		"uuid":        "uuid-1",
		"session_id":  "sess-1",
		"tool_use_id": "tool-1",
		"usage": map[string]interface{}{
			"total_tokens": float64(10),
			"tool_uses":    float64(1),
			"duration_ms":  float64(20),
		},
	}
	msg, err := ParseMessage(valid)
	if err != nil {
		t.Fatalf("valid task_notification: %v", err)
	}
	notif, ok := msg.(*TaskNotificationMessage)
	if !ok {
		t.Fatalf("expected *TaskNotificationMessage, got %T", msg)
	}
	if notif.Status != TaskNotificationStatusCompleted {
		t.Errorf("unexpected status: %q", notif.Status)
	}
	if notif.Usage == nil || notif.Usage.TotalTokens != 10 {
		t.Errorf("unexpected usage: %+v", notif.Usage)
	}

	// Without usage the optional field stays nil.
	noUsage := map[string]interface{}{}
	for k, v := range valid {
		if k != "usage" {
			noUsage[k] = v
		}
	}
	msg, err = ParseMessage(noUsage)
	if err != nil {
		t.Fatalf("task_notification without usage: %v", err)
	}
	if notif := msg.(*TaskNotificationMessage); notif.Usage != nil {
		t.Errorf("expected nil usage, got %+v", notif.Usage)
	}

	for _, field := range []string{"task_id", "status", "output_file", "summary", "uuid", "session_id"} {
		broken := map[string]interface{}{"type": "system", "subtype": "task_notification"}
		for k, v := range valid {
			if k != field {
				broken[k] = v
			}
		}
		if _, err := ParseMessage(broken); err == nil {
			t.Errorf("missing %s: expected error", field)
		}
	}
}

// ---------------------------------------------------------------------------
// Second batch: remaining parser error/edge branches
// ---------------------------------------------------------------------------

func TestParseResultMessageCoverage(t *testing.T) {
	valid := map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1),
		"duration_api_ms": float64(1),
		"is_error":        false,
		"num_turns":       float64(1),
		"session_id":      "s1",
	}
	for _, field := range []string{"subtype", "duration_ms", "duration_api_ms", "is_error", "num_turns", "session_id"} {
		broken := map[string]interface{}{"type": "result"}
		for k, v := range valid {
			if k != field {
				broken[k] = v
			}
		}
		if _, err := ParseMessage(broken); err == nil {
			t.Errorf("missing %s: expected error", field)
		}
	}

	// Optional extras: structured output, modelUsage (with an unmarshalable
	// entry that is skipped), deferred tool use, api error status, errors.
	full := map[string]interface{}{
		"type":               "result",
		"subtype":            "error_during_execution",
		"duration_ms":        float64(1),
		"duration_api_ms":    float64(1),
		"is_error":           true,
		"num_turns":          float64(2),
		"session_id":         "s1",
		"stop_reason":        "error",
		"total_cost_usd":     0.5,
		"usage":              map[string]interface{}{"in": 1},
		"result":             "oops",
		"structured_output":  map[string]interface{}{"k": "v"},
		"permission_denials": []interface{}{"denied"},
		"errors":             []interface{}{"e1", 42, "e2"},
		"modelUsage": map[string]interface{}{
			"claude-opus": map[string]interface{}{"inputTokens": 10, "outputTokens": 5},
			"bad":         func() {}, // cannot marshal: skipped
		},
		"deferred_tool_use": map[string]interface{}{"id": "d1", "name": "Bash", "input": map[string]interface{}{"cmd": "ls"}},
		"api_error_status":  float64(500),
		"uuid":              "uuid-1",
		"terminal_reason":   "completed",
	}
	msg, err := ParseMessage(full)
	if err != nil {
		t.Fatalf("full result: %v", err)
	}
	result, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("expected *ResultMessage, got %T", msg)
	}
	if result.StructuredOutput == nil || len(result.Errors) != 2 || result.APIErrorStatus == nil || *result.APIErrorStatus != 500 {
		t.Errorf("unexpected optional fields: %+v", result)
	}
	if result.DeferredToolUse == nil || result.DeferredToolUse.Name != "Bash" {
		t.Errorf("unexpected deferred tool use: %+v", result.DeferredToolUse)
	}
	if len(result.ModelUsage) != 1 {
		t.Errorf("modelUsage should skip the unmarshalable entry: %+v", result.ModelUsage)
	} else if mu := result.ModelUsage["claude-opus"]; mu.InputTokens != 10 || mu.OutputTokens != 5 {
		t.Errorf("unexpected model usage: %+v", mu)
	}
}

func TestParseStreamEventCoverage(t *testing.T) {
	valid := map[string]interface{}{
		"type":               "stream_event",
		"uuid":               "u1",
		"session_id":         "s1",
		"event":              map[string]interface{}{"type": "content_block_delta"},
		"parent_tool_use_id": "pt-1",
	}
	msg, err := ParseMessage(valid)
	if err != nil {
		t.Fatalf("valid stream_event: %v", err)
	}
	ev, ok := msg.(*StreamEvent)
	if !ok {
		t.Fatalf("expected *StreamEvent, got %T", msg)
	}
	if ev.UUID != "u1" || ev.SessionID != "s1" || ev.ParentToolUseID != "pt-1" {
		t.Errorf("unexpected event: %+v", ev)
	}

	for _, field := range []string{"uuid", "session_id", "event"} {
		broken := map[string]interface{}{"type": "stream_event"}
		for k, v := range valid {
			if k != field {
				broken[k] = v
			}
		}
		if _, err := ParseMessage(broken); err == nil {
			t.Errorf("missing %s: expected error", field)
		}
	}
}

func TestParseRateLimitEventCoverage(t *testing.T) {
	valid := map[string]interface{}{
		"type": "rate_limit_event",
		"rate_limit_info": map[string]interface{}{
			"status":                "allowed_warning",
			"resetsAt":              float64(1700000000000),
			"rateLimitType":         "five_hour",
			"utilization":           float64(0.9),
			"overageStatus":         "rejected",
			"overageResetsAt":       float64(1700000001000),
			"overageDisabledReason": "none",
		},
		"uuid":       "u1",
		"session_id": "s1",
	}
	msg, err := ParseMessage(valid)
	if err != nil {
		t.Fatalf("valid rate_limit_event: %v", err)
	}
	ev, ok := msg.(*RateLimitEvent)
	if !ok {
		t.Fatalf("expected *RateLimitEvent, got %T", msg)
	}
	info := ev.RateLimitInfo
	if info.Status != RateLimitStatusAllowedWarning || info.RateLimitType != RateLimitTypeFiveHour {
		t.Errorf("unexpected info: %+v", info)
	}
	if info.ResetsAt == nil || *info.ResetsAt != 1700000000000 {
		t.Errorf("unexpected resetsAt: %+v", info.ResetsAt)
	}
	if info.Utilization == nil || *info.Utilization != 0.9 {
		t.Errorf("unexpected utilization: %+v", info.Utilization)
	}
	if info.OverageStatus != RateLimitStatusRejected || info.OverageResetsAt == nil || info.OverageDisabledReason != "none" {
		t.Errorf("unexpected overage fields: %+v", info)
	}

	// Missing rate_limit_info / status / uuid / session_id all error.
	broken := map[string]interface{}{"type": "rate_limit_event", "uuid": "u", "session_id": "s"}
	if _, err := ParseMessage(broken); err == nil {
		t.Error("missing rate_limit_info: expected error")
	}
	broken = map[string]interface{}{"type": "rate_limit_event", "rate_limit_info": map[string]interface{}{}, "uuid": "u", "session_id": "s"}
	if _, err := ParseMessage(broken); err == nil {
		t.Error("missing status: expected error")
	}
	broken = map[string]interface{}{"type": "rate_limit_event", "rate_limit_info": map[string]interface{}{"status": "allowed"}, "session_id": "s"}
	if _, err := ParseMessage(broken); err == nil {
		t.Error("missing uuid: expected error")
	}
	broken = map[string]interface{}{"type": "rate_limit_event", "rate_limit_info": map[string]interface{}{"status": "allowed"}, "uuid": "u"}
	if _, err := ParseMessage(broken); err == nil {
		t.Error("missing session_id: expected error")
	}
}

func TestParseUserAndAssistantMessageErrors(t *testing.T) {
	// user message without a dict "message" field.
	if _, err := ParseMessage(map[string]interface{}{"type": "user", "message": "bad"}); err == nil {
		t.Error("user: expected error for non-dict message")
	}
	// user message with content blocks containing a malformed block.
	_, err := ParseMessage(map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"content": []interface{}{"not-a-dict"}},
	})
	if err == nil {
		t.Error("user: expected error for malformed content block")
	}
	// assistant without a dict "message" field.
	if _, err := ParseMessage(map[string]interface{}{"type": "assistant", "message": "bad"}); err == nil {
		t.Error("assistant: expected error for non-dict message")
	}
	// assistant without a content list.
	if _, err := ParseMessage(map[string]interface{}{
		"type":    "assistant",
		"message": map[string]interface{}{"model": "m"},
	}); err == nil {
		t.Error("assistant: expected error for missing content")
	}
}

// ---------------------------------------------------------------------------
// Second batch: FoldSessionSummary branches + session_store helpers
// ---------------------------------------------------------------------------

func TestFoldSessionSummaryBranches(t *testing.T) {
	// Numeric (non-string) timestamp fields also advance the mtime.
	out := FoldSessionSummary(nil, SessionKey{SessionID: "s"}, []SessionStoreEntry{
		{"type": "user", "timestamp": float64(5000)},
	})
	if out.Mtime != 5000 {
		t.Errorf("numeric timestamp mtime: got %d", out.Mtime)
	}

	// isSidechain is recorded once, from the first entry carrying it.
	out = FoldSessionSummary(nil, SessionKey{SessionID: "s"}, []SessionStoreEntry{
		{"type": "user", "isSidechain": true},
		{"type": "user", "isSidechain": false},
	})
	if v, _ := out.Data["is_sidechain"].(bool); !v {
		t.Errorf("is_sidechain: got %v", out.Data)
	}

	// Last-wins metadata fields are folded in.
	out = FoldSessionSummary(nil, SessionKey{SessionID: "s"}, []SessionStoreEntry{
		{"type": "assistant", "aiTitle": "AI", "lastPrompt": "lp", "summary": "sh", "gitBranch": "main"},
	})
	if out.Data["ai_title"] != "AI" || out.Data["last_prompt"] != "lp" ||
		out.Data["summary_hint"] != "sh" || out.Data["git_branch"] != "main" {
		t.Errorf("last-wins fields: %v", out.Data)
	}

	// Tag set then cleared by an empty tag entry.
	out = FoldSessionSummary(nil, SessionKey{SessionID: "s"}, []SessionStoreEntry{
		{"type": "tag", "tag": "v1"},
		{"type": "tag", "tag": ""},
	})
	if _, ok := out.Data["tag"]; ok {
		t.Errorf("tag should be cleared: %v", out.Data)
	}
}

func TestUint32ToHexZero(t *testing.T) {
	if got := uint32ToHex(0); got != "0" {
		t.Errorf("got %q, want %q", got, "0")
	}
	if got := uint32ToHex(255); got != "ff" {
		t.Errorf("got %q, want %q", got, "ff")
	}
}

func TestFilePathToSessionKeyEdges(t *testing.T) {
	projectsDir := t.TempDir()

	// Not a .jsonl file.
	if key := FilePathToSessionKey(filepath.Join(projectsDir, "proj", "notes.txt"), projectsDir); key != nil {
		t.Errorf("non-jsonl: got %+v", key)
	}

	// Session file name is not a UUID.
	if key := FilePathToSessionKey(filepath.Join(projectsDir, "proj", "session.jsonl"), projectsDir); key != nil {
		t.Errorf("non-uuid: got %+v", key)
	}

	// Valid layout.
	sid := "550e8400-e29b-41d4-a716-446655440000"
	key := FilePathToSessionKey(filepath.Join(projectsDir, "proj-a", sid+".jsonl"), projectsDir)
	if key == nil {
		t.Fatal("expected key")
	}
	if key.ProjectKey != "proj-a" || key.SessionID != sid || key.Subpath != "" {
		t.Errorf("unexpected key: %+v", key)
	}
}

func TestListSessionSummariesSkipsSubkeys(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	if err := store.Append(ctx, SessionKey{ProjectKey: "p", SessionID: sid}, []SessionStoreEntry{
		{"type": "user", "message": map[string]interface{}{"content": "main"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, SessionKey{ProjectKey: "p", SessionID: sid, Subpath: "subagents/agent-1"}, []SessionStoreEntry{
		{"type": "user", "message": map[string]interface{}{"content": "sub"}},
	}); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.ListSessionSummaries(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].SessionID != sid {
		t.Errorf("subkey summaries should be excluded: %+v", summaries)
	}

	// Subpath entries are also excluded from ListSessions.
	listing, err := store.ListSessions(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing) != 1 || listing[0].SessionID != sid {
		t.Errorf("subkey listings should be excluded: %+v", listing)
	}
}

// ---------------------------------------------------------------------------
// Third batch: remaining parser branches
// ---------------------------------------------------------------------------

func TestParseUserMessageContentVariants(t *testing.T) {
	// Non-string, non-list content passes through as-is.
	msg, err := ParseMessage(map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"content": float64(42)},
	})
	if err != nil {
		t.Fatalf("numeric content: %v", err)
	}
	user, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("expected *UserMessage, got %T", msg)
	}
	if user.Content != float64(42) {
		t.Errorf("unexpected content: %v", user.Content)
	}

	// Assistant message with a malformed content block errors out.
	if _, err := ParseMessage(map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": []interface{}{"not-a-dict"},
		},
	}); err == nil {
		t.Error("expected error for malformed assistant content block")
	}
}

func TestParseResultMessageModelUsageBadEntry(t *testing.T) {
	msg, err := ParseMessage(map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1),
		"duration_api_ms": float64(1),
		"is_error":        false,
		"num_turns":       float64(1),
		"session_id":      "s1",
		"modelUsage": map[string]interface{}{
			// Marshals fine but does not fit the ModelUsage shape.
			"bad": map[string]interface{}{"inputTokens": "not-a-number"},
		},
	})
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	result := msg.(*ResultMessage)
	if len(result.ModelUsage) != 0 {
		t.Errorf("malformed modelUsage entry should be dropped: %+v", result.ModelUsage)
	}
}

func TestParseAssistantMessageTopLevelError(t *testing.T) {
	msg, err := ParseMessage(map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"model":   "claude-opus-4-1-20250805",
			"error":   "rate_limit",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "x"}},
		},
	})
	if err != nil {
		t.Fatalf("assistant with error: %v", err)
	}
	assistant := msg.(*AssistantMessage)
	if assistant.Error != "rate_limit" {
		t.Errorf("unexpected error field: %q", assistant.Error)
	}
}

func TestListAllSessionsSkipsFiles(t *testing.T) {
	_, _, projectDir := setupSessionTestProject(t)
	projectsDir := filepath.Dir(projectDir)
	if err := os.WriteFile(filepath.Join(projectsDir, "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessions(nil)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	_ = sessions // the stray file is skipped without error
}
