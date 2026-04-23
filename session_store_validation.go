package claude

import (
	"context"
	"errors"
	"fmt"
)

// probeListSessions calls ListSessions with an empty sentinel key to detect
// whether the store provides a real implementation.
// Returns ErrNotImplemented when the store delegates to BaseSessionStore.
// Returns nil (or any other error) when the method is overridden.
func probeListSessions(store SessionStore) error {
	_, err := store.ListSessions(context.Background(), "")
	return err
}

// validateSessionStoreOptions checks for invalid SessionStore option combinations
// and returns a non-nil error if any are found. Called before subprocess spawn
// so misconfiguration fails fast instead of surfacing as a confusing runtime
// error mid-session.
//
// Ported from Python SDK _internal/session_store_validation.py.
func validateSessionStoreOptions(options *ClaudeAgentOptions) error {
	if options == nil || options.SessionStore == nil {
		return nil
	}

	if options.ContinueConversation && options.Resume == "" {
		// When Resume is explicitly set, ListSessions() is never called
		// (resume wins over continue), so a minimal store is fine.
		// For ContinueConversation without an explicit resume, we need
		// ListSessions to find the most-recent session.
		err := probeListSessions(options.SessionStore)
		if errors.Is(err, ErrNotImplemented) {
			return fmt.Errorf(
				"continue_conversation with session_store requires the store to " +
					"implement ListSessions()",
			)
		}
	}

	if options.EnableFileCheckpointing {
		return fmt.Errorf(
			"session_store cannot be combined with enable_file_checkpointing " +
				"(checkpoints are local-disk only and would diverge from the " +
				"mirrored transcript)",
		)
	}

	return nil
}
