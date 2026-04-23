package claude

import (
	"context"
	"strings"
	"testing"
)

// ---- InMemorySessionStore ----

func makeKey(sessionID string) SessionKey {
	return SessionKey{ProjectKey: "proj", SessionID: sessionID}
}

func TestInMemorySessionStore_AppendAndLoad(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	key := makeKey("sess1")

	e1 := SessionStoreEntry{"type": "user", "content": "hello"}
	e2 := SessionStoreEntry{"type": "assistant", "content": "hi"}

	if err := store.Append(ctx, key, []SessionStoreEntry{e1, e2}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(entries))
	}
	if entries[0]["content"] != "hello" {
		t.Errorf("entries[0].content mismatch: %v", entries[0])
	}
}

func TestInMemorySessionStore_AppendMultipleCalls(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	key := makeKey("sess-multi")

	if err := store.Append(ctx, key, []SessionStoreEntry{{"n": 1}}); err != nil {
		t.Fatalf("Append(1): %v", err)
	}
	if err := store.Append(ctx, key, []SessionStoreEntry{{"n": 2}, {"n": 3}}); err != nil {
		t.Fatalf("Append(2): %v", err)
	}

	entries, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}
}

func TestInMemorySessionStore_LoadEmpty(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	entries, err := store.Load(ctx, makeKey("nonexistent"))
	if err != nil {
		t.Fatalf("Load on missing key should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Expected empty slice, got %v", entries)
	}
}

func TestInMemorySessionStore_Delete(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	key := makeKey("del-sess")

	_ = store.Append(ctx, key, []SessionStoreEntry{{"x": 1}})
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, _ := store.Load(ctx, key)
	if len(entries) != 0 {
		t.Errorf("Expected empty after delete, got %v", entries)
	}
}

func TestInMemorySessionStore_DeleteNonExistent(t *testing.T) {
	store := NewInMemorySessionStore()
	// Must not error on missing key
	if err := store.Delete(context.Background(), makeKey("ghost")); err != nil {
		t.Errorf("Expected nil error for missing key, got %v", err)
	}
}

func TestInMemorySessionStore_ListSessions(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	proj := "listproj"

	for _, id := range []string{"s1", "s2", "s3"} {
		_ = store.Append(ctx, SessionKey{ProjectKey: proj, SessionID: id}, []SessionStoreEntry{{"v": id}})
	}

	// a different project — should not appear
	_ = store.Append(ctx, SessionKey{ProjectKey: "other", SessionID: "s4"}, []SessionStoreEntry{})

	sessions, err := store.ListSessions(ctx, proj)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("Expected 3 sessions, got %d: %v", len(sessions), sessions)
	}
}

func TestInMemorySessionStore_ListSessionSummaries(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	proj := "sumproj"

	_ = store.Append(ctx, SessionKey{ProjectKey: proj, SessionID: "a"}, []SessionStoreEntry{{"type": "user", "message": map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}}}}})

	summaries, err := store.ListSessionSummaries(ctx, proj)
	if err != nil {
		t.Fatalf("ListSessionSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Errorf("Expected 1 summary, got %d", len(summaries))
	}
}

func TestInMemorySessionStore_ListSubkeys(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()

	key1 := SessionKey{ProjectKey: "p", SessionID: "s1", Subpath: "k1"}
	key2 := SessionKey{ProjectKey: "p", SessionID: "s1", Subpath: "k2"}
	key3 := SessionKey{ProjectKey: "p", SessionID: "s2", Subpath: "k1"}

	_ = store.Append(ctx, key1, []SessionStoreEntry{{"v": 1}})
	_ = store.Append(ctx, key2, []SessionStoreEntry{{"v": 2}})
	_ = store.Append(ctx, key3, []SessionStoreEntry{{"v": 3}})

	listKey := SessionListSubkeysKey{ProjectKey: "p", SessionID: "s1"}
	subkeys, err := store.ListSubkeys(ctx, listKey)
	if err != nil {
		t.Fatalf("ListSubkeys: %v", err)
	}
	if len(subkeys) != 2 {
		t.Errorf("Expected 2 subkeys for s1, got %d: %v", len(subkeys), subkeys)
	}
}

func TestInMemorySessionStore_SizeAndClear(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	_ = store.Append(ctx, makeKey("a"), []SessionStoreEntry{{"x": 1}})
	_ = store.Append(ctx, makeKey("b"), []SessionStoreEntry{{"x": 2}})

	if store.Size() != 2 {
		t.Errorf("Expected Size=2, got %d", store.Size())
	}
	store.Clear()
	if store.Size() != 0 {
		t.Errorf("Expected Size=0 after Clear, got %d", store.Size())
	}
}

// ---- ProjectKeyForDirectory ----

func TestProjectKeyForDirectory_Short(t *testing.T) {
	key := ProjectKeyForDirectory("/home/user/myproject")
	if key == "" {
		t.Error("Expected non-empty key")
	}
	// Short paths should be returned as-is (normalized)
	if !strings.Contains(key, "myproject") {
		t.Errorf("Expected key to contain 'myproject', got %s", key)
	}
}

func TestProjectKeyForDirectory_LongTruncated(t *testing.T) {
	// Build a path > 200 runes
	long := "/" + strings.Repeat("a", 250)
	key := ProjectKeyForDirectory(long)
	if len([]rune(key)) > 220 {
		t.Errorf("Expected truncated key (≤220 runes), got len=%d: %s", len([]rune(key)), key)
	}
	// Should end with a hash suffix
	if !strings.Contains(key, "-") {
		t.Errorf("Expected hash suffix separated by '-', got %s", key)
	}
}

func TestProjectKeyForDirectory_Idempotent(t *testing.T) {
	dir := "/Users/me/project"
	k1 := ProjectKeyForDirectory(dir)
	k2 := ProjectKeyForDirectory(dir)
	if k1 != k2 {
		t.Errorf("ProjectKeyForDirectory not idempotent: %q != %q", k1, k2)
	}
}

// ---- FilePathToSessionKey ----

func TestFilePathToSessionKey_ValidPath(t *testing.T) {
	projectsDir := "/projects"
	// sessionID must be a valid UUID
	validUUID := "12345678-1234-4234-a234-123456789012"
	filePath := "/projects/myproject/" + validUUID + ".jsonl"
	key := FilePathToSessionKey(filePath, projectsDir)
	if key == nil {
		t.Fatal("Expected non-nil SessionKey")
	}
	if key.SessionID != validUUID {
		t.Errorf("SessionID mismatch: %s", key.SessionID)
	}
	if key.ProjectKey == "" {
		t.Error("Expected non-empty ProjectKey")
	}
}

func TestFilePathToSessionKey_InvalidPath(t *testing.T) {
	key := FilePathToSessionKey("/some/unrelated/path.jsonl", "/projects")
	if key != nil {
		t.Errorf("Expected nil for unrelated path, got %+v", key)
	}
}

func TestFilePathToSessionKey_NoJsonlExtension(t *testing.T) {
	key := FilePathToSessionKey("/projects/proj/session.txt", "/projects")
	if key != nil {
		t.Errorf("Expected nil for non-.jsonl file, got %+v", key)
	}
}
