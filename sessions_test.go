package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func int64Ptr(v int64) *int64 {
	return &v
}

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
		FileSize:     int64Ptr(1024),
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

// TestApplySortAndLimit tests the applySortLimitOffset function.
func TestApplySortAndLimit(t *testing.T) {
	sessions := []SDKSessionInfo{
		{SessionID: "a", LastModified: 100, Summary: "A", FileSize: int64Ptr(1)},
		{SessionID: "b", LastModified: 300, Summary: "B", FileSize: int64Ptr(1)},
		{SessionID: "c", LastModified: 200, Summary: "C", FileSize: int64Ptr(1)},
	}

	// Test sorting (no limit)
	result := applySortLimitOffset(sessions, nil, 0)
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
	result = applySortLimitOffset(sessions, &limitVal, 0)
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
		{SessionID: "a", LastModified: 100, Summary: "A-old", FileSize: int64Ptr(1)},
		{SessionID: "b", LastModified: 200, Summary: "B", FileSize: int64Ptr(1)},
		{SessionID: "a", LastModified: 150, Summary: "A-new", FileSize: int64Ptr(1)},
		{SessionID: "c", LastModified: 300, Summary: "C", FileSize: int64Ptr(1)},
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

// ---------------------------------------------------------------------------
// Test helpers for session file tests
// ---------------------------------------------------------------------------

// setupSessionTestProject creates a temp CLAUDE_CONFIG_DIR and project directory.
func setupSessionTestProject(t *testing.T) (string, string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir) // macOS /var → /private/var
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	projectPath := filepath.Join(tmpDir, "proj")
	os.MkdirAll(projectPath, 0755)

	projectDir := filepath.Join(projectsDir, sanitizePath(projectPath))
	os.MkdirAll(projectDir, 0755)

	return configDir, projectPath, projectDir
}

// makeTestSessionFile creates a .jsonl session file with configurable content.
// Returns (sessionID, filePath).
func makeTestSessionFile(t *testing.T, dir string, opts ...func(*sessionFileOpts)) (string, string) {
	t.Helper()
	o := &sessionFileOpts{}
	for _, fn := range opts {
		fn(o)
	}

	sid := generateUUID()
	lines := []string{}

	userMsg := map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"content": "hello"},
	}
	if o.firstPrompt != "" {
		userMsg["message"] = map[string]interface{}{"content": o.firstPrompt}
	}
	if o.timestamp != "" {
		userMsg["timestamp"] = o.timestamp
	}
	if o.cwd != "" {
		userMsg["cwd"] = o.cwd
	}
	if o.isSidechain {
		userMsg["isSidechain"] = true
	}
	b, _ := json.Marshal(userMsg)
	lines = append(lines, string(b))

	if o.gitBranch != "" {
		entry := map[string]interface{}{
			"type":      "assistant",
			"message":   map[string]interface{}{"content": "ok"},
			"gitBranch": o.gitBranch,
		}
		b, _ := json.Marshal(entry)
		lines = append(lines, string(b))
	}

	for _, extra := range o.extraLines {
		lines = append(lines, extra)
	}

	fp := filepath.Join(dir, sid+".jsonl")
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	if o.mtime > 0 {
		os.Chtimes(fp, time.Unix(int64(o.mtime), 0), time.Unix(int64(o.mtime), 0))
	}

	return sid, fp
}

type sessionFileOpts struct {
	firstPrompt string
	timestamp   string
	cwd         string
	gitBranch   string
	isSidechain bool
	mtime       float64
	extraLines  []string
}

func withFirstPrompt(p string) func(*sessionFileOpts) {
	return func(o *sessionFileOpts) { o.firstPrompt = p }
}
func withTimestamp(ts string) func(*sessionFileOpts) {
	return func(o *sessionFileOpts) { o.timestamp = ts }
}
func withMtime(mt float64) func(*sessionFileOpts) {
	return func(o *sessionFileOpts) { o.mtime = mt }
}
func withGitBranch(gb string) func(*sessionFileOpts) {
	return func(o *sessionFileOpts) { o.gitBranch = gb }
}
func withSidechain() func(*sessionFileOpts) {
	return func(o *sessionFileOpts) { o.isSidechain = true }
}
func withExtraLines(lines ...string) func(*sessionFileOpts) {
	return func(o *sessionFileOpts) { o.extraLines = lines }
}

// compactJSON marshals with no whitespace (Go default is already compact).
func compactJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------------------
// TestOffsetPagination
// ---------------------------------------------------------------------------

