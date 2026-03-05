package claude

import (
	"testing"
)

// TestSimpleHash tests the simpleHash function.
func TestSimpleHash(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "0",
		},
		{
			name:     "simple string",
			input:    "test",
			expected: "2487m",
		},
		{
			name:     "another string",
			input:    "hello",
			expected: "1n1e4y",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := simpleHash(tt.input)
			if result != tt.expected {
				t.Errorf("simpleHash(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSanitizePath tests the sanitizePath function.
func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple path",
			input:    "/Users/test/project",
			expected: "-Users-test-project",
		},
		{
			name:     "path with special chars",
			input:    "my-project@123",
			expected: "my-project-123",
		},
		{
			name:     "path with spaces",
			input:    "my project",
			expected: "my-project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizePath(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestValidateUUID tests the validateUUID function.
func TestValidateUUID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid lowercase UUID",
			input:    "550e8400-e29b-41d4-a716-446655440000",
			expected: true,
		},
		{
			name:     "valid uppercase UUID",
			input:    "550E8400-E29B-41D4-A716-446655440000",
			expected: true,
		},
		{
			name:     "mixed case UUID",
			input:    "550e8400-E29b-41d4-a716-446655440000",
			expected: true,
		},
		{
			name:     "invalid UUID - too short",
			input:    "550e8400-e29b",
			expected: false,
		},
		{
			name:     "invalid UUID - bad format",
			input:    "not-a-uuid",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateUUID(tt.input)
			if result != tt.expected {
				t.Errorf("validateUUID(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestExtractJSONStringField tests the extractJSONStringField function.
func TestExtractJSONStringField(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		key      string
		expected string
	}{
		{
			name:     "extract simple field",
			text:     `{"type":"user","message":"hello"}`,
			key:      "type",
			expected: "user",
		},
		{
			name:     "extract with space",
			text:     `{"type": "user","message":"hello"}`,
			key:      "type",
			expected: "user",
		},
		{
			name:     "extract nested field",
			text:     `{"cwd":"/path/to/dir"}`,
			key:      "cwd",
			expected: "/path/to/dir",
		},
		{
			name:     "key not found",
			text:     `{"type":"user"}`,
			key:      "missing",
			expected: "",
		},
		{
			name:     "empty text",
			text:     "",
			key:      "type",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractJSONStringField(tt.text, tt.key)
			if result != tt.expected {
				t.Errorf("extractJSONStringField(%q, %q) = %q, want %q", tt.text, tt.key, result, tt.expected)
			}
		})
	}
}

// TestExtractLastJSONStringField tests the extractLastJSONStringField function.
func TestExtractLastJSONStringField(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		key      string
		expected string
	}{
		{
			name:     "extract last occurrence",
			text:     `{"type":"user","summary":"first"}\n{"summary":"last"}`,
			key:      "summary",
			expected: "last",
		},
		{
			name:     "single occurrence",
			text:     `{"summary":"only"}`,
			key:      "summary",
			expected: "only",
		},
		{
			name:     "key not found",
			text:     `{"type":"user"}`,
			key:      "summary",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractLastJSONStringField(tt.text, tt.key)
			if result != tt.expected {
				t.Errorf("extractLastJSONStringField(%q, %q) = %q, want %q", tt.text, tt.key, result, tt.expected)
			}
		})
	}
}

// TestUnescapeJSONString tests the unescapeJSONString function.
func TestUnescapeJSONString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no escapes",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "with backslash",
			input:    `hello\\nworld`,
			expected: `hello\nworld`, // JSON unescapes \n to actual newline
		},
		{
			name:     "with quote",
			input:    `hello\"world`,
			expected: `hello"world`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unescapeJSONString(tt.input)
			if result != tt.expected {
				t.Errorf("unescapeJSONString(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSDKSessionInfo tests the SDKSessionInfo struct.
func TestSDKSessionInfo(t *testing.T) {
	customTitle := "Custom Session Title"
	firstPrompt := "First user prompt"
	gitBranch := "main"
	cwd := "/path/to/project"

	info := SDKSessionInfo{
		SessionID:    "550e8400-e29b-41d4-a716-446655440000",
		Summary:      "Session Summary",
		LastModified: 1234567890,
		FileSize:     1024,
		CustomTitle:  &customTitle,
		FirstPrompt:  &firstPrompt,
		GitBranch:    &gitBranch,
		CWD:          &cwd,
	}

	if info.SessionID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("SessionID mismatch")
	}
	if info.Summary != "Session Summary" {
		t.Errorf("Summary mismatch")
	}
	if info.CustomTitle == nil || *info.CustomTitle != "Custom Session Title" {
		t.Errorf("CustomTitle mismatch")
	}
	if info.FirstPrompt == nil || *info.FirstPrompt != "First user prompt" {
		t.Errorf("FirstPrompt mismatch")
	}
	if info.GitBranch == nil || *info.GitBranch != "main" {
		t.Errorf("GitBranch mismatch")
	}
	if info.CWD == nil || *info.CWD != "/path/to/project" {
		t.Errorf("CWD mismatch")
	}
}

// TestSessionMessage tests the SessionMessage struct.
func TestSessionMessage(t *testing.T) {
	parentToolUseID := "parent-uuid"

	msg := SessionMessage{
		Type:            SessionMessageTypeUser,
		UUID:            "550e8400-e29b-41d4-a716-446655440000",
		SessionID:       "session-123",
		Message:         map[string]interface{}{"content": "Hello"},
		ParentToolUseID: &parentToolUseID,
	}

	if msg.Type != SessionMessageTypeUser {
		t.Errorf("Type mismatch")
	}
	if msg.UUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("UUID mismatch")
	}
	if msg.ParentToolUseID == nil || *msg.ParentToolUseID != "parent-uuid" {
		t.Errorf("ParentToolUseID mismatch")
	}

	// Test nil ParentToolUseID
	msg2 := SessionMessage{
		Type:      SessionMessageTypeAssistant,
		UUID:      "660e8400-e29b-41d4-a716-446655440001",
		SessionID: "session-123",
		Message:   map[string]interface{}{"content": "Hi there"},
	}

	if msg2.ParentToolUseID != nil {
		t.Errorf("ParentToolUseID should be nil")
	}
}

// TestApplySortAndLimit tests the applySortAndLimit function.
func TestApplySortAndLimit(t *testing.T) {
	sessions := []SDKSessionInfo{
		{SessionID: "a", LastModified: 100, Summary: "A", FileSize: 1},
		{SessionID: "b", LastModified: 300, Summary: "B", FileSize: 1},
		{SessionID: "c", LastModified: 200, Summary: "C", FileSize: 1},
	}

	// Test sorting (no limit)
	result := applySortAndLimit(sessions, nil)
	if len(result) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(result))
	}
	if result[0].SessionID != "b" {
		t.Errorf("Expected first session to be 'b' (newest), got '%s'", result[0].SessionID)
	}
	if result[1].SessionID != "c" {
		t.Errorf("Expected second session to be 'c', got '%s'", result[1].SessionID)
	}
	if result[2].SessionID != "a" {
		t.Errorf("Expected third session to be 'a' (oldest), got '%s'", result[2].SessionID)
	}

	// Test with limit
	limitVal := 2
	result = applySortAndLimit(sessions, &limitVal)
	if len(result) != 2 {
		t.Errorf("Expected 2 sessions with limit=2, got %d", len(result))
	}
	if result[0].SessionID != "b" {
		t.Errorf("Expected first session to be 'b', got '%s'", result[0].SessionID)
	}
}

