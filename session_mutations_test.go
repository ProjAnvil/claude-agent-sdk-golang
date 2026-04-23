package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestProject creates a temp CLAUDE_CONFIG_DIR with a project directory.
// Returns (configDir, projectPath, projectDir). Caller must defer cleanup.
func setupTestProject(t *testing.T) (string, string, string) {
	t.Helper()

	tmpDir := t.TempDir()
	// Resolve symlinks so the path matches what canonicalizePath returns
	// (on macOS, /var -> /private/var)
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	// Create a project directory
	projectPath := filepath.Join(tmpDir, "proj")
	os.MkdirAll(projectPath, 0755)

	sanitized := sanitizePath(projectPath)
	projectDir := filepath.Join(projectsDir, sanitized)
	os.MkdirAll(projectDir, 0755)

	return configDir, projectPath, projectDir
}

// makeSessionFile creates a .jsonl session file with basic messages.
func makeSessionFile(t *testing.T, projectDir, sessionID string) string {
	t.Helper()

	filePath := filepath.Join(projectDir, sessionID+".jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"Hello Claude"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"Hi!"}}`,
	}
	os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	return filePath
}

// TestRenameSession_InvalidSessionID tests that invalid UUID is rejected.
func TestRenameSession_InvalidSessionID(t *testing.T) {
	err := RenameSession("not-a-uuid", "title", nil)
	if err == nil {
		t.Fatal("Expected error for invalid session ID")
	}
	if !strings.Contains(err.Error(), "invalid session_id") {
		t.Errorf("Expected 'invalid session_id' error, got: %v", err)
	}
}

// TestRenameSession_EmptyTitle tests that empty title is rejected.
func TestRenameSession_EmptyTitle(t *testing.T) {
	_, _, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	makeSessionFile(t, projectDir, sid)

	for _, title := range []string{"", "   ", "\n\t"} {
		err := RenameSession(sid, title, nil)
		if err == nil {
			t.Errorf("Expected error for empty/whitespace title %q", title)
		}
	}
}

