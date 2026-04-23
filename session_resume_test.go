package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// applyMaterializedOptions
// ---------------------------------------------------------------------------

func TestApplyMaterializedOptions_SetsConfigDirAndResume(t *testing.T) {
	opts := &ClaudeAgentOptions{
		ContinueConversation: true,
		Env:                  map[string]string{"EXISTING": "val"},
	}
	m := &MaterializedResume{
		ConfigDir:       "/tmp/test-resume-dir",
		ResumeSessionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	result := applyMaterializedOptions(opts, m)

	if result.Env["CLAUDE_CONFIG_DIR"] != "/tmp/test-resume-dir" {
		t.Errorf("CLAUDE_CONFIG_DIR not set correctly: %v", result.Env)
	}
	if result.Env["EXISTING"] != "val" {
		t.Errorf("existing env var was lost: %v", result.Env)
	}
	if result.Resume != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("Resume not set: %v", result.Resume)
	}
	if result.ContinueConversation {
		t.Error("ContinueConversation should be cleared")
	}
	// Original options should be unmodified
	if !opts.ContinueConversation {
		t.Error("original options should be unmodified")
	}
}

func TestApplyMaterializedOptions_NilMaterialized(t *testing.T) {
	opts := &ClaudeAgentOptions{Resume: "original"}
	result := applyMaterializedOptions(opts, nil)
	if result != opts {
		t.Error("nil materialized should return original options unchanged")
	}
}

func TestApplyMaterializedOptions_NilOptions(t *testing.T) {
	m := &MaterializedResume{ConfigDir: "/tmp/x", ResumeSessionID: "id"}
	result := applyMaterializedOptions(nil, m)
	if result != nil {
		t.Error("nil options should return nil")
	}
}

// ---------------------------------------------------------------------------
// materializeResumeSession
// ---------------------------------------------------------------------------

func TestMaterializeResumeSession_NoStore(t *testing.T) {
	opts := &ClaudeAgentOptions{ContinueConversation: true}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("no store should return nil,nil: %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_NoContinueOrResume(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{SessionStore: store}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("no resume/continue should return nil,nil: %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{
		SessionStore: store,
		Resume:       "not-a-uuid",
	}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("invalid UUID resume should return nil,nil: %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_EmptyStore(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{
		SessionStore:        store,
		ContinueConversation: true,
	}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("empty store should return nil,nil (fresh session): %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_WritesJSONL(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	sessionID := "11111111-2222-3333-4444-555555555555"
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(""), SessionID: sessionID}

	entries := []SessionStoreEntry{
		{"type": "user", "content": "hello", "uuid": sessionID},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("Append: %v", err)
	}

	opts := &ClaudeAgentOptions{
		SessionStore: store,
		Resume:       sessionID,
		LoadTimeoutMs: 5000,
	}
	m, err := materializeResumeSession(ctx, opts)
	if err != nil {
		t.Fatalf("materializeResumeSession: %v", err)
	}
	if m == nil {
		t.Fatal("expected MaterializedResume, got nil")
	}
	defer m.Cleanup()

	if m.ResumeSessionID != sessionID {
		t.Errorf("ResumeSessionID: got %q, want %q", m.ResumeSessionID, sessionID)
	}
	if m.ConfigDir == "" {
		t.Error("ConfigDir should be set")
	}

	// Verify the JSONL file was written under projects/<projectKey>/<sessionID>.jsonl
	projectKey := ProjectKeyForDirectory("")
	jsonlPath := filepath.Join(m.ConfigDir, "projects", projectKey, sessionID+".jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("JSONL file not found at %s: %v", jsonlPath, err)
	}
	if len(data) == 0 {
		t.Error("JSONL file is empty")
	}
}

func TestMaterializeResumeSession_CleanupRemovesDir(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	sessionID := "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(""), SessionID: sessionID}
	_ = store.Append(ctx, key, []SessionStoreEntry{{"type": "user", "uuid": sessionID}})

	opts := &ClaudeAgentOptions{SessionStore: store, Resume: sessionID}
	m, err := materializeResumeSession(ctx, opts)
	if err != nil || m == nil {
		t.Fatalf("unexpected: %v, %v", m, err)
	}

	tmpDir := m.ConfigDir
	if err := m.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("temp dir should be removed after Cleanup: %v", err)
	}
}

// ---------------------------------------------------------------------------
// isSafeSubpath
// ---------------------------------------------------------------------------

func TestIsSafeSubpath(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")

	cases := []struct {
		subpath string
		safe    bool
	}{
		{"subagents/foo", true},
		{"subagents/foo/bar", true},
		{"", false},
		{"/absolute/path", false},
		{"../escape", false},
		{"sub/../escape", false},
		{".", false},
		{"..", false},
	}
	for _, tc := range cases {
		got := isSafeSubpath(tc.subpath, sessionDir)
		if got != tc.safe {
			t.Errorf("isSafeSubpath(%q) = %v, want %v", tc.subpath, got, tc.safe)
		}
	}
}

// ---------------------------------------------------------------------------
// writeJSONL
// ---------------------------------------------------------------------------

func TestWriteJSONL_CreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.jsonl")
	entries := []SessionStoreEntry{
		{"type": "user", "content": "hello"},
		{"type": "assistant", "content": "hi"},
	}
	if err := writeJSONL(path, entries); err != nil {
		t.Fatalf("writeJSONL: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := splitLines(string(data))
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
}

func splitLines(s string) []string {
	var lines []string
	for _, l := range splitOnNewline(s) {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func splitOnNewline(s string) []string {
	var result []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}