// TestDeduplicateBySessionID tests the deduplicateBySessionID function.
func TestDeduplicateBySessionID(t *testing.T) {
	sessions := []SDKSessionInfo{
		{SessionID: "a", LastModified: 100, Summary: "A-old", FileSize: 1},
		{SessionID: "b", LastModified: 200, Summary: "B", FileSize: 1},
		{SessionID: "a", LastModified: 150, Summary: "A-new", FileSize: 1},
		{SessionID: "c", LastModified: 300, Summary: "C", FileSize: 1},
	}

	result := deduplicateBySessionID(sessions)

	if len(result) != 3 {
		t.Errorf("Expected 3 unique sessions, got %d", len(result))
	}

	// Find session 'a' and verify it has the newest timestamp
	var sessionA *SDKSessionInfo
	for i := range result {
		if result[i].SessionID == "a" {
			sessionA = &result[i]
			break
		}
	}

	if sessionA == nil {
		t.Fatal("Session 'a' not found in result")
	}

	if sessionA.LastModified != 150 {
		t.Errorf("Expected session 'a' to have LastModified=150 (newest), got %d", sessionA.LastModified)
	}
	if sessionA.Summary != "A-new" {
		t.Errorf("Expected session 'a' to have Summary='A-new', got '%s'", sessionA.Summary)
	}
}
