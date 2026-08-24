package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupImportFixture creates a session file (with a subagent transcript and
// metadata sidecar) in a temp CLAUDE_CONFIG_DIR layout and returns the pieces
// needed by ImportSessionToStore.
func setupImportFixture(t *testing.T) (projectPath, projectDir, sessionID string) {
	t.Helper()
	_, projectPath, projectDir = setupSessionTestProject(t)
	sessionID = "550e8400-e29b-41d4-a716-446655440000"

	writeTranscript(t, projectDir, sessionID, []map[string]interface{}{
		makeTranscriptEntry("user", "u1", nil, sessionID, "import me"),
		makeTranscriptEntry("assistant", "a1", strPtr("u1"), sessionID, "ok"),
	})

	subDir := filepath.Join(projectDir, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, subDir, "agent-x", []map[string]interface{}{
		makeTranscriptEntry("user", "su1", nil, sessionID, "sub work"),
	})
	meta := `{"toolUseId":"tu-9","agentType":"helper"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-x.meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectPath, projectDir, sessionID
}

func TestImportSessionToStoreBasic(t *testing.T) {
	projectPath, _, sessionID := setupImportFixture(t)
	store := NewInMemorySessionStore()

	total, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store, nil)
	if err != nil {
		t.Fatalf("ImportSessionToStore failed: %v", err)
	}
	// 2 main entries + 1 subagent entry + 1 metadata entry.
	if total != 4 {
		t.Errorf("expected 4 imported entries, got %d", total)
	}

	projectKey := sanitizePath(projectPath)
	main, err := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil || len(main) != 2 {
		t.Errorf("main transcript: n=%d err=%v", len(main), err)
	}

	sub, err := store.Load(context.Background(), SessionKey{
		ProjectKey: projectKey, SessionID: sessionID, Subpath: "subagents/agent-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawTranscript, sawMeta bool
	for _, e := range sub {
		if e["type"] == "agent_metadata" {
			sawMeta = true
			if e["toolUseId"] != "tu-9" {
				t.Errorf("metadata content lost: %v", e)
			}
		} else {
			sawTranscript = true
		}
	}
	if !sawTranscript || !sawMeta {
		t.Errorf("subagent import incomplete: transcript=%v meta=%v", sawTranscript, sawMeta)
	}
}

func TestImportSessionToStoreExcludeSubagents(t *testing.T) {
	projectPath, _, sessionID := setupImportFixture(t)
	store := NewInMemorySessionStore()

	total, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store,
		&ImportSessionToStoreOptions{ExcludeSubagents: true})
	if err != nil {
		t.Fatalf("ImportSessionToStore failed: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 entries (main only), got %d", total)
	}
}

func TestImportSessionToStoreErrors(t *testing.T) {
	projectPath, _, sessionID := setupImportFixture(t)
	ctx := context.Background()

	// Invalid session ID.
	if _, err := ImportSessionToStore(ctx, "bad", &projectPath, NewInMemorySessionStore(), nil); err == nil {
		t.Error("expected error for invalid session id")
	}

	// Session not found, with and without a directory.
	missing := "660e8400-e29b-41d4-a716-446655440000"
	if _, err := ImportSessionToStore(ctx, missing, &projectPath, NewInMemorySessionStore(), nil); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("not found with directory: %v", err)
	}
	if _, err := ImportSessionToStore(ctx, missing, nil, NewInMemorySessionStore(), nil); err == nil {
		t.Error("expected not found without directory")
	}

	// Store append failure aborts the import.
	if _, err := ImportSessionToStore(ctx, sessionID, &projectPath, &failingAppendStore{&BaseSessionStore{}}, nil); err == nil ||
		!strings.Contains(err.Error(), "failed to import main session file") {
		t.Errorf("append failure: %v", err)
	}
}

func TestImportSessionToStoreBatching(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sessionID := "550e8400-e29b-41d4-a716-446655440000"

	// Five entries plus a malformed line; batch size 2 forces multiple flushes.
	var lines []string
	for i := 0; i < 5; i++ {
		lines = append(lines, compactJSON(map[string]interface{}{
			"type": "user", "uuid": fmt.Sprintf("u%d", i),
			"message": map[string]interface{}{"content": fmt.Sprintf("m%d", i)},
		}))
	}
	lines = append(lines, "not-json-at-all")
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewInMemorySessionStore()
	total, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store,
		&ImportSessionToStoreOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("ImportSessionToStore failed: %v", err)
	}
	if total != 5 {
		t.Errorf("expected 5 entries (malformed skipped), got %d", total)
	}
}

func TestAppendJSONLFileInBatchesErrors(t *testing.T) {
	ctx := context.Background()
	key := SessionKey{ProjectKey: "p", SessionID: "s"}
	store := NewInMemorySessionStore()

	// Missing file.
	if _, err := appendJSONLFileInBatches(ctx, filepath.Join(t.TempDir(), "nope.jsonl"), key, store, 0); err == nil {
		t.Error("expected open error")
	}

	dir := t.TempDir()

	// Append failure at the final flush (single small batch).
	small := filepath.Join(dir, "small.jsonl")
	if err := os.WriteFile(small, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := appendJSONLFileInBatches(ctx, small, key, &failingAppendStore{&BaseSessionStore{}}, 0); err == nil ||
		!strings.Contains(err.Error(), "final batch") {
		t.Errorf("final flush: %v", err)
	}

	// Scanner error: a line exceeding the 4 MB buffer.
	huge := filepath.Join(dir, "huge.jsonl")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Repeat("x", 5*1024*1024) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := appendJSONLFileInBatches(ctx, huge, key, store, 0); err == nil ||
		!strings.Contains(err.Error(), "failed to read") {
		t.Errorf("scanner error: %v", err)
	}
}

// metaAppendFailStore fails only when appending to a subpath key (the
// agent_metadata append), after the transcript import succeeded.
type metaAppendFailStore struct {
	*BaseSessionStore
	saved []SessionStoreEntry
}

func (s *metaAppendFailStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	if key.Subpath != "" {
		for _, e := range entries {
			if e["type"] == "agent_metadata" {
				return fmt.Errorf("meta append exploded")
			}
		}
	}
	s.saved = append(s.saved, entries...)
	return nil
}

func TestImportSessionToStoreMetaAppendError(t *testing.T) {
	projectPath, _, sessionID := setupImportFixture(t)
	store := &metaAppendFailStore{BaseSessionStore: &BaseSessionStore{}}

	_, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store, nil)
	if err == nil || !strings.Contains(err.Error(), "meta entry") {
		t.Errorf("expected meta append error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Third batch
// ---------------------------------------------------------------------------

func TestAppendJSONLFileInBatchesMidBatchError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Batch size 1 forces a flush after the first entry; the failing store
	// errors there (not at the final flush).
	_, err := appendJSONLFileInBatches(context.Background(), path,
		SessionKey{ProjectKey: "p", SessionID: "s"}, &failingAppendStore{&BaseSessionStore{}}, 1)
	if err == nil || !strings.Contains(err.Error(), "append batch") {
		t.Errorf("expected mid-batch append error, got %v", err)
	}
}

func TestImportSessionToStoreSubagentAppendError(t *testing.T) {
	projectPath, _, sessionID := setupImportFixture(t)
	// The main transcript imports fine; the subagent transcript append fails.
	store := &subpathFailStore{BaseSessionStore: &BaseSessionStore{}}
	_, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store, nil)
	if err == nil || !strings.Contains(err.Error(), "subagent file") {
		t.Errorf("expected subagent import error, got %v", err)
	}
}

// subpathFailStore fails appends for any subpath key.
type subpathFailStore struct {
	*BaseSessionStore
}

func (s *subpathFailStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	if key.Subpath != "" {
		return fmt.Errorf("subpath append exploded")
	}
	return nil
}

func TestImportSessionToStoreUnreadableMetaSidecar(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are ineffective as root")
	}
	projectPath, projectDir, sessionID := setupImportFixture(t)
	metaPath := filepath.Join(projectDir, sessionID, "subagents", "agent-x.meta.json")
	if err := os.Chmod(metaPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(metaPath, 0o644) })

	store := NewInMemorySessionStore()
	_, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store, nil)
	if err == nil || !strings.Contains(err.Error(), "meta file") {
		t.Errorf("expected meta read error, got %v", err)
	}
}

func TestImportSessionToStoreUnreadableSubagentsDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are ineffective as root")
	}
	projectPath, projectDir, sessionID := setupImportFixture(t)
	subDir := filepath.Join(projectDir, sessionID, "subagents")
	if err := os.Chmod(subDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

	store := NewInMemorySessionStore()
	_, err := ImportSessionToStore(context.Background(), sessionID, &projectPath, store, nil)
	if err == nil || !strings.Contains(err.Error(), "scan subagents") {
		t.Errorf("expected scan error, got %v", err)
	}
}
