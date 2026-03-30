package claude

import (
	"encoding/json"
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
