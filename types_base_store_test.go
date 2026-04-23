package claude

import (
	"context"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// BaseSessionStore tests
// ---------------------------------------------------------------------------

func TestBaseSessionStore_AllMethodsReturnErrNotImplemented(t *testing.T) {
	var base BaseSessionStore
	ctx := context.Background()
	key := SessionKey{ProjectKey: "p", SessionID: "s"}
	lsKey := SessionListSubkeysKey{ProjectKey: "p", SessionID: "s"}

	if err := base.Append(ctx, key, nil); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Append: expected ErrNotImplemented, got %v", err)
	}
	if _, err := base.Load(ctx, key); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Load: expected ErrNotImplemented, got %v", err)
	}
	if _, err := base.ListSessions(ctx, ""); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("ListSessions: expected ErrNotImplemented, got %v", err)
	}
	if _, err := base.ListSessionSummaries(ctx, ""); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("ListSessionSummaries: expected ErrNotImplemented, got %v", err)
	}
	if err := base.Delete(ctx, key); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Delete: expected ErrNotImplemented, got %v", err)
	}
	if _, err := base.ListSubkeys(ctx, lsKey); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("ListSubkeys: expected ErrNotImplemented, got %v", err)
	}
}

func TestErrNotImplemented_IsDistinct(t *testing.T) {
	if ErrNotImplemented == nil {
		t.Fatal("ErrNotImplemented should not be nil")
	}
	if errors.Is(ErrNotImplemented, errors.New("other")) {
		t.Fatal("ErrNotImplemented should not match generic errors")
	}
}

// ---------------------------------------------------------------------------
// Embed BaseSessionStore - only override a subset
// ---------------------------------------------------------------------------

type partialStore struct {
	BaseSessionStore
	data []SessionStoreEntry
}

func (s *partialStore) Append(_ context.Context, _ SessionKey, entries []SessionStoreEntry) error {
	s.data = append(s.data, entries...)
	return nil
}

func (s *partialStore) Load(_ context.Context, _ SessionKey) ([]SessionStoreEntry, error) {
	return s.data, nil
}

func TestBaseSessionStore_EmbedAndOverride(t *testing.T) {
	store := &partialStore{}
	ctx := context.Background()
	key := SessionKey{ProjectKey: "p", SessionID: "s"}

	if err := store.Append(ctx, key, []SessionStoreEntry{{"k": "v"}}); err != nil {
		t.Fatalf("Append: unexpected error: %v", err)
	}

	loaded, err := store.Load(ctx, key)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Load: unexpected result: %v, %v", loaded, err)
	}

	// Unimplemented methods still return ErrNotImplemented
	if _, err := store.ListSessions(ctx, ""); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("ListSessions: expected ErrNotImplemented, got %v", err)
	}
}
