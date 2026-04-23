package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
