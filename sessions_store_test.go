package claude

import (
	"context"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// listingProjectKey returns the project key that ListSessionsFromStore would
// compute for the given directory (empty string == current dir).
func listingProjectKey(dir string) string {
	d := dir
	if d == "" {
		d = "."
	}
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		resolved = d
	}
	return sanitizePath(filepath.Clean(resolved))
}

// populateStoreSessionListing appends a minimal session under the project key
// that ListSessionsFromStore would use for the given directory.
func populateStoreSessionListing(t *testing.T, store *InMemorySessionStore, sessionID, dir string) {
	t.Helper()
	pk := listingProjectKey(dir)
	ctx := context.Background()
	key := SessionKey{ProjectKey: pk, SessionID: sessionID}
	entries := []SessionStoreEntry{
		{
			"type":      "user",
			"uuid":      generateUUID(),
			"sessionId": sessionID,
			"timestamp": "2024-01-01T00:00:00.000Z",
			"message":   map[string]interface{}{"role": "user", "content": "Hello from store"},
		},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("populateStoreSessionListing Append: %v", err)
	}
}

// populateStoreSession appends a minimal user+assistant transcript under projectKey+sessionID.
func populateStoreSession(t *testing.T, store *InMemorySessionStore, sessionID string) {
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
			"timestamp":  "2024-01-01T00:00:00.000Z",
			"message":    map[string]interface{}{"role": "user", "content": "Hello from store"},
		},
		{
			"type":       "assistant",
			"uuid":       assistantUUID,
			"parentUuid": userUUID,
			"sessionId":  sessionID,
			"timestamp":  "2024-01-01T00:00:01.000Z",
			"message":    map[string]interface{}{"role": "assistant", "content": "Response from store"},
		},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("populateStoreSession Append: %v", err)
	}
}

// populateSubagent appends a minimal user+assistant transcript for a subagent.
func populateSubagent(t *testing.T, store *InMemorySessionStore, sessionID, agentID string) {
	t.Helper()
	ctx := context.Background()
	projectKey := ProjectKeyForDirectory("")
	subpath := "subagents/agent-" + agentID
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}
	entries := []SessionStoreEntry{
		{
			"type":      "user",
			"uuid":      generateUUID(),
			"sessionId": sessionID,
			"timestamp": "2024-01-01T00:01:00.000Z",
			"message":   map[string]interface{}{"role": "user", "content": "Subagent task"},
		},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("populateSubagent Append: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ListSessionsFromStore
// ---------------------------------------------------------------------------

func TestListSessionsFromStore_NilStore(t *testing.T) {
	_, err := ListSessionsFromStore(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil opts")
	}
}

func TestListSessionsFromStore_NilStoreField(t *testing.T) {
	_, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{Store: nil})
	if err == nil {
		t.Error("expected error for nil store field")
	}
}

func TestListSessionsFromStore_Empty(t *testing.T) {
	store := NewInMemorySessionStore()
	results, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{Store: store})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(results))
	}
}