func TestOffsetPagination(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	for i := 0; i < 5; i++ {
		makeTestSessionFile(t, projectDir,
			withFirstPrompt(fmt.Sprintf("prompt %d", i)),
			withMtime(1000+float64(i)),
		)
	}

	limit2 := 2
	page1, err := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
		Limit:     &limit2,
		Offset:    0,
	})
	if err != nil {
		t.Fatalf("ListSessions page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("Expected 2 sessions in page1, got %d", len(page1))
	}

	page2, err := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
		Limit:     &limit2,
		Offset:    2,
	})
	if err != nil {
		t.Fatalf("ListSessions page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("Expected 2 sessions in page2, got %d", len(page2))
	}

	// Pages should be disjoint
	page1IDs := map[string]bool{page1[0].SessionID: true, page1[1].SessionID: true}
	for _, s := range page2 {
		if page1IDs[s.SessionID] {
			t.Error("Page 1 and page 2 should have different sessions")
		}
	}

	// Page 1 should be newer
	if page1[0].LastModified <= page2[0].LastModified {
		t.Error("Page 1 should contain newer sessions than page 2")
	}

	// Offset beyond available returns empty
	pageEmpty, err := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
		Offset:    100,
	})
	if err != nil {
		t.Fatalf("ListSessions offset=100: %v", err)
	}
	if len(pageEmpty) != 0 {
		t.Errorf("Expected empty result for large offset, got %d", len(pageEmpty))
	}
}

// ---------------------------------------------------------------------------
// TestTagExtraction
// ---------------------------------------------------------------------------

// tagJSON builds a raw tag line with {"type":"tag" first, matching CLI output.
func tagJSON(tag, sid string) string {
	return fmt.Sprintf(`{"type":"tag","tag":"%s","sessionId":"%s"}`, tag, sid)
}

func TestTagExtraction_FromTail(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "hello"}}),
		tagJSON("my-tag", sid),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Tag == nil || *sessions[0].Tag != "my-tag" {
		t.Errorf("Expected tag 'my-tag', got %v", sessions[0].Tag)
	}
}

func TestTagExtraction_LastWins(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "hello"}}),
		tagJSON("first-tag", sid),
		tagJSON("second-tag", sid),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Tag == nil || *sessions[0].Tag != "second-tag" {
		t.Errorf("Expected tag 'second-tag', got %v", sessions[0].Tag)
	}
}

func TestTagExtraction_EmptyStringIsNil(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "hello"}}),
		tagJSON("old-tag", sid),
		tagJSON("", sid),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Tag != nil {
		t.Errorf("Expected nil tag for empty clear marker, got %v", *sessions[0].Tag)
	}
}

func TestTagExtraction_Absent(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	makeTestSessionFile(t, projectDir, withFirstPrompt("hello"))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Tag != nil {
		t.Errorf("Expected nil tag, got %v", *sessions[0].Tag)
	}
}

func TestTagExtraction_IgnoresToolUseInputs(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "tag this v1.0"}}),
		tagJSON("real-tag", sid),
		// A tool_use entry with a "tag" key — must NOT match.
		compactJSON(map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type":  "tool_use",
						"name":  "mcp__docker__build",
						"input": map[string]interface{}{"tag": "myapp:v2", "context": "."},
					},
				},
			},
		}),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Tag == nil || *sessions[0].Tag != "real-tag" {
		t.Errorf("Expected tag 'real-tag', got %v", sessions[0].Tag)
	}
}

// ---------------------------------------------------------------------------
// TestCreatedAt
// ---------------------------------------------------------------------------

func TestCreatedAt_FromISOTimestamp(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	makeTestSessionFile(t, projectDir,
		withFirstPrompt("hello"),
		withTimestamp("2026-01-15T10:30:00.000Z"),
	)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	// 2026-01-15T10:30:00Z = 1768473000 seconds = 1768473000000 ms
	if sessions[0].CreatedAt == nil || *sessions[0].CreatedAt != 1768473000000 {
		t.Errorf("Expected created_at=1768473000000, got %v", sessions[0].CreatedAt)
	}
}

func TestCreatedAt_LeqLastModified(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	_, fp := makeTestSessionFile(t, projectDir,
		withFirstPrompt("hello"),
		withTimestamp("2026-01-01T00:00:00.000Z"),
	)
	// Set mtime to Feb 2026
	os.Chtimes(fp, time.Unix(1769904000, 0), time.Unix(1769904000, 0))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].CreatedAt == nil {
		t.Fatal("Expected created_at to be set")
	}
	if *sessions[0].CreatedAt > sessions[0].LastModified {
		t.Errorf("Expected created_at <= last_modified, got %d > %d",
			*sessions[0].CreatedAt, sessions[0].LastModified)
	}
}

func TestCreatedAt_NilWhenMissing(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	makeTestSessionFile(t, projectDir, withFirstPrompt("no timestamp"))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].CreatedAt != nil {
		t.Errorf("Expected nil created_at, got %v", *sessions[0].CreatedAt)
	}
}

