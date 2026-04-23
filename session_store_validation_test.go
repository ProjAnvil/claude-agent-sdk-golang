package claude

import (
	"context"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// validateSessionStoreOptions
// ---------------------------------------------------------------------------

// fullStore implements all SessionStore methods.
type fullValidStore struct {
	BaseSessionStore
	sessions []SessionStoreListEntry
}

func (s *fullValidStore) ListSessions(_ context.Context, _ string) ([]SessionStoreListEntry, error) {
	return s.sessions, nil
}

// bareStore only embeds BaseSessionStore (ListSessions returns ErrNotImplemented).
type bareValidStore struct {
	BaseSessionStore
}

func TestValidateSessionStoreOptions_NilOptions(t *testing.T) {
	if err := validateSessionStoreOptions(nil); err != nil {
		t.Fatalf("nil options should not error: %v", err)
	}
}

func TestValidateSessionStoreOptions_NoStore(t *testing.T) {
	opts := &ClaudeAgentOptions{}
	if err := validateSessionStoreOptions(opts); err != nil {
		t.Fatalf("nil store should not error: %v", err)
	}
}

func TestValidateSessionStoreOptions_StoreWithoutContinue(t *testing.T) {
	opts := &ClaudeAgentOptions{
		SessionStore: &bareValidStore{},
	}
	if err := validateSessionStoreOptions(opts); err != nil {
		t.Fatalf("store without continue/resume should not error: %v", err)
	}
}

func TestValidateSessionStoreOptions_ContinueConversationRequiresListSessions(t *testing.T) {
	opts := &ClaudeAgentOptions{
		SessionStore:         &bareValidStore{},
		ContinueConversation: true,
	}
	err := validateSessionStoreOptions(opts)
	if err == nil {
		t.Fatal("expected error when continue_conversation with a store that doesn't implement ListSessions")
	}
	if !errors.Is(err, err) { // just verify it's non-nil
		t.Errorf("unexpected error type: %v", err)
	}
}

func TestValidateSessionStoreOptions_ContinueWithResumeSkipsCheck(t *testing.T) {
	// When Resume is set, ListSessions is never called, so bare store is fine.
	opts := &ClaudeAgentOptions{
		SessionStore:         &bareValidStore{},
		ContinueConversation: true,
		Resume:               "12345678-1234-1234-1234-123456789012",
	}
	if err := validateSessionStoreOptions(opts); err != nil {
		t.Fatalf("continue_conversation with explicit resume should not require ListSessions: %v", err)
	}
}

func TestValidateSessionStoreOptions_FullStoreWithContinue(t *testing.T) {
	opts := &ClaudeAgentOptions{
		SessionStore:         &fullValidStore{},
		ContinueConversation: true,
	}
	if err := validateSessionStoreOptions(opts); err != nil {
		t.Fatalf("full store with continue_conversation should not error: %v", err)
	}
}

func TestValidateSessionStoreOptions_EnableFileCheckpointingForbidden(t *testing.T) {
	opts := &ClaudeAgentOptions{
		SessionStore:            &fullValidStore{},
		EnableFileCheckpointing: true,
	}
	err := validateSessionStoreOptions(opts)
	if err == nil {
		t.Fatal("expected error when session_store + enable_file_checkpointing")
	}
}