// TestRenameSession_AppendsCustomTitle tests that custom-title entry is appended.
func TestRenameSession_AppendsCustomTitle(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	filePath := makeSessionFile(t, projectDir, sid)

	err := RenameSession(sid, "My New Title", &projectPath)
	if err != nil {
		t.Fatalf("RenameSession failed: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lastLine := lines[len(lines)-1]

	var entry map[string]interface{}
	json.Unmarshal([]byte(lastLine), &entry)
	if entry["type"] != "custom-title" {
		t.Errorf("Expected type 'custom-title', got %v", entry["type"])
	}
	if entry["customTitle"] != "My New Title" {
		t.Errorf("Expected customTitle 'My New Title', got %v", entry["customTitle"])
	}
	if entry["sessionId"] != sid {
		t.Errorf("Expected sessionId %s, got %v", sid, entry["sessionId"])
	}
}

// TestRenameSession_TitleTrimmed tests that whitespace is stripped.
func TestRenameSession_TitleTrimmed(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440001"
	filePath := makeSessionFile(t, projectDir, sid)

	err := RenameSession(sid, "  Trimmed Title  ", &projectPath)
	if err != nil {
		t.Fatalf("RenameSession failed: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["customTitle"] != "Trimmed Title" {
		t.Errorf("Expected trimmed title, got %v", entry["customTitle"])
	}
}

// TestTagSession_InvalidSessionID tests validation.
func TestTagSession_InvalidSessionID(t *testing.T) {
	err := TagSession("bad-id", nil, nil)
	if err == nil {
		t.Fatal("Expected error for invalid session ID")
	}
}

// TestTagSession_AppendsTagEntry tests that tag entry is appended.
func TestTagSession_AppendsTagEntry(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440002"
	filePath := makeSessionFile(t, projectDir, sid)

	tag := "important"
	err := TagSession(sid, &tag, &projectPath)
	if err != nil {
		t.Fatalf("TagSession failed: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["type"] != "tag" {
		t.Errorf("Expected type 'tag', got %v", entry["type"])
	}
	if entry["tag"] != "important" {
		t.Errorf("Expected tag 'important', got %v", entry["tag"])
	}
}

// TestTagSession_ClearTag tests that nil tag clears the tag.
func TestTagSession_ClearTag(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440003"
	filePath := makeSessionFile(t, projectDir, sid)

	err := TagSession(sid, nil, &projectPath)
	if err != nil {
		t.Fatalf("TagSession failed: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["tag"] != "" {
		t.Errorf("Expected empty tag for clear, got %v", entry["tag"])
	}
}

// TestDeleteSession_RemovesFile tests that session file is deleted.
func TestDeleteSession_RemovesFile(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440004"
	filePath := makeSessionFile(t, projectDir, sid)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("Session file should exist before delete")
	}

	err := DeleteSession(sid, &projectPath)
	if err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("Session file should not exist after delete")
	}
}

// TestDeleteSession_InvalidSessionID tests validation.
func TestDeleteSession_InvalidSessionID(t *testing.T) {
	err := DeleteSession("bad-id", nil)
	if err == nil {
		t.Fatal("Expected error for invalid session ID")
	}
}

// TestDeleteSession_NotFound tests error when session doesn't exist.
func TestDeleteSession_NotFound(t *testing.T) {
	_, projectPath, _ := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440099"

	err := DeleteSession(sid, &projectPath)
	if err == nil {
		t.Fatal("Expected error for non-existent session")
	}
}

// TestSanitizeUnicode tests Unicode sanitization.
func TestSanitizeUnicode_Basic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal text", "hello world", "hello world"},
		{"zero-width chars", "he\u200bllo", "hello"},
		{"BOM", "\uFEFFhello", "hello"},
		{"RTL override", "he\u202ello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeUnicode(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeUnicode(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestForkSession_BasicFork tests basic fork functionality.
func TestForkSession_BasicFork(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440005"

	// Create a more complete session file
	lines := []string{
		`{"type":"summary","summary":"Test session","sessionId":"` + sid + `"}`,
		`{"parentUuid":null,"isSidechain":false,"type":"user","uuid":"u1","message":{"role":"user","content":"Hello"},"sessionId":"` + sid + `","timestamp":"2024-01-01T00:00:00.000Z"}`,
		`{"parentUuid":"u1","isSidechain":false,"type":"assistant","uuid":"a1","message":{"role":"assistant","content":"Hi!"},"sessionId":"` + sid + `","timestamp":"2024-01-01T00:00:01.000Z"}`,
	}
	filePath := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	forkTitle := "Forked Session"
	opts := &ForkSessionOptions{
		SessionID: sid,
		Title:     &forkTitle,
		Directory: &projectPath,
	}

	result, err := ForkSession(opts)
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}

	if result.SessionID == "" {
		t.Error("Expected non-empty SessionID")
	}
	if result.SessionID == sid {
		t.Error("Forked session should have different ID")
	}

	// Verify forked file exists
	forkedPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	if _, err := os.Stat(forkedPath); os.IsNotExist(err) {
		t.Error("Forked session file should exist")
	}

	// Read and verify the forked file
	data, _ := os.ReadFile(forkedPath)
	forkedLines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Should have at least the messages + custom-title + forkedFrom
	if len(forkedLines) < 3 {
		t.Errorf("Expected at least 3 lines in forked file, got %d", len(forkedLines))
	}

	// Check that custom-title was added
	foundTitle := false
	for _, line := range forkedLines {
		var entry map[string]interface{}
		json.Unmarshal([]byte(line), &entry)
		if entry["type"] == "custom-title" {
			foundTitle = true
			if entry["customTitle"] != "Forked Session" {
				t.Errorf("Expected title 'Forked Session', got %v", entry["customTitle"])
			}
		}
	}
	if !foundTitle {
		t.Error("Expected custom-title entry in forked file")
	}
}

// ---------------------------------------------------------------------------
// appendToSession (tryAppend) tests
// ---------------------------------------------------------------------------

func TestAppendToSession_ExistingFile(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := makeSessionFile(t, projectDir, sid)

	err := appendToSession(sid, `{"type":"tag","tag":"hello"}`+"\n", &projectPath)
	if err != nil {
		t.Fatalf("appendToSession: %v", err)
	}

	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), `"tag":"hello"`) {
		t.Error("Expected appended tag in file")
	}
}

func TestAppendToSession_MissingFile(t *testing.T) {
	_, projectPath, _ := setupTestProject(t)
	sid := generateUUID()

	err := appendToSession(sid, "data\n", &projectPath)
	if err == nil {
		t.Fatal("Expected error for missing file")
	}
}

func TestAppendToSession_ZeroByteFile(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(fp, []byte(""), 0644)

	err := appendToSession(sid, "data\n", &projectPath)
	if err == nil {
		t.Fatal("Expected error for zero-byte file")
	}

	data, _ := os.ReadFile(fp)
	if len(data) != 0 {
		t.Error("Zero-byte file should remain unchanged")
	}
}

func TestAppendToSession_MultipleAppends(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	makeSessionFile(t, projectDir, sid)

	for i := 0; i < 3; i++ {
		line := fmt.Sprintf(`{"type":"tag","tag":"t%d","sessionId":"%s"}`+"\n", i, sid)
		err := appendToSession(sid, line, &projectPath)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	data, _ := os.ReadFile(filepath.Join(projectDir, sid+".jsonl"))
	for i := 0; i < 3; i++ {
		expected := fmt.Sprintf(`"tag":"t%d"`, i)
		if !strings.Contains(string(data), expected) {
			t.Errorf("Missing append %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// RenameSession additional tests
// ---------------------------------------------------------------------------

func TestRenameSession_NotFound(t *testing.T) {
	_, projectPath, _ := setupTestProject(t)
	sid := generateUUID()
	err := RenameSession(sid, "title", &projectPath)
	if err == nil {
		t.Fatal("Expected error for non-existent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}
}

func TestRenameSession_SearchAllProjects(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	projDir := filepath.Join(projectsDir, sanitizePath("/some/project"))
	os.MkdirAll(projDir, 0755)

	sid := generateUUID()
	fp := makeSessionFile(t, projDir, sid)

	// No directory → searches all projects
	err := RenameSession(sid, "Found Without Dir", nil)
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	data, _ := os.ReadFile(fp)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["customTitle"] != "Found Without Dir" {
		t.Errorf("Expected 'Found Without Dir', got %v", entry["customTitle"])
	}
}

func TestRenameSession_CompactJSON(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := makeSessionFile(t, projectDir, sid)

	RenameSession(sid, "Test Title", &projectPath)

	data, _ := os.ReadFile(fp)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lastLine := lines[len(lines)-1]
	// Should be compact JSON (no extra spaces in key-value pairs)
	if strings.Contains(lastLine, ": ") {
		t.Error("Expected compact JSON format")
	}
}

// ---------------------------------------------------------------------------
// TagSession additional tests
// ---------------------------------------------------------------------------

func TestTagSession_EmptyTagRaises(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	makeSessionFile(t, projectDir, sid)

	for _, tag := range []string{"", "   "} {
		err := TagSession(sid, &tag, &projectPath)
		if err == nil {
			t.Errorf("Expected error for empty/whitespace tag %q", tag)
		}
		if err != nil && !strings.Contains(err.Error(), "non-empty") {
			t.Errorf("Expected 'non-empty' error, got: %v", err)
		}
	}
}

func TestTagSession_NotFound(t *testing.T) {
	_, projectPath, _ := setupTestProject(t)
	sid := generateUUID()
	tag := "hello"
	err := TagSession(sid, &tag, &projectPath)
	if err == nil {
		t.Fatal("Expected error for non-existent session")
	}
}

func TestTagSession_TagTrimmed(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := makeSessionFile(t, projectDir, sid)

	tag := "  trimmed-tag  "
	TagSession(sid, &tag, &projectPath)

	data, _ := os.ReadFile(fp)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["tag"] != "trimmed-tag" {
		t.Errorf("Expected 'trimmed-tag', got %v", entry["tag"])
	}
}

func TestTagSession_UnicodeSanitization(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := makeSessionFile(t, projectDir, sid)

	tag := "hello\u200bworld"
	TagSession(sid, &tag, &projectPath)

	data, _ := os.ReadFile(fp)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["tag"] != "helloworld" {
		t.Errorf("Expected 'helloworld', got %v", entry["tag"])
	}
}

func TestTagSession_RejectsPureInvisible(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	makeSessionFile(t, projectDir, sid)

	tag := "\u200b\u200c\ufeff"
	err := TagSession(sid, &tag, &projectPath)
	if err == nil {
		t.Fatal("Expected error for pure-invisible tag")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("Expected 'non-empty' error, got: %v", err)
	}
}

func TestTagSession_LastWins(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := makeSessionFile(t, projectDir, sid)

	for _, tagVal := range []string{"first", "second", "final"} {
		tag := tagVal
		TagSession(sid, &tag, &projectPath)
	}

	data, _ := os.ReadFile(fp)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[len(lines)-1]), &entry)
	if entry["tag"] != "final" {
		t.Errorf("Expected last tag 'final', got %v", entry["tag"])
	}
}

// ---------------------------------------------------------------------------
// SanitizeUnicode additional tests
// ---------------------------------------------------------------------------

func TestSanitizeUnicode_ZeroWidth(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"zero-width space", "a\u200bb", "ab"},
		{"zero-width non-joiner", "a\u200cb", "ab"},
		{"zero-width joiner", "a\u200db", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeUnicode(tt.input); got != tt.expected {
				t.Errorf("sanitizeUnicode(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeUnicode_DirectionalMarks(t *testing.T) {
	if got := sanitizeUnicode("a\u202ab\u202cc"); got != "abc" {
		t.Errorf("Expected 'abc', got %q", got)
	}
	if got := sanitizeUnicode("a\u2066b\u2069c"); got != "abc" {
		t.Errorf("Expected 'abc', got %q", got)
	}
}

func TestSanitizeUnicode_PrivateUse(t *testing.T) {
	if got := sanitizeUnicode("a\ue000b"); got != "ab" {
		t.Errorf("Expected 'ab', got %q", got)
	}
	if got := sanitizeUnicode("a\uf8ffb"); got != "ab" {
		t.Errorf("Expected 'ab', got %q", got)
	}
}

func TestSanitizeUnicode_Iterative(t *testing.T) {
	result := sanitizeUnicode("a" + strings.Repeat("\u200b", 20) + "b")
	if result != "ab" {
		t.Errorf("Expected 'ab', got %q", result)
	}
}

// ---------------------------------------------------------------------------
// DeleteSession additional tests
// ---------------------------------------------------------------------------

func TestDeleteSession_SearchAllProjects(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	projDir := filepath.Join(projectsDir, sanitizePath("/some/proj"))
	os.MkdirAll(projDir, 0755)

	sid := generateUUID()
	fp := makeSessionFile(t, projDir, sid)

	// No directory → searches all projects
	err := DeleteSession(sid, nil)
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("File should be deleted")
	}
}

// ---------------------------------------------------------------------------
// ForkSession additional tests
// ---------------------------------------------------------------------------

// makeTranscriptSession creates a session with num_turns user/assistant pairs.
// Returns (sessionID, filePath, uuids).
func makeTranscriptSession(t *testing.T, projectDir string, numTurns int) (string, string, []string) {
	t.Helper()
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")

	var uuids []string
	var lines []string

	for i := 0; i < numTurns; i++ {
		userUUID := generateUUID()
		assistantUUID := generateUUID()
		uuids = append(uuids, userUUID, assistantUUID)

		var parentUUID interface{} = nil
		if i > 0 {
			parentUUID = uuids[len(uuids)-3] // previous assistant UUID
		}

		userEntry := map[string]interface{}{
			"type":       "user",
			"uuid":       userUUID,
			"parentUuid": parentUUID,
			"sessionId":  sid,
			"timestamp":  fmt.Sprintf("2024-01-01T00:%02d:00.000Z", i*2),
			"message":    map[string]interface{}{"role": "user", "content": fmt.Sprintf("User turn %d", i+1)},
		}
		assistantEntry := map[string]interface{}{
			"type":       "assistant",
			"uuid":       assistantUUID,
			"parentUuid": userUUID,
			"sessionId":  sid,
			"timestamp":  fmt.Sprintf("2024-01-01T00:%02d:01.000Z", i*2),
			"message":    map[string]interface{}{"role": "assistant", "content": fmt.Sprintf("Assistant turn %d", i+1)},
		}
		b1, _ := json.Marshal(userEntry)
		b2, _ := json.Marshal(assistantEntry)
		lines = append(lines, string(b1), string(b2))
	}

	os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	return sid, fp, uuids
}

func TestForkSession_RemapsUUIDs(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, originalUUIDs := makeTranscriptSession(t, projectDir, 2)

	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forkedPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	data, _ := os.ReadFile(forkedPath)

	origSet := make(map[string]bool)
	for _, u := range originalUUIDs {
		origSet[u] = true
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]interface{}
		json.Unmarshal([]byte(line), &entry)
		entryType, _ := entry["type"].(string)
		if entryType == "user" || entryType == "assistant" {
			uuid, _ := entry["uuid"].(string)
			if origSet[uuid] {
				t.Errorf("Found original UUID %s in forked file", uuid)
			}
			if parentUUID, ok := entry["parentUuid"].(string); ok && parentUUID != "" {
				if origSet[parentUUID] {
					t.Errorf("Found original parentUUID %s in forked file", parentUUID)
				}
			}
		}
	}
}

func TestForkSession_PreservesMessageCount(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, _ := makeTranscriptSession(t, projectDir, 3)

	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	originalMsgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	forkMsgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: result.SessionID,
		Directory: &projectPath,
	})
	if len(forkMsgs) != len(originalMsgs) {
		t.Errorf("Expected %d messages in fork, got %d", len(originalMsgs), len(forkMsgs))
	}
}

func TestForkSession_UpToMessageID(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, uuids := makeTranscriptSession(t, projectDir, 3)

	// Fork up to first assistant (index 1)
	cutoff := uuids[1]
	result, err := ForkSession(&ForkSessionOptions{
		SessionID:     sid,
		Directory:     &projectPath,
		UpToMessageID: &cutoff,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forkMsgs, _ := GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: result.SessionID,
		Directory: &projectPath,
	})
	if len(forkMsgs) != 2 {
		t.Errorf("Expected 2 messages (1 user + 1 assistant), got %d", len(forkMsgs))
	}
}

func TestForkSession_UpToMessageIDNotFound(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, _ := makeTranscriptSession(t, projectDir, 2)

	bogus := generateUUID()
	_, err := ForkSession(&ForkSessionOptions{
		SessionID:     sid,
		Directory:     &projectPath,
		UpToMessageID: &bogus,
	})
	if err == nil {
		t.Fatal("Expected error for unknown cutoff UUID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}
}

func TestForkSession_CustomTitle(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, _ := makeTranscriptSession(t, projectDir, 1)

	title := "My Fork"
	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
		Title:     &title,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	for _, s := range sessions {
		if s.SessionID == result.SessionID {
			if s.CustomTitle == nil || *s.CustomTitle != "My Fork" {
				t.Errorf("Expected custom_title='My Fork', got %v", s.CustomTitle)
			}
			return
		}
	}
	t.Error("Forked session not found in ListSessions")
}

func TestForkSession_DefaultTitleHasSuffix(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, _ := makeTranscriptSession(t, projectDir, 1)

	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	sessions, _ := ListSessions(&ListSessionsOptions{Directory: &projectPath})
	for _, s := range sessions {
		if s.SessionID == result.SessionID {
			if s.CustomTitle == nil || !strings.HasSuffix(*s.CustomTitle, "(fork)") {
				t.Errorf("Expected title ending with '(fork)', got %v", s.CustomTitle)
			}
			return
		}
	}
	t.Error("Forked session not found")
}

func TestForkSession_ForkedFromField(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, _ := makeTranscriptSession(t, projectDir, 2)

	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forkedPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	data, _ := os.ReadFile(forkedPath)

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]interface{}
		json.Unmarshal([]byte(line), &entry)
		entryType, _ := entry["type"].(string)
		if entryType == "user" || entryType == "assistant" {
			forkedFrom, ok := entry["forkedFrom"].(map[string]interface{})
			if !ok {
				t.Errorf("Missing forkedFrom in %s entry", entryType)
				continue
			}
			if forkedFrom["sessionId"] != sid {
				t.Errorf("forkedFrom.sessionId=%v, want %s", forkedFrom["sessionId"], sid)
			}
		}
	}
}

func TestForkSession_SessionIDInEntries(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid, _, _ := makeTranscriptSession(t, projectDir, 2)

	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forkedPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	data, _ := os.ReadFile(forkedPath)

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]interface{}
		json.Unmarshal([]byte(line), &entry)
		if sessionID, _ := entry["sessionId"].(string); sessionID != result.SessionID {
			t.Errorf("Entry has sessionId=%s, want %s", sessionID, result.SessionID)
		}
	}
}

func TestForkSession_ClearsStaleFields(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)
	sid := generateUUID()
	fp := filepath.Join(projectDir, sid+".jsonl")

	entry := map[string]interface{}{
		"type":       "user",
		"uuid":       generateUUID(),
		"parentUuid": nil,
		"sessionId":  sid,
		"timestamp":  "2026-03-01T00:00:00Z",
		"teamName":   "test-team",
		"agentName":  "test-agent",
		"slug":       "test-slug",
		"message":    map[string]interface{}{"role": "user", "content": "Hello"},
	}
	b, _ := json.Marshal(entry)
	os.WriteFile(fp, []byte(string(b)+"\n"), 0644)

	result, err := ForkSession(&ForkSessionOptions{
		SessionID: sid,
		Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forkedPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	data, _ := os.ReadFile(forkedPath)

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e map[string]interface{}
		json.Unmarshal([]byte(line), &e)
		if e["type"] == "user" {
			if _, ok := e["teamName"]; ok {
				t.Error("teamName should be cleared")
			}
			if _, ok := e["agentName"]; ok {
				t.Error("agentName should be cleared")
			}
			if _, ok := e["slug"]; ok {
				t.Error("slug should be cleared")
			}
		}
	}
}

func TestForkSession_SearchAllProjects(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	projDir := filepath.Join(projectsDir, sanitizePath("/search/all"))
	os.MkdirAll(projDir, 0755)

	sid, _, _ := makeTranscriptSession(t, projDir, 1)

	// No directory → searches all
	result, err := ForkSession(&ForkSessionOptions{SessionID: sid})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if result.SessionID == "" {
		t.Error("Expected non-empty forked session ID")
	}
}

// ---------------------------------------------------------------------------
// Cascade delete: subagent directory
// ---------------------------------------------------------------------------

// TestDeleteSession_CascadingSubagentDir verifies that DeleteSession also
// removes the sibling subagent directory (same name without .jsonl).
func TestDeleteSession_CascadingSubagentDir(t *testing.T) {
	_, projectPath, projectDir := setupTestProject(t)

	sid := generateUUID()
	// Create the main session file
	sessionFile := makeSessionFile(t, projectDir, sid)

	// Create the sibling subagent directory with a dummy file inside
	subagentDir := strings.TrimSuffix(sessionFile, ".jsonl")
	if err := os.MkdirAll(subagentDir, 0755); err != nil {
		t.Fatalf("MkdirAll subagentDir: %v", err)
	}
	dummyFile := filepath.Join(subagentDir, "agent-abc.jsonl")
	if err := os.WriteFile(dummyFile, []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile dummy: %v", err)
	}

	err := DeleteSession(sid, &projectPath)
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Main session file must be gone
	if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
		t.Error("Expected session file to be deleted")
	}
	// Subagent directory must also be gone
	if _, err := os.Stat(subagentDir); !os.IsNotExist(err) {
		t.Error("Expected subagent directory to be removed by cascading delete")
	}
}