func TestCreatedAt_NilOnInvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	sid := generateUUID()
	fp := filepath.Join(tmpDir, sid+".jsonl")
	line := compactJSON(map[string]interface{}{
		"type":      "user",
		"message":   map[string]interface{}{"content": "hello"},
		"timestamp": "not-a-valid-iso-date",
	})
	os.WriteFile(fp, []byte(line+"\n"), 0644)

	lite := readSessionLite(fp)
	if lite == nil {
		t.Fatal("Expected non-nil lite")
	}
	info := parseSessionInfoFromLite(sid, lite, "/fallback")
	if info == nil {
		t.Fatal("Expected non-nil info")
	}
	if info.CreatedAt != nil {
		t.Errorf("Expected nil created_at for invalid format, got %v", *info.CreatedAt)
	}
}

func TestCreatedAt_WithoutZSuffix(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	sid := generateUUID()
	fp := filepath.Join(tmpDir, sid+".jsonl")
	line := compactJSON(map[string]interface{}{
		"type":      "user",
		"message":   map[string]interface{}{"content": "hello"},
		"timestamp": "2026-01-15T10:30:00+00:00",
	})
	os.WriteFile(fp, []byte(line+"\n"), 0644)

	lite := readSessionLite(fp)
	if lite == nil {
		t.Fatal("Expected non-nil lite")
	}
	info := parseSessionInfoFromLite(sid, lite, "")
	if info == nil {
		t.Fatal("Expected non-nil info")
	}
	if info.CreatedAt == nil || *info.CreatedAt != 1768473000000 {
		t.Errorf("Expected created_at=1768473000000, got %v", info.CreatedAt)
	}
}

// ---------------------------------------------------------------------------
// TestGetSessionInfo
// ---------------------------------------------------------------------------

func TestGetSessionInfo_InvalidSessionID(t *testing.T) {
	_, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: "not-a-uuid"})
	if err == nil {
		t.Fatal("Expected error for invalid session ID")
	}
}

func TestGetSessionInfo_NonexistentSession(t *testing.T) {
	setupSessionTestProject(t)
	sid := generateUUID()
	info, _ := GetSessionInfo(&GetSessionInfoOptions{SessionID: sid})
	if info != nil {
		t.Errorf("Expected nil for nonexistent session, got %v", info)
	}
}

func TestGetSessionInfo_FoundWithDirectory(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, _ := makeTestSessionFile(t, projectDir,
		withFirstPrompt("hello"),
		withGitBranch("main"),
	)

	info, err := GetSessionInfo(&GetSessionInfoOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("GetSessionInfo: %v", err)
	}
	if info == nil {
		t.Fatal("Expected non-nil info")
	}
	if info.SessionID != sid {
		t.Errorf("Expected session_id=%s, got %s", sid, info.SessionID)
	}
	if info.Summary != "hello" {
		t.Errorf("Expected summary='hello', got '%s'", info.Summary)
	}
	if info.GitBranch == nil || *info.GitBranch != "main" {
		t.Errorf("Expected git_branch='main', got %v", info.GitBranch)
	}
}

func TestGetSessionInfo_FoundWithoutDirectory(t *testing.T) {
	_, _, projectDir := setupSessionTestProject(t)
	sid, _ := makeTestSessionFile(t, projectDir, withFirstPrompt("search all"))

	info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: sid})
	if err != nil {
		t.Fatalf("GetSessionInfo: %v", err)
	}
	if info == nil {
		t.Fatal("Expected non-nil info")
	}
	if info.SessionID != sid {
		t.Errorf("Expected session_id=%s, got %s", sid, info.SessionID)
	}
	if info.Summary != "search all" {
		t.Errorf("Expected summary='search all', got '%s'", info.Summary)
	}
}

func TestGetSessionInfo_SidechainReturnsNil(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, _ := makeTestSessionFile(t, projectDir,
		withFirstPrompt("sidechain"),
		withSidechain(),
	)

	info, _ := GetSessionInfo(&GetSessionInfoOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if info != nil {
		t.Error("Expected nil for sidechain session")
	}
}

func TestGetSessionInfo_IncludesTag(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "hello"}}),
		tagJSON("urgent", sid),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	info, err := GetSessionInfo(&GetSessionInfoOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("GetSessionInfo: %v", err)
	}
	if info == nil {
		t.Fatal("Expected non-nil info")
	}
	if info.Tag == nil || *info.Tag != "urgent" {
		t.Errorf("Expected tag='urgent', got %v", info.Tag)
	}
}

