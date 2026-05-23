package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// relProjectKey computes the project key used in SessionKey for tests, by
// computing the relative path from projectsDir to projectDir and normalising
// to forward slashes.
func relProjectKey(t *testing.T, projectsDir, projectDir string) string {
	t.Helper()
	rel, err := filepath.Rel(projectsDir, projectDir)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	return filepath.ToSlash(rel)
}

// setupImportProject creates the minimal directory structure for ImportSessionToStore tests.
// Returns (configDir, projectPath, projectsDir, projectDir).
func setupImportProject(t *testing.T) (string, string, string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	configDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	os.MkdirAll(projectsDir, 0755)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	projectPath := filepath.Join(tmpDir, "importproj")
	os.MkdirAll(projectPath, 0755)
	// Resolve symlinks so sanitizePath is consistent
	projectPath, _ = filepath.EvalSymlinks(projectPath)

	sanitized := sanitizePath(projectPath)
	projectDir := filepath.Join(projectsDir, sanitized)
	os.MkdirAll(projectDir, 0755)

	return configDir, projectPath, projectsDir, projectDir
}

// TestImportSessionToStore_Basic verifies that all JSONL lines are imported.
func TestImportSessionToStore_Basic(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hi"}}`,
		`{"type":"user","message":{"role":"user","content":"bye"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}
	if n != 3 {
		t.Errorf("Expected 3 entries imported, got %d", n)
	}
	if store.Size() == 0 {
		t.Error("Expected entries in store after import")
	}
}

// TestImportSessionToStore_InvalidSessionID verifies that bad UUIDs are rejected.
func TestImportSessionToStore_InvalidSessionID(t *testing.T) {
	store := NewInMemorySessionStore()
	dir := "/tmp"
	_, err := ImportSessionToStore(context.Background(), "not-a-uuid", &dir, store, nil)
	if err == nil {
		t.Error("Expected error for invalid session ID")
	}
}

// TestImportSessionToStore_NotFound verifies that missing session file returns error.
func TestImportSessionToStore_NotFound(t *testing.T) {
	_, projectPath, projectsDir, _ := setupImportProject(t)
	store := NewInMemorySessionStore()

	nonexistent := generateUUID()
	_, err := ImportSessionToStore(context.Background(), nonexistent, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err == nil {
		t.Error("Expected error when session file does not exist")
	}
}

// TestImportSessionToStore_EmptyFile verifies that an empty session file
// returns "not found" because findSessionFileWithDir requires a non-empty file.
func TestImportSessionToStore_EmptyFile(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(""), 0644)

	store := NewInMemorySessionStore()
	_, err := ImportSessionToStore(context.Background(), sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	// empty files are not valid sessions — expect an error
	if err == nil {
		t.Error("Expected error for empty session file (file is not a valid session)")
	}
}

// ---------------------------------------------------------------------------
// Batch processing tests
// ---------------------------------------------------------------------------

// TestImportSessionToStore_BatchProcessing verifies that entries are flushed in
// batches by using a small BatchSize and checking that multiple Append calls
// are made to the store.
func TestImportSessionToStore_BatchProcessing(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	// Build a JSONL file with 10 entries.
	var lines []string
	for i := 0; i < 10; i++ {
		entry := map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"role": "user", "content": "msg"},
			"index":   float64(i),
		}
		b, _ := json.Marshal(entry)
		lines = append(lines, string(b))
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	// Use BatchSize=3 so we get multiple flushes: 3+3+3+1.
	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
		BatchSize:   3,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}
	if n != 10 {
		t.Errorf("Expected 10 entries imported, got %d", n)
	}

	// Verify the data is correct by loading from the store.
	key := SessionKey{
		ProjectKey: relProjectKey(t, projectsDir, projectDir),
		SessionID:  sid,
	}
	entries, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 10 {
		t.Errorf("Expected 10 entries in store, got %d", len(entries))
	}
}

// TestImportSessionToStore_BatchProcessing_DefaultBatchSize verifies that
// with default batch size, all entries are still imported correctly.
func TestImportSessionToStore_BatchProcessing_DefaultBatchSize(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	var lines []string
	for i := 0; i < 600; i++ {
		entry := map[string]interface{}{"type": "user", "idx": float64(i)}
		b, _ := json.Marshal(entry)
		lines = append(lines, string(b))
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}
	if n != 600 {
		t.Errorf("Expected 600 entries imported, got %d", n)
	}

	key := SessionKey{
		ProjectKey: relProjectKey(t, projectsDir, projectDir),
		SessionID:  sid,
	}
	entries, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 600 {
		t.Errorf("Expected 600 entries in store, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Subagent import tests
// ---------------------------------------------------------------------------

// TestImportSessionToStore_Subagents verifies that subagent JSONL files under
// <sessionDir>/subagents/ are imported with the correct subkey.
func TestImportSessionToStore_Subagents(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	// Main session file.
	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hi"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// Create subagents directory: <projectDir>/<sid>/subagents/
	sessionDir := filepath.Join(projectDir, sid)
	subagentsDir := filepath.Join(sessionDir, "subagents")
	os.MkdirAll(subagentsDir, 0755)

	// Subagent JSONL file.
	subAgentLines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":"sub1-line1"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"sub1-line2"}}`,
	}
	subFile := filepath.Join(subagentsDir, "agent-abc123.jsonl")
	os.WriteFile(subFile, []byte(strings.Join(subAgentLines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// 2 main + 2 subagent = 4
	if n != 4 {
		t.Errorf("Expected 4 total entries imported, got %d", n)
	}

	// Verify subagent entries are stored under the correct subkey.
	projectKey := relProjectKey(t, projectsDir, projectDir)
	subKey := SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-abc123",
	}
	subEntries, err := store.Load(ctx, subKey)
	if err != nil {
		t.Fatalf("Load subagent: %v", err)
	}
	if len(subEntries) != 2 {
		t.Errorf("Expected 2 subagent entries, got %d", len(subEntries))
	}
}

// TestImportSessionToStore_SubagentsWithMeta verifies that .meta.json sidecar
// files are imported as agent_metadata entries.
func TestImportSessionToStore_SubagentsWithMeta(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	// Main session file.
	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// Create subagents directory.
	sessionDir := filepath.Join(projectDir, sid)
	subagentsDir := filepath.Join(sessionDir, "subagents")
	os.MkdirAll(subagentsDir, 0755)

	// Subagent JSONL file.
	subLines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":"sub-line"}}`,
	}
	subFile := filepath.Join(subagentsDir, "agent-xyz.jsonl")
	os.WriteFile(subFile, []byte(strings.Join(subLines, "\n")+"\n"), 0644)

	// Meta sidecar.
	metaContent := map[string]interface{}{
		"agent_name": "test-agent",
		"model":      "claude-4-sonnet",
		"tools":      []string{"bash", "read"},
	}
	metaBytes, _ := json.Marshal(metaContent)
	metaFile := filepath.Join(subagentsDir, "agent-xyz.meta.json")
	os.WriteFile(metaFile, metaBytes, 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// 1 main + 1 subagent + 1 meta = 3
	if n != 3 {
		t.Errorf("Expected 3 total entries imported, got %d", n)
	}

	// Verify the meta entry.
	projectKey := relProjectKey(t, projectsDir, projectDir)
	subKey := SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-xyz",
	}
	entries, err := store.Load(ctx, subKey)
	if err != nil {
		t.Fatalf("Load subagent: %v", err)
	}
	// 1 transcript entry + 1 meta entry
	if len(entries) != 2 {
		t.Fatalf("Expected 2 subagent entries (transcript + meta), got %d", len(entries))
	}

	// Find the meta entry.
	var metaEntry SessionStoreEntry
	for _, e := range entries {
		if e["type"] == "agent_metadata" {
			metaEntry = e
			break
		}
	}
	if metaEntry == nil {
		t.Fatal("Expected to find an agent_metadata entry")
	}
	if metaEntry["agent_name"] != "test-agent" {
		t.Errorf("Expected agent_name=test-agent, got %v", metaEntry["agent_name"])
	}
	if metaEntry["type"] != "agent_metadata" {
		t.Errorf("Expected type=agent_metadata, got %v", metaEntry["type"])
	}
}