func TestListSessionsFromStore_SingleSession(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	// Use the same project-key derivation as ListSessionsFromStore.
	populateStoreSessionListing(t, store, sessionID, "")

	results, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{Store: store})
	if err != nil {
		t.Fatalf("ListSessionsFromStore: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	var found bool
	for _, r := range results {
		if r.SessionID == sessionID {
			found = true
		}
	}
	if !found {
		t.Errorf("session %s not found in results", sessionID)
	}
}

func TestListSessionsFromStore_LimitAndOffset(t *testing.T) {
	store := NewInMemorySessionStore()
	for i := 0; i < 3; i++ {
		// Use the same project-key derivation as ListSessionsFromStore.
		populateStoreSessionListing(t, store, generateUUID(), "")
	}

	limit := 2
	results, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{
		Store: store,
		Limit: &limit,
	})
	if err != nil {
		t.Fatalf("ListSessionsFromStore with Limit: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}

	// Offset beyond total
	results2, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{
		Store:  store,
		Offset: 100,
	})
	if err != nil {
		t.Fatalf("ListSessionsFromStore with high Offset: %v", err)
	}
	if len(results2) != 0 {
		t.Errorf("expected 0 results with high offset, got %d", len(results2))
	}
}

// storeNoListings is a SessionStore that returns ErrNotImplemented for both
// ListSessions and ListSessionSummaries — ListSessionsFromStore should error.
type storeNoListings struct {
	BaseSessionStore
	entries map[string][]SessionStoreEntry
}

func (s *storeNoListings) Append(_ context.Context, key SessionKey, e []SessionStoreEntry) error {
	k := key.ProjectKey + "/" + key.SessionID
	s.entries[k] = append(s.entries[k], e...)
	return nil
}
func (s *storeNoListings) Load(_ context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	return s.entries[key.ProjectKey+"/"+key.SessionID], nil
}

func TestListSessionsFromStore_ErrorWhenNoListMethod(t *testing.T) {
	store := &storeNoListings{entries: map[string][]SessionStoreEntry{}}
	_, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{Store: store})
	if err == nil {
		t.Error("expected error when store has neither ListSessions nor ListSessionSummaries")
	}
}

// ---------------------------------------------------------------------------
// GetSessionInfoFromStore
// ---------------------------------------------------------------------------

func TestGetSessionInfoFromStore_NilStore(t *testing.T) {
	_, err := GetSessionInfoFromStore(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil opts")
	}
}

func TestGetSessionInfoFromStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	result, err := GetSessionInfoFromStore(context.Background(), &GetSessionInfoFromStoreOptions{
		Store:     store,
		SessionID: "not-a-uuid",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for invalid UUID")
	}
}