func TestParseSessionInfoFromLite_Helper(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	sid := generateUUID()
	fp := filepath.Join(tmpDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"content": "test prompt"},
			"cwd":     "/workspace",
		}),
		tagJSON("experiment", sid),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	lite := readSessionLite(fp)
	if lite == nil {
		t.Fatal("Expected non-nil lite")
	}
	info := parseSessionInfoFromLite(sid, lite, "/fallback")
	if info == nil {
		t.Fatal("Expected non-nil info")
	}
	if info.SessionID != sid {
		t.Errorf("Expected session_id=%s, got %s", sid, info.SessionID)
	}
	if info.Summary != "test prompt" {
		t.Errorf("Expected summary='test prompt', got '%s'", info.Summary)
	}
	if info.Tag == nil || *info.Tag != "experiment" {
		t.Errorf("Expected tag='experiment', got %v", info.Tag)
	}
	if info.CWD == nil || *info.CWD != "/workspace" {
		t.Errorf("Expected cwd='/workspace', got %v", info.CWD)
	}
}

// ---------------------------------------------------------------------------
// Test extractFirstPromptFromHead
// ---------------------------------------------------------------------------

func TestExtractFirstPromptFromHead(t *testing.T) {
	t.Run("simple prompt", func(t *testing.T) {
		head := compactJSON(map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"content": "Hello!"},
		}) + "\n"
		result := extractFirstPromptFromHead(head)
		if result != "Hello!" {
			t.Errorf("Expected 'Hello!', got %q", result)
		}
	})

	t.Run("skips isMeta", func(t *testing.T) {
		head := compactJSON(map[string]interface{}{
			"type":    "user",
			"isMeta":  true,
			"message": map[string]interface{}{"content": "meta"},
		}) + "\n" +
			compactJSON(map[string]interface{}{
				"type":    "user",
				"message": map[string]interface{}{"content": "real prompt"},
			}) + "\n"
		result := extractFirstPromptFromHead(head)
		if result != "real prompt" {
			t.Errorf("Expected 'real prompt', got %q", result)
		}
	})

	t.Run("skips tool_result", func(t *testing.T) {
		head := compactJSON(map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "tool_result", "content": "x"},
				},
			},
		}) + "\n" +
			compactJSON(map[string]interface{}{
				"type":    "user",
				"message": map[string]interface{}{"content": "actual prompt"},
			}) + "\n"
		result := extractFirstPromptFromHead(head)
		if result != "actual prompt" {
			t.Errorf("Expected 'actual prompt', got %q", result)
		}
	})

	t.Run("content blocks with text", func(t *testing.T) {
		head := compactJSON(map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "block prompt"},
				},
			},
		}) + "\n"
		result := extractFirstPromptFromHead(head)
		if result != "block prompt" {
			t.Errorf("Expected 'block prompt', got %q", result)
		}
	})

	t.Run("truncates long prompts", func(t *testing.T) {
		longPrompt := strings.Repeat("x", 300)
		head := compactJSON(map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"content": longPrompt},
		}) + "\n"
		result := extractFirstPromptFromHead(head)
		// 200 chars + "…" (3 bytes UTF-8)
		if len([]rune(result)) > 201 {
			t.Errorf("Expected truncated result (<= 201 runes), got %d", len([]rune(result)))
		}
		if !strings.HasSuffix(result, "\u2026") {
			t.Error("Expected ellipsis suffix for truncated prompt")
		}
	})

	t.Run("command fallback", func(t *testing.T) {
		head := compactJSON(map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"content": "<command-name>/help</command-name>stuff"},
		}) + "\n"
		result := extractFirstPromptFromHead(head)
		if result != "/help" {
			t.Errorf("Expected '/help', got %q", result)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		if result := extractFirstPromptFromHead(""); result != "" {
			t.Errorf("Expected empty string, got %q", result)
		}
	})

	t.Run("no user messages", func(t *testing.T) {
		head := compactJSON(map[string]interface{}{
			"type":    "assistant",
			"message": map[string]interface{}{"content": "response"},
		}) + "\n"
		if result := extractFirstPromptFromHead(head); result != "" {
			t.Errorf("Expected empty string, got %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// TestListSessions integration tests
// ---------------------------------------------------------------------------

func TestListSessions_EmptyProjectsDir(t *testing.T) {
	_, projectPath, _ := setupSessionTestProject(t)
	// No session files created

	sessions, err := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	// No session files → empty list
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions, got %d", len(sessions))
	}
}

func TestListSessions_NoConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistent := filepath.Join(tmpDir, "nonexistent")
	t.Setenv("CLAUDE_CONFIG_DIR", nonexistent)

	_, err := ListSessions(nil)
	if err == nil {
		t.Fatal("Expected error for nonexistent config dir")
	}
}

func TestListSessions_SingleSession(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	sid, _ := makeTestSessionFile(t, projectDir,
		withFirstPrompt("What is 2+2?"),
		withGitBranch("main"),
	)

	sessions, err := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.SessionID != sid {
		t.Errorf("Session ID mismatch")
	}
	if s.Summary != "What is 2+2?" {
		t.Errorf("Expected summary='What is 2+2?', got '%s'", s.Summary)
	}
	if s.GitBranch == nil || *s.GitBranch != "main" {
		t.Errorf("Expected git_branch='main', got %v", s.GitBranch)
	}
	if s.FileSize == nil || *s.FileSize <= 0 {
		t.Error("Expected file_size > 0")
	}
	if s.LastModified <= 0 {
		t.Error("Expected last_modified > 0")
	}
}

func TestListSessions_CustomTitleWinsSummary(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "original question"}}),
		compactJSON(map[string]interface{}{"type": "assistant", "message": map[string]interface{}{"content": "response"}}),
		compactJSON(map[string]interface{}{"type": "summary", "summary": "auto summary", "customTitle": "My Custom Title"}),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Summary != "My Custom Title" {
		t.Errorf("Expected summary='My Custom Title', got '%s'", sessions[0].Summary)
	}
	if sessions[0].CustomTitle == nil || *sessions[0].CustomTitle != "My Custom Title" {
		t.Errorf("Expected custom_title='My Custom Title'")
	}
}

