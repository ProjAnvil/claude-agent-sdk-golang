package claude

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// Helper: populate an in-memory store with a minimal session transcript
// ---------------------------------------------------------------------------

func makeStoreSession(t *testing.T, store *InMemorySessionStore, sessionID string) {
	t.Helper()
	ctx := context.Background()
	projectKey := ProjectKeyForDirectory("")
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	entries := []SessionStoreEntry{
		{
			"type":    "user",
			"uuid":    generateUUID(),
			"message": map[string]interface{}{"role": "user", "content": "Hello"},
		},
		{
			"type":    "assistant",
			"uuid":    generateUUID(),
			"message": map[string]interface{}{"role": "assistant", "content": "Hi"},
		},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// makeStoreSessionWithUUIDs populates a store session and returns (userUUID, assistantUUID).
func makeStoreSessionWithUUIDs(t *testing.T, store *InMemorySessionStore, sessionID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	projectKey := ProjectKeyForDirectory("")
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	userUUID := generateUUID()
	assistantUUID := generateUUID()
	entries := []SessionStoreEntry{
		{
			"type":       "user",
			"uuid":       userUUID,
			"parentUuid": nil,
			"sessionId":  sessionID,
			"message":    map[string]interface{}{"role": "user", "content": "Hello"},
			"timestamp":  "2024-01-01T00:00:00.000Z",
		},
		{
			"type":       "assistant",
			"uuid":       assistantUUID,
			"parentUuid": userUUID,
			"sessionId":  sessionID,
			"message":    map[string]interface{}{"role": "assistant", "content": "Hi"},
			"timestamp":  "2024-01-01T00:00:01.000Z",
		},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return userUUID, assistantUUID
}

// ---------------------------------------------------------------------------
// RenameSessionViaStore
// ---------------------------------------------------------------------------

func TestRenameSessionViaStore_Basic(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	makeStoreSession(t, store, sessionID)

	if err := RenameSessionViaStore(context.Background(), store, sessionID, "My Custom Title", nil); err != nil {
		t.Fatalf("RenameSessionViaStore: %v", err)
	}

	// Verify a custom-title entry was appended
	projectKey := ProjectKeyForDirectory("")
	entries, _ := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	var found bool
	for _, e := range entries {
		if t2, _ := e["type"].(string); t2 == "custom-title" {
			if ct, _ := e["customTitle"].(string); ct == "My Custom Title" {
				found = true
			}
		}
	}
	if !found {
		t.Error("Expected custom-title entry with 'My Custom Title'")
	}
}

func TestRenameSessionViaStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	err := RenameSessionViaStore(context.Background(), store, "not-a-uuid", "Title", nil)
	if err == nil {
		t.Error("expected error for invalid session ID")
	}
}

func TestRenameSessionViaStore_EmptyTitle(t *testing.T) {
	store := NewInMemorySessionStore()
	err := RenameSessionViaStore(context.Background(), store, generateUUID(), "   ", nil)
	if err == nil {
		t.Error("expected error for empty/whitespace title")
	}
}

// ---------------------------------------------------------------------------
// TagSessionViaStore
// ---------------------------------------------------------------------------

func TestTagSessionViaStore_Basic(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	makeStoreSession(t, store, sessionID)

	tag := "project-alpha"
	if err := TagSessionViaStore(context.Background(), store, sessionID, &tag, nil); err != nil {
		t.Fatalf("TagSessionViaStore: %v", err)
	}

	projectKey := ProjectKeyForDirectory("")
	entries, _ := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	var found bool
	for _, e := range entries {
		if t2, _ := e["type"].(string); t2 == "tag" {
			if tagVal, _ := e["tag"].(string); tagVal == "project-alpha" {
				found = true
			}
		}
	}
	if !found {
		t.Error("Expected tag entry with 'project-alpha'")
	}
}

func TestTagSessionViaStore_ClearTag(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	makeStoreSession(t, store, sessionID)

	// Clearing tag (nil) should append an entry with empty tag value
	if err := TagSessionViaStore(context.Background(), store, sessionID, nil, nil); err != nil {
		t.Fatalf("TagSessionViaStore clear: %v", err)
	}

	projectKey := ProjectKeyForDirectory("")
	entries, _ := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	var found bool
	for _, e := range entries {
		if t2, _ := e["type"].(string); t2 == "tag" {
			found = true
		}
	}
	if !found {
		t.Error("Expected tag entry even when clearing")
	}
}

func TestTagSessionViaStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	tag := "t"
	err := TagSessionViaStore(context.Background(), store, "bad-id", &tag, nil)
	if err == nil {
		t.Error("expected error for invalid session ID")
	}
}

// ---------------------------------------------------------------------------
// DeleteSessionViaStore
// ---------------------------------------------------------------------------

func TestDeleteSessionViaStore_Basic(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	makeStoreSession(t, store, sessionID)

	if err := DeleteSessionViaStore(context.Background(), store, sessionID, nil); err != nil {
		t.Fatalf("DeleteSessionViaStore: %v", err)
	}

	projectKey := ProjectKeyForDirectory("")
	entries, _ := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if len(entries) != 0 {
		t.Errorf("Expected empty entries after delete, got %d", len(entries))
	}
}

func TestDeleteSessionViaStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	err := DeleteSessionViaStore(context.Background(), store, "not-a-uuid", nil)
	if err == nil {
		t.Error("expected error for invalid session ID")
	}
}