// TestImportSessionToStore_SubagentsNested verifies that nested subagent files
// (e.g. subagents/deep/agent-nested.jsonl) are imported with the correct subpath.
func TestImportSessionToStore_SubagentsNested(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	// Main session file.
	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"main"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// Create nested subagents directory.
	sessionDir := filepath.Join(projectDir, sid)
	nestedDir := filepath.Join(sessionDir, "subagents", "deep")
	os.MkdirAll(nestedDir, 0755)

	subLines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":"nested"}}`,
	}
	subFile := filepath.Join(nestedDir, "agent-nested.jsonl")
	os.WriteFile(subFile, []byte(strings.Join(subLines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// 1 main + 1 subagent = 2
	if n != 2 {
		t.Errorf("Expected 2 total entries imported, got %d", n)
	}

	projectKey := relProjectKey(t, projectsDir, projectDir)
	subKey := SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/deep/agent-nested",
	}
	entries, err := store.Load(ctx, subKey)
	if err != nil {
		t.Fatalf("Load nested subagent: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Expected 1 nested subagent entry, got %d", len(entries))
	}
}

// TestImportSessionToStore_ExcludeSubagents verifies that when
// ExcludeSubagents is true, subagent files are skipped.
func TestImportSessionToStore_ExcludeSubagents(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	// Main session file.
	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hi"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// Create subagents directory with files.
	sessionDir := filepath.Join(projectDir, sid)
	subagentsDir := filepath.Join(sessionDir, "subagents")
	os.MkdirAll(subagentsDir, 0755)

	subLines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":"should-not-import"}}`,
	}
	subFile := filepath.Join(subagentsDir, "agent-skip.jsonl")
	os.WriteFile(subFile, []byte(strings.Join(subLines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir:      projectsDir,
		ExcludeSubagents: true,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// Only the 2 main entries should be imported.
	if n != 2 {
		t.Errorf("Expected 2 entries (subagents skipped), got %d", n)
	}

	// Verify no subagent entries exist in the store.
	projectKey := relProjectKey(t, projectsDir, projectDir)
	subKey := SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-skip",
	}
	entries, _ := store.Load(ctx, subKey)
	if len(entries) != 0 {
		t.Errorf("Expected 0 subagent entries when ExcludeSubagents=true, got %d", len(entries))
	}
}

// TestImportSessionToStore_MultipleSubagents verifies importing multiple
// subagent JSONL files at once.
func TestImportSessionToStore_MultipleSubagents(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	// Main session file.
	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"main"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// Create subagents directory with two files.
	sessionDir := filepath.Join(projectDir, sid)
	subagentsDir := filepath.Join(sessionDir, "subagents")
	os.MkdirAll(subagentsDir, 0755)

	for _, name := range []string{"agent-aaa", "agent-bbb"} {
		lines := []string{
			`{"type":"assistant","message":{"role":"assistant","content":"` + name + `"}}`,
		}
		fp := filepath.Join(subagentsDir, name+".jsonl")
		os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// 1 main + 1 agent-aaa + 1 agent-bbb = 3
	if n != 3 {
		t.Errorf("Expected 3 total entries, got %d", n)
	}

	projectKey := relProjectKey(t, projectsDir, projectDir)

	// Check agent-aaa.
	entriesA, _ := store.Load(ctx, SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-aaa",
	})
	if len(entriesA) != 1 {
		t.Errorf("Expected 1 entry for agent-aaa, got %d", len(entriesA))
	}

	// Check agent-bbb.
	entriesB, _ := store.Load(ctx, SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-bbb",
	})
	if len(entriesB) != 1 {
		t.Errorf("Expected 1 entry for agent-bbb, got %d", len(entriesB))
	}
}

// TestImportSessionToStore_SubagentsNoSubagentsDir verifies that when no
// subagents directory exists, the import still succeeds with just the main file.
func TestImportSessionToStore_SubagentsNoSubagentsDir(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// No subagents directory is created.

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}
	if n != 1 {
		t.Errorf("Expected 1 entry imported, got %d", n)
	}
}