func TestListSessions_SummaryWinsFirstPrompt(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "question"}}),
		compactJSON(map[string]interface{}{"type": "summary", "summary": "better summary"}),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Summary != "better summary" {
		t.Errorf("Expected summary='better summary', got '%s'", sessions[0].Summary)
	}
	if sessions[0].CustomTitle != nil {
		t.Errorf("Expected nil custom_title, got %v", *sessions[0].CustomTitle)
	}
}

func TestListSessions_MultipleSessions_SortedByMtime(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	sidOld, _ := makeTestSessionFile(t, projectDir, withFirstPrompt("old"), withMtime(1000))
	sidNew, _ := makeTestSessionFile(t, projectDir, withFirstPrompt("new"), withMtime(3000))
	sidMid, _ := makeTestSessionFile(t, projectDir, withFirstPrompt("mid"), withMtime(2000))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 3 {
		t.Fatalf("Expected 3 sessions, got %d", len(sessions))
	}
	ids := []string{sessions[0].SessionID, sessions[1].SessionID, sessions[2].SessionID}
	if ids[0] != sidNew || ids[1] != sidMid || ids[2] != sidOld {
		t.Errorf("Expected order [new, mid, old], got %v", ids)
	}
}

func TestListSessions_FiltersSidechainSessions(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	makeTestSessionFile(t, projectDir, withFirstPrompt("normal"))
	makeTestSessionFile(t, projectDir, withFirstPrompt("sidechain"), withSidechain())

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session (sidechain filtered), got %d", len(sessions))
	}
	if sessions[0].Summary != "normal" {
		t.Errorf("Expected normal session, got '%s'", sessions[0].Summary)
	}
}

func TestListSessions_IgnoresNonJsonlFiles(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("not a session"), 0644)
	makeTestSessionFile(t, projectDir, withFirstPrompt("session"))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(sessions))
	}
}

func TestListSessions_FiltersNonUUIDFilenames(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	os.WriteFile(filepath.Join(projectDir, "not-a-uuid.jsonl"),
		[]byte(compactJSON(map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "x"}})+"\n"), 0644)
	makeTestSessionFile(t, projectDir, withFirstPrompt("valid session"))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(sessions))
	}
}

func TestListSessions_ListAllSessions(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	proj1 := filepath.Join(projectsDir, sanitizePath("/some/path/one"))
	proj2 := filepath.Join(projectsDir, sanitizePath("/some/path/two"))
	os.MkdirAll(proj1, 0755)
	os.MkdirAll(proj2, 0755)

	makeTestSessionFile(t, proj1, withFirstPrompt("from proj1"), withMtime(1000))
	makeTestSessionFile(t, proj2, withFirstPrompt("from proj2"), withMtime(2000))

	sessions, err := ListSessions(nil)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].Summary != "from proj2" {
		t.Errorf("Expected newest first ('from proj2'), got '%s'", sessions[0].Summary)
	}
}

func TestListSessions_DeduplicatesBySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	proj1 := filepath.Join(projectsDir, sanitizePath("/path/one"))
	proj2 := filepath.Join(projectsDir, sanitizePath("/path/two"))
	os.MkdirAll(proj1, 0755)
	os.MkdirAll(proj2, 0755)

	sharedSID := generateUUID()

	// Older copy in proj1
	fp1 := filepath.Join(proj1, sharedSID+".jsonl")
	os.WriteFile(fp1, []byte(compactJSON(map[string]interface{}{
		"type": "user", "message": map[string]interface{}{"content": "older"},
	})+"\n"), 0644)
	os.Chtimes(fp1, time.Unix(1000, 0), time.Unix(1000, 0))

	// Newer copy in proj2
	fp2 := filepath.Join(proj2, sharedSID+".jsonl")
	os.WriteFile(fp2, []byte(compactJSON(map[string]interface{}{
		"type": "user", "message": map[string]interface{}{"content": "newer"},
	})+"\n"), 0644)
	os.Chtimes(fp2, time.Unix(2000, 0), time.Unix(2000, 0))

	sessions, _ := ListSessions(nil)
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session (deduped), got %d", len(sessions))
	}
	if sessions[0].Summary != "newer" {
		t.Errorf("Expected newer session summary, got '%s'", sessions[0].Summary)
	}
}