func TestGetSessionInfoFromStore_Missing(t *testing.T) {
	store := NewInMemorySessionStore()
	result, err := GetSessionInfoFromStore(context.Background(), &GetSessionInfoFromStoreOptions{
		Store:     store,
		SessionID: generateUUID(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for missing session")
	}
}

func TestGetSessionInfoFromStore_Found(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	populateStoreSession(t, store, sessionID)

	result, err := GetSessionInfoFromStore(context.Background(), &GetSessionInfoFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("GetSessionInfoFromStore: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.SessionID != sessionID {
		t.Errorf("expected SessionID %q, got %q", sessionID, result.SessionID)
	}
}

// ---------------------------------------------------------------------------
// GetSessionMessagesFromStore
// ---------------------------------------------------------------------------

func TestGetSessionMessagesFromStore_NilStore(t *testing.T) {
	_, err := GetSessionMessagesFromStore(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil opts")
	}
}

func TestGetSessionMessagesFromStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	msgs, err := GetSessionMessagesFromStore(context.Background(), &GetSessionMessagesFromStoreOptions{
		Store:     store,
		SessionID: "bad-uuid",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for invalid UUID, got %d", len(msgs))
	}
}

func TestGetSessionMessagesFromStore_Empty(t *testing.T) {
	store := NewInMemorySessionStore()
	msgs, err := GetSessionMessagesFromStore(context.Background(), &GetSessionMessagesFromStoreOptions{
		Store:     store,
		SessionID: generateUUID(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestGetSessionMessagesFromStore_Basic(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	populateStoreSession(t, store, sessionID)

	msgs, err := GetSessionMessagesFromStore(context.Background(), &GetSessionMessagesFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("GetSessionMessagesFromStore: %v", err)
	}
	if len(msgs) < 1 {
		t.Fatalf("expected at least 1 message, got %d", len(msgs))
	}
}

func TestGetSessionMessagesFromStore_Limit(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	populateStoreSession(t, store, sessionID)

	limit := 1
	msgs, err := GetSessionMessagesFromStore(context.Background(), &GetSessionMessagesFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
		Limit:     &limit,
	})
	if err != nil {
		t.Fatalf("GetSessionMessagesFromStore with Limit: %v", err)
	}
	if len(msgs) > 1 {
		t.Errorf("expected at most 1 message, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// ListSubagentsFromStore
// ---------------------------------------------------------------------------

func TestListSubagentsFromStore_NilStore(t *testing.T) {
	_, err := ListSubagentsFromStore(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil opts")
	}
}

func TestListSubagentsFromStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	ids, err := ListSubagentsFromStore(context.Background(), &ListSubagentsFromStoreOptions{
		Store:     store,
		SessionID: "bad-uuid",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 IDs for invalid UUID, got %d", len(ids))
	}
}

func TestListSubagentsFromStore_NoSubagents(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	populateStoreSession(t, store, sessionID)

	ids, err := ListSubagentsFromStore(context.Background(), &ListSubagentsFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("ListSubagentsFromStore: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 subagent IDs, got %d", len(ids))
	}
}

func TestListSubagentsFromStore_WithSubagent(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	agentID := "test-agent-001"
	populateStoreSession(t, store, sessionID)
	populateSubagent(t, store, sessionID, agentID)

	ids, err := ListSubagentsFromStore(context.Background(), &ListSubagentsFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("ListSubagentsFromStore: %v", err)
	}
	var found bool
	for _, id := range ids {
		if id == agentID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agent %q in IDs %v", agentID, ids)
	}
}

// noSubkeysStore returns ErrNotImplemented for ListSubkeys
type noSubkeysStore struct{ *InMemorySessionStore }

func (n *noSubkeysStore) ListSubkeys(_ context.Context, _ SessionListSubkeysKey) ([]string, error) {
	return nil, ErrNotImplemented
}

func TestListSubagentsFromStore_StoreNoListSubkeys(t *testing.T) {
	inner := NewInMemorySessionStore()
	store := &noSubkeysStore{inner}
	sessionID := generateUUID()
	_, err := ListSubagentsFromStore(context.Background(), &ListSubagentsFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
	})
	if err == nil {
		t.Error("expected error when store has no ListSubkeys")
	}
}

// ---------------------------------------------------------------------------
// GetSubagentMessagesFromStore
// ---------------------------------------------------------------------------

func TestGetSubagentMessagesFromStore_NilStore(t *testing.T) {
	_, err := GetSubagentMessagesFromStore(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil opts")
	}
}

func TestGetSubagentMessagesFromStore_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	msgs, err := GetSubagentMessagesFromStore(context.Background(), &GetSubagentMessagesFromStoreOptions{
		Store:     store,
		SessionID: "bad",
		AgentID:   "any",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for invalid UUID, got %d", len(msgs))
	}
}

func TestGetSubagentMessagesFromStore_EmptyAgentID(t *testing.T) {
	store := NewInMemorySessionStore()
	_, err := GetSubagentMessagesFromStore(context.Background(), &GetSubagentMessagesFromStoreOptions{
		Store:     store,
		SessionID: generateUUID(),
		AgentID:   "",
	})
	if err == nil {
		t.Error("expected error for empty AgentID")
	}
}

func TestGetSubagentMessagesFromStore_Basic(t *testing.T) {
	store := NewInMemorySessionStore()
	sessionID := generateUUID()
	agentID := "sub-001"
	populateStoreSession(t, store, sessionID)
	populateSubagent(t, store, sessionID, agentID)

	msgs, err := GetSubagentMessagesFromStore(context.Background(), &GetSubagentMessagesFromStoreOptions{
		Store:     store,
		SessionID: sessionID,
		AgentID:   agentID,
	})
	if err != nil {
		t.Fatalf("GetSubagentMessagesFromStore: %v", err)
	}
	if len(msgs) < 1 {
		t.Fatalf("expected at least 1 message, got %d", len(msgs))
	}
}