// TestImportSessionToStore_NilOptions verifies that nil options use defaults
// (ExcludeSubagents=false, default batch size).
func TestImportSessionToStore_NilOptions(t *testing.T) {
	_, projectPath, _, projectDir := setupImportProject(t)

	sid := generateUUID()

	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()

	n, err := ImportSessionToStore(context.Background(), sid, &projectPath, store, nil)
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}
	if n != 1 {
		t.Errorf("Expected 1 entry imported with nil options, got %d", n)
	}
}

// TestImportSessionToStore_SubagentsBatchProcessing verifies that subagent
// imports also use batch processing with the specified BatchSize.
func TestImportSessionToStore_SubagentsBatchProcessing(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	// Create subagents directory.
	sessionDir := filepath.Join(projectDir, sid)
	subagentsDir := filepath.Join(sessionDir, "subagents")
	os.MkdirAll(subagentsDir, 0755)

	// Subagent with 7 entries, batch size 3: 3+3+1.
	var subLines []string
	for i := 0; i < 7; i++ {
		entry := map[string]interface{}{
			"type":  "assistant",
			"index": float64(i),
		}
		b, _ := json.Marshal(entry)
		subLines = append(subLines, string(b))
	}
	subFile := filepath.Join(subagentsDir, "agent-batched.jsonl")
	os.WriteFile(subFile, []byte(strings.Join(subLines, "\n")+"\n"), 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
		BatchSize:   3,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// 1 main + 7 subagent = 8
	if n != 8 {
		t.Errorf("Expected 8 total entries, got %d", n)
	}

	projectKey := relProjectKey(t, projectsDir, projectDir)
	subKey := SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-batched",
	}
	entries, _ := store.Load(ctx, subKey)
	if len(entries) != 7 {
		t.Errorf("Expected 7 subagent entries, got %d", len(entries))
	}
}