func TestListSessions_EmptyFileFiltered(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	sid := generateUUID()
	os.WriteFile(filepath.Join(projectDir, sid+".jsonl"), []byte(""), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions for empty file, got %d", len(sessions))
	}
}

func TestListSessions_CwdFallbackToProjectPath(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	// Session without cwd field
	makeTestSessionFile(t, projectDir, withFirstPrompt("no cwd field"))

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].CWD == nil {
		t.Fatal("Expected non-nil CWD")
	}
	// CWD should fall back to the project path
	if *sessions[0].CWD != projectPath {
		t.Errorf("Expected cwd='%s', got '%s'", projectPath, *sessions[0].CWD)
	}
}

func TestListSessions_GitBranchFromTailPreferred(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		compactJSON(map[string]interface{}{
			"type":      "user",
			"message":   map[string]interface{}{"content": "hello"},
			"gitBranch": "old-branch",
		}),
		compactJSON(map[string]interface{}{
			"type":      "summary",
			"gitBranch": "new-branch",
		}),
	}
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].GitBranch == nil || *sessions[0].GitBranch != "new-branch" {
		t.Errorf("Expected git_branch='new-branch', got %v", sessions[0].GitBranch)
	}
}

func TestListSessions_Limit(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	for i := 0; i < 5; i++ {
		makeTestSessionFile(t, projectDir,
			withFirstPrompt(fmt.Sprintf("prompt %d", i)),
			withMtime(1000+float64(i)),
		)
	}

	limit2 := 2
	sessions, _ := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
		Limit:     &limit2,
	})
	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions with limit=2, got %d", len(sessions))
	}
	// Should be the 2 newest
	if sessions[0].LastModified < sessions[1].LastModified {
		t.Error("Expected sessions sorted newest first")
	}
}

func TestListSessions_LimitZeroReturnsAll(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	for i := 0; i < 3; i++ {
		makeTestSessionFile(t, projectDir, withFirstPrompt(fmt.Sprintf("p%d", i)))
	}

	limit0 := 0
	sessions, _ := ListSessions(&ListSessionsOptions{
		Directory: &projectPath,
		Limit:     &limit0,
	})
	if len(sessions) != 3 {
		t.Errorf("Expected 3 sessions with limit=0, got %d", len(sessions))
	}
}

// ---------------------------------------------------------------------------
// TestGetSessionMessages
// ---------------------------------------------------------------------------

func makeTranscriptEntry(entryType, uuid string, parentUUID *string, sessionID, content string) map[string]interface{} {
	entry := map[string]interface{}{
		"type":       entryType,
		"uuid":       uuid,
		"parentUuid": parentUUID,
		"sessionId":  sessionID,
	}
	if content != "" {
		role := entryType
		if role != "user" && role != "assistant" {
			role = "user"
		}
		entry["message"] = map[string]interface{}{"role": role, "content": content}
	}
	return entry
}

func writeTranscript(t *testing.T, dir, sessionID string, entries []map[string]interface{}) {
	t.Helper()
	lines := []string{}
	for _, e := range entries {
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	fp := filepath.Join(dir, sessionID+".jsonl")
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func strPtr(s string) *string { return &s }

func TestGetSessionMessages_InvalidSessionID(t *testing.T) {
	setupSessionTestProject(t)
	_, err := GetSessionMessages(&GetSessionMessagesOptions{SessionID: "not-a-uuid"})
	if err == nil {
		t.Fatal("Expected error for invalid session ID")
	}
}

func TestGetSessionMessages_NonexistentSession(t *testing.T) {
	setupSessionTestProject(t)
	sid := generateUUID()
	msgs, err := GetSessionMessages(&GetSessionMessagesOptions{SessionID: sid})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(msgs))
	}
}

func TestGetSessionMessages_SimpleChain(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, a1, u2, a2 := generateUUID(), generateUUID(), generateUUID(), generateUUID()

	entries := []map[string]interface{}{
		makeTranscriptEntry("user", u1, nil, sid, "hello"),
		makeTranscriptEntry("assistant", a1, &u1, sid, "hi!"),
		makeTranscriptEntry("user", u2, &a1, sid, "thanks"),
		makeTranscriptEntry("assistant", a2, &u2, sid, "welcome"),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, err := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].UUID != u1 || msgs[0].Type != "user" {
		t.Errorf("Expected first message to be user u1")
	}
	if msgs[1].UUID != a1 || msgs[1].Type != "assistant" {
		t.Errorf("Expected second message to be assistant a1")
	}
	if msgs[2].UUID != u2 {
		t.Errorf("Expected third message to be user u2")
	}
	if msgs[3].UUID != a2 {
		t.Errorf("Expected fourth message to be assistant a2")
	}
	// All messages should have the session ID
	for _, m := range msgs {
		if m.SessionID != sid {
			t.Errorf("Expected session_id=%s, got %s", sid, m.SessionID)
		}
	}
}

