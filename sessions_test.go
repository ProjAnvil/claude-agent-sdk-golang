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