// TestImportSessionToStore_MetaSidecarOnly verifies that if .meta.json is
// present but the subagent JSONL has no entries, only the meta entry is
// imported for that subagent.
func TestImportSessionToStore_MetaSidecarNoTranscriptEntries(t *testing.T) {
	_, projectPath, projectsDir, projectDir := setupImportProject(t)

	sid := generateUUID()

	mainLines := []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	}
	sessionFile := filepath.Join(projectDir, sid+".jsonl")
	os.WriteFile(sessionFile, []byte(strings.Join(mainLines, "\n")+"\n"), 0644)

	sessionDir := filepath.Join(projectDir, sid)
	subagentsDir := filepath.Join(sessionDir, "subagents")
	os.MkdirAll(subagentsDir, 0755)

	// Subagent JSONL with only blank lines (no valid entries).
	subFile := filepath.Join(subagentsDir, "agent-empty.jsonl")
	os.WriteFile(subFile, []byte("\n\n\n"), 0644)

	// But a meta sidecar exists.
	metaContent := map[string]interface{}{"agent_name": "empty-agent"}
	metaBytes, _ := json.Marshal(metaContent)
	metaFile := filepath.Join(subagentsDir, "agent-empty.meta.json")
	os.WriteFile(metaFile, metaBytes, 0644)

	store := NewInMemorySessionStore()
	ctx := context.Background()

	n, err := ImportSessionToStore(ctx, sid, &projectPath, store, &ImportSessionToStoreOptions{
		ProjectsDir: projectsDir,
	})
	if err != nil {
		t.Fatalf("ImportSessionToStore: %v", err)
	}

	// 1 main + 0 subagent transcript + 1 meta = 2
	if n != 2 {
		t.Errorf("Expected 2 entries (main + meta sidecar), got %d", n)
	}

	projectKey := relProjectKey(t, projectsDir, projectDir)
	subKey := SessionKey{
		ProjectKey: projectKey,
		SessionID:  sid,
		Subpath:    "subagents/agent-empty",
	}
	entries, _ := store.Load(ctx, subKey)
	if len(entries) != 1 {
		t.Fatalf("Expected 1 meta entry for empty subagent, got %d", len(entries))
	}
	if entries[0]["type"] != "agent_metadata" {
		t.Errorf("Expected agent_metadata entry, got type=%v", entries[0]["type"])
	}
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

// TestCollectJSONLFiles verifies that collectJSONLFiles returns all .jsonl
// files recursively.
func TestCollectJSONLFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure.
	os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)

	os.WriteFile(filepath.Join(tmpDir, "a.jsonl"), []byte("line\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.jsonl"), []byte("line\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "sub", "c.jsonl"), []byte("line\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "ignore.txt"), []byte("not jsonl\n"), 0644)

	files, err := collectJSONLFiles(tmpDir)
	if err != nil {
		t.Fatalf("collectJSONLFiles: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("Expected 3 .jsonl files, got %d", len(files))
	}

	// Verify all returned paths end with .jsonl.
	for _, f := range files {
		if !strings.HasSuffix(f, ".jsonl") {
			t.Errorf("Expected .jsonl suffix, got %s", f)
		}
	}
}

// TestCollectJSONLFiles_NonexistentDir verifies that a nonexistent directory
// returns an error.
func TestCollectJSONLFiles_NonexistentDir(t *testing.T) {
	_, err := collectJSONLFiles("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}
}

// TestMergeImportOptions verifies option merging behavior.
func TestMergeImportOptions(t *testing.T) {
	t.Run("nil returns defaults", func(t *testing.T) {
		o := mergeImportOptions(nil)
		if o.ExcludeSubagents {
			t.Error("Expected ExcludeSubagents=false by default")
		}
		if o.BatchSize != maxImportBatchEntries {
			t.Errorf("Expected BatchSize=%d, got %d", maxImportBatchEntries, o.BatchSize)
		}
	})

	t.Run("zero fields fall back to defaults", func(t *testing.T) {
		o := mergeImportOptions(&ImportSessionToStoreOptions{})
		if o.ExcludeSubagents {
			t.Error("Expected ExcludeSubagents=false when zero")
		}
		if o.BatchSize != maxImportBatchEntries {
			t.Errorf("Expected BatchSize=%d, got %d", maxImportBatchEntries, o.BatchSize)
		}
	})

	t.Run("explicit values are used", func(t *testing.T) {
		o := mergeImportOptions(&ImportSessionToStoreOptions{
			ExcludeSubagents: true,
			BatchSize:        100,
			ProjectsDir:      "/tmp/projects",
		})
		if !o.ExcludeSubagents {
			t.Error("Expected ExcludeSubagents=true")
		}
		if o.BatchSize != 100 {
			t.Errorf("Expected BatchSize=100, got %d", o.BatchSize)
		}
		if o.ProjectsDir != "/tmp/projects" {
			t.Errorf("Expected ProjectsDir=/tmp/projects, got %s", o.ProjectsDir)
		}
	})
}