func TestGetSessionMessages_FiltersMetaMessages(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, meta, a1 := generateUUID(), generateUUID(), generateUUID()

	entries := []map[string]interface{}{
		makeTranscriptEntry("user", u1, nil, sid, "hello"),
		// Meta user message in the chain
		func() map[string]interface{} {
			e := makeTranscriptEntry("user", meta, &u1, sid, "meta")
			e["isMeta"] = true
			return e
		}(),
		makeTranscriptEntry("assistant", a1, &meta, sid, "hi"),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	// Only u1 and a1 visible (meta filtered out)
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages (meta filtered), got %d", len(msgs))
	}
	if msgs[0].UUID != u1 {
		t.Errorf("Expected first message uuid=%s, got %s", u1, msgs[0].UUID)
	}
	if msgs[1].UUID != a1 {
		t.Errorf("Expected second message uuid=%s, got %s", a1, msgs[1].UUID)
	}
}

func TestGetSessionMessages_FiltersNonUserAssistant(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, prog, a1 := generateUUID(), generateUUID(), generateUUID()

	entries := []map[string]interface{}{
		makeTranscriptEntry("user", u1, nil, sid, "hello"),
		makeTranscriptEntry("progress", prog, &u1, sid, ""),
		makeTranscriptEntry("assistant", a1, &prog, sid, "hi"),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages (progress filtered), got %d", len(msgs))
	}
	if msgs[0].UUID != u1 || msgs[1].UUID != a1 {
		t.Error("Expected user and assistant only")
	}
}

func TestGetSessionMessages_PicksMainChainOverSidechain(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	root, mainLeaf, sideLeaf := generateUUID(), generateUUID(), generateUUID()

	entries := []map[string]interface{}{
		makeTranscriptEntry("user", root, nil, sid, "root"),
		makeTranscriptEntry("assistant", mainLeaf, &root, sid, "main"),
		func() map[string]interface{} {
			e := makeTranscriptEntry("assistant", sideLeaf, &root, sid, "side")
			e["isSidechain"] = true
			return e
		}(),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].UUID != mainLeaf {
		t.Errorf("Expected main leaf, not sidechain")
	}
}

func TestGetSessionMessages_PicksLatestLeafByFilePosition(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	root, oldLeaf, newLeaf := generateUUID(), generateUUID(), generateUUID()

	entries := []map[string]interface{}{
		makeTranscriptEntry("user", root, nil, sid, "root"),
		makeTranscriptEntry("assistant", oldLeaf, &root, sid, "old"),
		makeTranscriptEntry("assistant", newLeaf, &root, sid, "new"),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].UUID != newLeaf {
		t.Errorf("Expected new leaf (higher file position), got %s", msgs[1].UUID)
	}
}

func TestGetSessionMessages_TerminalNonMessageWalkedBack(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, a1, prog := generateUUID(), generateUUID(), generateUUID()

	entries := []map[string]interface{}{
		makeTranscriptEntry("user", u1, nil, sid, "hi"),
		makeTranscriptEntry("assistant", a1, &u1, sid, "hello"),
		makeTranscriptEntry("progress", prog, &a1, sid, ""),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].UUID != u1 || msgs[1].UUID != a1 {
		t.Error("Expected terminal progress walked back to user/assistant")
	}
}

func TestGetSessionMessages_CorruptLinesSkipped(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, a1 := generateUUID(), generateUUID()

	lines := []string{
		compactJSON(makeTranscriptEntry("user", u1, nil, sid, "hi")),
		"not valid json {{{",
		"",
		compactJSON(makeTranscriptEntry("assistant", a1, &u1, sid, "hello")),
	}
	fp := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages (corrupt lines skipped), got %d", len(msgs))
	}
}