func TestDeleteSessionViaStore_NotFound(t *testing.T) {
	store := NewInMemorySessionStore()
	// Non-existent session: InMemorySessionStore.Delete should not error
	if err := DeleteSessionViaStore(context.Background(), store, generateUUID(), nil); err != nil {
		t.Fatalf("DeleteSessionViaStore on missing session should not error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ForkSessionViaStore
// ---------------------------------------------------------------------------

func TestForkSessionViaStore_Basic(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	makeStoreSessionWithUUIDs(t, store, sessionID)

	result, err := ForkSessionViaStore(context.Background(), &ForkSessionViaStoreOptions{
		Store:     store,
		SessionID: sessionID,
		Title:     "My Fork",
	})
	if err != nil {
		t.Fatalf("ForkSessionViaStore: %v", err)
	}
	if result == nil || result.SessionID == "" {
		t.Fatal("Expected non-empty result.SessionID")
	}
	if result.SessionID == sessionID {
		t.Error("Forked session ID should differ from source")
	}

	// Verify new session was written to store
	projectKey := ProjectKeyForDirectory("")
	newEntries, _ := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: result.SessionID})
	if len(newEntries) == 0 {
		t.Error("Expected entries in forked session")
	}

	// Verify forkedFrom is set on entries
	var hasForkRef bool
	for _, e := range newEntries {
		if ff, ok := e["forkedFrom"]; ok && ff != nil {
			hasForkRef = true
		}
	}
	if !hasForkRef {
		t.Error("Expected at least one entry with forkedFrom set")
	}
}

func TestForkSessionViaStore_UpToMessageID(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	userUUID, _ := makeStoreSessionWithUUIDs(t, store, sessionID)

	result, err := ForkSessionViaStore(context.Background(), &ForkSessionViaStoreOptions{
		Store:         store,
		SessionID:     sessionID,
		UpToMessageID: userUUID,
	})
	if err != nil {
		t.Fatalf("ForkSessionViaStore with UpToMessageID: %v", err)
	}
	if result == nil || result.SessionID == "" {
		t.Fatal("Expected non-empty result.SessionID")
	}

	// Should contain only user message + title entry
	projectKey := ProjectKeyForDirectory("")
	newEntries, _ := store.Load(context.Background(), SessionKey{ProjectKey: projectKey, SessionID: result.SessionID})
	// At minimum: 1 user message + 1 title entry
	if len(newEntries) < 2 {
		t.Errorf("Expected at least 2 entries in forked session, got %d", len(newEntries))
	}
}

func TestForkSessionViaStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	_, err := ForkSessionViaStore(context.Background(), &ForkSessionViaStoreOptions{
		Store:     store,
		SessionID: "not-a-uuid",
	})
	if err == nil {
		t.Error("expected error for invalid session ID")
	}
}

func TestForkSessionViaStore_NotFound(t *testing.T) {
	store := NewInMemorySessionStore()
	_, err := ForkSessionViaStore(context.Background(), &ForkSessionViaStoreOptions{
		Store:     store,
		SessionID: generateUUID(),
	})
	if err == nil {
		t.Error("expected error for non-existent session")
	}
}

func TestForkSessionViaStore_NilStore(t *testing.T) {
	_, err := ForkSessionViaStore(context.Background(), &ForkSessionViaStoreOptions{
		Store:     nil,
		SessionID: generateUUID(),
	})
	if err == nil {
		t.Error("expected error for nil store")
	}
}