func TestGetSessionMessages_CycleDetection(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, a1 := generateUUID(), generateUUID()

	// Cyclic: a1 → u1 → a1
	entries := []map[string]interface{}{
		makeTranscriptEntry("user", u1, &a1, sid, "hi"),
		makeTranscriptEntry("assistant", a1, &u1, sid, "hello"),
	}
	writeTranscript(t, projectDir, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	// Both are parents of each other → no terminals → empty
	if len(msgs) != 0 {
		t.Errorf("Expected 0 messages for cyclic chain, got %d", len(msgs))
	}
}

func TestGetSessionMessages_EmptyTranscript(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(fp, []byte(""), 0644)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 0 {
		t.Errorf("Expected 0 messages for empty file, got %d", len(msgs))
	}
}

func TestGetSessionMessages_IgnoresNonTranscriptTypes(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()
	u1, a1 := generateUUID(), generateUUID()

	lines := []string{
		compactJSON(makeTranscriptEntry("user", u1, nil, sid, "hi")),
		compactJSON(map[string]interface{}{"type": "summary", "summary": "A nice chat"}),
		compactJSON(makeTranscriptEntry("assistant", a1, &u1, sid, "hello")),
	}
	fp := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages (summary ignored), got %d", len(msgs))
	}
}

func TestGetSessionMessages_LimitAndOffset(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := generateUUID()

	// Build chain of 6: u→a→u→a→u→a
	uuids := make([]string, 6)
	for i := range uuids {
		uuids[i] = generateUUID()
	}
	entries := []map[string]interface{}{}
	for i, uid := range uuids {
		var parentUUID *string
		if i > 0 {
			parentUUID = &uuids[i-1]
		}
		entryType := "user"
		if i%2 != 0 {
			entryType = "assistant"
		}
		entries = append(entries, makeTranscriptEntry(entryType, uid, parentUUID, sid, fmt.Sprintf("m%d", i)))
	}
	writeTranscript(t, projectDir, sid, entries)

	// No limit/offset
	allMsgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if len(allMsgs) != 6 {
		t.Fatalf("Expected 6 messages, got %d", len(allMsgs))
	}

	// limit=2
	limit2 := 2
	page, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
		Limit:     &limit2,
	})
	if len(page) != 2 {
		t.Errorf("Expected 2 messages with limit=2, got %d", len(page))
	}
	if page[0].UUID != uuids[0] || page[1].UUID != uuids[1] {
		t.Error("Expected first 2 messages")
	}

	// offset=2, limit=2
	page, _ = GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
		Limit:     &limit2,
		Offset:    2,
	})
	if len(page) != 2 {
		t.Errorf("Expected 2 messages with offset=2 limit=2, got %d", len(page))
	}
	if page[0].UUID != uuids[2] || page[1].UUID != uuids[3] {
		t.Error("Expected messages 2-3")
	}

	// offset beyond end
	page, _ = GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
		Offset:    100,
	})
	if len(page) != 0 {
		t.Errorf("Expected 0 messages for large offset, got %d", len(page))
	}
}

func TestGetSessionMessages_SearchAllProjects(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	proj2 := filepath.Join(projectsDir, sanitizePath("/path/two"))
	os.MkdirAll(proj2, 0755)

	sid := generateUUID()
	u1, a1 := generateUUID(), generateUUID()
	entries := []map[string]interface{}{
		makeTranscriptEntry("user", u1, nil, sid, "hi"),
		makeTranscriptEntry("assistant", a1, &u1, sid, "hello"),
	}
	writeTranscript(t, proj2, sid, entries)

	msgs, _ := GetSessionMessages(&GetSessionMessagesOptions{SessionID: sid})
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages from search-all, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// TestBuildConversationChain
// ---------------------------------------------------------------------------

func TestBuildConversationChain(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		result := buildConversationChain(nil)
		if len(result) != 0 {
			t.Errorf("Expected empty, got %d", len(result))
		}
	})

	t.Run("single entry", func(t *testing.T) {
		entries := []transcriptEntry{
			{Type: "user", UUID: "a"},
		}
		result := buildConversationChain(entries)
		if len(result) != 1 || result[0].UUID != "a" {
			t.Errorf("Expected single entry 'a', got %v", result)
		}
	})

	t.Run("linear chain", func(t *testing.T) {
		bPtr := strPtr("a")
		cPtr := strPtr("b")
		entries := []transcriptEntry{
			{Type: "user", UUID: "a"},
			{Type: "assistant", UUID: "b", ParentUUID: bPtr},
			{Type: "user", UUID: "c", ParentUUID: cPtr},
		}
		result := buildConversationChain(entries)
		if len(result) != 3 {
			t.Fatalf("Expected 3, got %d", len(result))
		}
		ids := []string{result[0].UUID, result[1].UUID, result[2].UUID}
		if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
			t.Errorf("Expected [a,b,c], got %v", ids)
		}
	})

	t.Run("only progress returns empty", func(t *testing.T) {
		aPtr := strPtr("a")
		entries := []transcriptEntry{
			{Type: "progress", UUID: "a"},
			{Type: "progress", UUID: "b", ParentUUID: aPtr},
		}
		result := buildConversationChain(entries)
		if len(result) != 0 {
			t.Errorf("Expected empty for progress-only entries, got %d", len(result))
		}
	})
}
