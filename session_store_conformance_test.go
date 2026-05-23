package claude

import (
	"context"
	"errors"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// Session Store Conformance Test Suite
// ---------------------------------------------------------------------------
//
// RunSessionStoreConformance asserts the 14 behavioral contracts every
// SessionStore adapter must satisfy. Call it from a standard Go test:
//
//	func TestMyStoreConformance(t *testing.T) {
//	    RunSessionStoreConformance(t, func() SessionStore {
//	        return NewMyStore()
//	    }, nil)
//	}
//
// The makeStore factory is invoked once per contract to provide isolation.
// Contracts for optional methods (ListSessions, ListSessionSummaries, Delete,
// ListSubkeys) are skipped when named in skipOptional or when the store
// returns ErrNotImplemented for that method.

// OptionalMethodName identifies an optional SessionStore method.
type OptionalMethodName string

const (
	OptListSessions         OptionalMethodName = "ListSessions"
	OptListSessionSummaries OptionalMethodName = "ListSessionSummaries"
	OptDelete               OptionalMethodName = "Delete"
	OptListSubkeys          OptionalMethodName = "ListSubkeys"
)

var allOptionalMethods = map[OptionalMethodName]bool{
	OptListSessions:         true,
	OptListSessionSummaries: true,
	OptDelete:               true,
	OptListSubkeys:          true,
}

// RunSessionStoreConformance asserts the 14 SessionStore behavioral contracts.
//
// makeStore is called once per contract to provide test isolation.
// skipOptional is a set of optional method names that should be skipped.
// nil means no methods are skipped (all supported methods are tested).
func RunSessionStoreConformance(
	t *testing.T,
	makeStore func() SessionStore,
	skipOptional map[OptionalMethodName]bool,
) {
	t.Helper()

	if skipOptional == nil {
		skipOptional = make(map[OptionalMethodName]bool)
	}

	// Validate skipOptional names.
	for name := range skipOptional {
		if !allOptionalMethods[name] {
			t.Fatalf("unknown optional method in skipOptional: %q", name)
		}
	}

	// Probe the store to detect which optional methods it supports.
	probe := makeStore()
	hasListSessions := hasOptional(probe, skipOptional, OptListSessions, func() error {
		_, err := probe.ListSessions(context.Background(), "probe")
		return err
	})
	hasListSummaries := hasOptional(probe, skipOptional, OptListSessionSummaries, func() error {
		_, err := probe.ListSessionSummaries(context.Background(), "probe")
		return err
	})
	hasDelete := hasOptional(probe, skipOptional, OptDelete, func() error {
		return probe.Delete(context.Background(), SessionKey{ProjectKey: "probe", SessionID: "probe"})
	})
	hasListSubkeys := hasOptional(probe, skipOptional, OptListSubkeys, func() error {
		_, err := probe.ListSubkeys(context.Background(), SessionListSubkeysKey{ProjectKey: "probe", SessionID: "probe"})
		return err
	})

	// Common key used by most tests.
	key := SessionKey{ProjectKey: "proj", SessionID: "sess"}

	// --- Required: Append + Load -------------------------------------------

	// 1. Append then Load returns same entries in same order.
	t.Run("AppendThenLoad_SameEntriesInOrder", func(t *testing.T) {
		store := makeStore()
		ctx := context.Background()

		entries := []SessionStoreEntry{
			te("uuid", "b", "n", 1),
			te("uuid", "a", "n", 2),
		}
		if err := store.Append(ctx, key, entries); err != nil {
			t.Fatalf("Append: %v", err)
		}
		loaded, err := store.Load(ctx, key)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !entriesEqual(loaded, entries) {
			t.Errorf("Load returned wrong entries.\ngot:  %v\nwant: %v", loaded, entries)
		}
	})

	// 2. Load unknown key returns nil (no entries).
	t.Run("LoadUnknownKey_ReturnsNil", func(t *testing.T) {
		store := makeStore()
		ctx := context.Background()

		loaded, err := store.Load(ctx, SessionKey{ProjectKey: "proj", SessionID: "nope"})
		if err != nil {
			t.Fatalf("Load missing key: %v", err)
		}
		if len(loaded) != 0 {
			t.Errorf("expected nil/empty for unknown key, got %d entries", len(loaded))
		}

		// Also verify that a subpath that was never written returns nil.
		if err := store.Append(ctx, key, []SessionStoreEntry{te("uuid", "x", "n", 1)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		subKey := SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "nope"}
		loaded, err = store.Load(ctx, subKey)
		if err != nil {
			t.Fatalf("Load missing subpath: %v", err)
		}
		if len(loaded) != 0 {
			t.Errorf("expected nil/empty for unknown subpath, got %d entries", len(loaded))
		}
	})

	// 3. Multiple Append calls preserve call order.
	t.Run("MultipleAppend_PreservesCallOrder", func(t *testing.T) {
		store := makeStore()
		ctx := context.Background()

		if err := store.Append(ctx, key, []SessionStoreEntry{te("uuid", "z", "n", 1)}); err != nil {
			t.Fatalf("Append(1): %v", err)
		}
		if err := store.Append(ctx, key, []SessionStoreEntry{
			te("uuid", "a", "n", 2),
			te("uuid", "m", "n", 3),
		}); err != nil {
			t.Fatalf("Append(2): %v", err)
		}
		if err := store.Append(ctx, key, []SessionStoreEntry{te("uuid", "b", "n", 4)}); err != nil {
			t.Fatalf("Append(3): %v", err)
		}

		want := []SessionStoreEntry{
			te("uuid", "z", "n", 1),
			te("uuid", "a", "n", 2),
			te("uuid", "m", "n", 3),
			te("uuid", "b", "n", 4),
		}
		loaded, err := store.Load(ctx, key)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !entriesEqual(loaded, want) {
			t.Errorf("Load returned wrong entries.\ngot:  %v\nwant: %v", loaded, want)
		}
	})

	// 4. Append([]) is a no-op.
	t.Run("AppendEmpty_NoOp", func(t *testing.T) {
		store := makeStore()
		ctx := context.Background()

		want := []SessionStoreEntry{te("uuid", "a", "n", 1)}
		if err := store.Append(ctx, key, want); err != nil {
			t.Fatalf("Append(1): %v", err)
		}
		if err := store.Append(ctx, key, []SessionStoreEntry{}); err != nil {
			t.Fatalf("Append(empty): %v", err)
		}

		loaded, err := store.Load(ctx, key)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !entriesEqual(loaded, want) {
			t.Errorf("Load after empty append returned wrong entries.\ngot:  %v\nwant: %v", loaded, want)
		}
	})

	// 5. Subpath keys are stored independently of main.
	t.Run("SubpathStoredIndependently", func(t *testing.T) {
		store := makeStore()
		ctx := context.Background()

		subKey := SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-1"}

		if err := store.Append(ctx, key, []SessionStoreEntry{te("uuid", "m", "n", 1)}); err != nil {
			t.Fatalf("Append main: %v", err)
		}
		if err := store.Append(ctx, subKey, []SessionStoreEntry{te("uuid", "s", "n", 1)}); err != nil {
			t.Fatalf("Append sub: %v", err)
		}

		mainEntries, err := store.Load(ctx, key)
		if err != nil {
			t.Fatalf("Load main: %v", err)
		}
		if !entriesEqual(mainEntries, []SessionStoreEntry{te("uuid", "m", "n", 1)}) {
			t.Errorf("main entries mismatch: %v", mainEntries)
		}

		subEntries, err := store.Load(ctx, subKey)
		if err != nil {
			t.Fatalf("Load sub: %v", err)
		}
		if !entriesEqual(subEntries, []SessionStoreEntry{te("uuid", "s", "n", 1)}) {
			t.Errorf("sub entries mismatch: %v", subEntries)
		}
	})

	// 6. ProjectKey isolation.
	t.Run("ProjectKeyIsolation", func(t *testing.T) {
		store := makeStore()
		ctx := context.Background()

		keyA := SessionKey{ProjectKey: "A", SessionID: "s1"}
		keyB := SessionKey{ProjectKey: "B", SessionID: "s1"}

		if err := store.Append(ctx, keyA, []SessionStoreEntry{{"type": "x", "from": "A"}}); err != nil {
			t.Fatalf("Append A: %v", err)
		}
		if err := store.Append(ctx, keyB, []SessionStoreEntry{{"type": "x", "from": "B"}}); err != nil {
			t.Fatalf("Append B: %v", err)
		}

		loadedA, err := store.Load(ctx, keyA)
		if err != nil {
			t.Fatalf("Load A: %v", err)
		}
		if !entriesEqual(loadedA, []SessionStoreEntry{{"type": "x", "from": "A"}}) {
			t.Errorf("project A entries mismatch: %v", loadedA)
		}

		loadedB, err := store.Load(ctx, keyB)
		if err != nil {
			t.Fatalf("Load B: %v", err)
		}
		if !entriesEqual(loadedB, []SessionStoreEntry{{"type": "x", "from": "B"}}) {
			t.Errorf("project B entries mismatch: %v", loadedB)
		}

		if hasListSessions {
			sessionsA, err := store.ListSessions(ctx, "A")
			if err != nil {
				t.Fatalf("ListSessions A: %v", err)
			}
			if len(sessionsA) != 1 {
				t.Errorf("expected 1 session for project A, got %d", len(sessionsA))
			}
			sessionsB, err := store.ListSessions(ctx, "B")
			if err != nil {
				t.Fatalf("ListSessions B: %v", err)
			}
			if len(sessionsB) != 1 {
				t.Errorf("expected 1 session for project B, got %d", len(sessionsB))
			}
		}
	})

	// --- Optional: ListSessions -------------------------------------------

	if hasListSessions {
		// 7. ListSessions returns session IDs for project.
		t.Run("ListSessions_ReturnsSessionIDs", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			_ = store.Append(ctx, SessionKey{ProjectKey: "proj", SessionID: "a"}, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, SessionKey{ProjectKey: "proj", SessionID: "b"}, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, SessionKey{ProjectKey: "other", SessionID: "c"}, []SessionStoreEntry{te("n", 1)})

			sessions, err := store.ListSessions(ctx, "proj")
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}

			ids := sessionIDs(sessions)
			sort.Strings(ids)
			wantIDs := []string{"a", "b"}
			if len(ids) != len(wantIDs) || (len(ids) > 0 && (ids[0] != wantIDs[0] || ids[1] != wantIDs[1])) {
				t.Errorf("session IDs mismatch.\ngot:  %v\nwant: %v", ids, wantIDs)
			}

			// Mtime must be epoch-ms; >1e12 rules out epoch-seconds (~2001 in ms).
			for _, s := range sessions {
				if s.Mtime <= int64(1e12) {
					t.Errorf("mtime %d does not look like epoch-ms (>1e12)", s.Mtime)
				}
			}

			// Empty project returns empty.
			never, err := store.ListSessions(ctx, "never-appended-project")
			if err != nil {
				t.Fatalf("ListSessions empty project: %v", err)
			}
			if len(never) != 0 {
				t.Errorf("expected 0 sessions for empty project, got %d", len(never))
			}
		})

		// 8. ListSessions excludes subagent subpaths.
		t.Run("ListSessions_ExcludesSubagentSubpaths", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			mainKey := SessionKey{ProjectKey: "proj", SessionID: "main"}
			subKey := SessionKey{ProjectKey: "proj", SessionID: "main", Subpath: "subagents/agent-1"}

			_ = store.Append(ctx, mainKey, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, subKey, []SessionStoreEntry{te("n", 1)})

			sessions, err := store.ListSessions(ctx, "proj")
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
			ids := sessionIDs(sessions)
			if len(ids) != 1 || ids[0] != "main" {
				t.Errorf("expected only ['main'], got %v", ids)
			}
		})
	}

	// --- Optional: ListSessionSummaries ----------------------------------

	if hasListSummaries {
		// 14. ListSessionSummaries round-trips through FoldSessionSummary.
		t.Run("ListSessionSummaries_RoundTrips", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			summKey := SessionKey{ProjectKey: "proj", SessionID: "summ-sess"}

			_ = store.Append(ctx, summKey, []SessionStoreEntry{
				{"type": "x", "timestamp": "2024-01-01T00:00:00.000Z", "customTitle": "first"},
				{"type": "x", "timestamp": "2024-01-01T00:00:01.000Z"},
			})
			_ = store.Append(ctx, summKey, []SessionStoreEntry{
				{"type": "x", "timestamp": "2024-01-01T00:00:02.000Z", "customTitle": "second"},
			})
			_ = store.Append(ctx, SessionKey{ProjectKey: "other", SessionID: "elsewhere"}, []SessionStoreEntry{
				{"type": "x", "timestamp": "2024-01-01T00:00:00.000Z"},
			})

			summaries, err := store.ListSessionSummaries(ctx, "proj")
			if err != nil {
				t.Fatalf("ListSessionSummaries: %v", err)
			}

			byID := summariesBySessionID(summaries)
			if len(byID) != 1 {
				t.Fatalf("expected exactly 1 summary, got %d: %v", len(byID), byID)
			}
			summ, ok := byID["summ-sess"]
			if !ok {
				t.Fatal("expected summary for session 'summ-sess'")
			}

			// Mtime must be epoch-ms.
			if summ.Mtime <= int64(1e12) {
				t.Errorf("mtime %d does not look like epoch-ms (>1e12)", summ.Mtime)
			}

			// Clock alignment: sidecar mtime >= list_sessions mtime for same session.
			if hasListSessions {
				ls, err := store.ListSessions(ctx, "proj")
				if err != nil {
					t.Fatalf("ListSessions: %v", err)
				}
				lsBySessionID := make(map[string]int64)
				for _, e := range ls {
					lsBySessionID[e.SessionID] = e.Mtime
				}
				if summ.Mtime < lsBySessionID["summ-sess"] {
					t.Errorf("summary mtime %d < list_sessions mtime %d for same session", summ.Mtime, lsBySessionID["summ-sess"])
				}
			}

			// Data is opaque; the contract is that it round-trips into the fold.
			if summ.Data == nil {
				t.Fatal("summary Data should not be nil")
			}
			refolded := FoldSessionSummary(&summ, summKey, []SessionStoreEntry{
				{"type": "x", "timestamp": "2024-01-01T00:00:03.000Z"},
			})
			if refolded.SessionID != "summ-sess" {
				t.Errorf("refolded SessionID = %q, want %q", refolded.SessionID, "summ-sess")
			}
			// The fold preserves prev.Mtime verbatim.
			if refolded.Mtime != summ.Mtime {
				t.Errorf("refolded Mtime = %d, want %d (should preserve prev)", refolded.Mtime, summ.Mtime)
			}

			// Subagent appends must NOT affect the main session's summary.
			subKey := SessionKey{ProjectKey: "proj", SessionID: "summ-sess", Subpath: "subagents/agent-1"}
			_ = store.Append(ctx, subKey, []SessionStoreEntry{
				{"type": "x", "timestamp": "2024-01-01T00:00:09.000Z", "customTitle": "subagent"},
			})
			afterSub, err := store.ListSessionSummaries(ctx, "proj")
			if err != nil {
				t.Fatalf("ListSessionSummaries after subagent: %v", err)
			}
			afterSubByID := summariesBySessionID(afterSub)
			if afterSubByID["summ-sess"].Data["custom_title"] != summ.Data["custom_title"] {
				t.Errorf("subagent append modified main summary data.\ngot:  %v\nwant: %v",
					afterSubByID["summ-sess"].Data["custom_title"], summ.Data["custom_title"])
			}

			// Empty project returns empty.
			never, err := store.ListSessionSummaries(ctx, "never-appended-project")
			if err != nil {
				t.Fatalf("ListSessionSummaries empty project: %v", err)
			}
			if len(never) != 0 {
				t.Errorf("expected 0 summaries for empty project, got %d", len(never))
			}

			// Delete integration.
			if hasDelete {
				_ = store.Delete(ctx, summKey)
				afterDelete, err := store.ListSessionSummaries(ctx, "proj")
				if err != nil {
					t.Fatalf("ListSessionSummaries after delete: %v", err)
				}
				if len(afterDelete) != 0 {
					t.Errorf("expected 0 summaries after delete, got %d", len(afterDelete))
				}
			}
		})
	}

	// --- Optional: Delete -------------------------------------------------

	if hasDelete {
		// 9. Delete main then Load returns nil.
		t.Run("DeleteMain_LoadReturnsNil", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			// Delete on a key that was never written should not error.
			if err := store.Delete(ctx, SessionKey{ProjectKey: "proj", SessionID: "never-written"}); err != nil {
				t.Fatalf("Delete never-written: %v", err)
			}

			if err := store.Append(ctx, key, []SessionStoreEntry{te("n", 1)}); err != nil {
				t.Fatalf("Append: %v", err)
			}
			if err := store.Delete(ctx, key); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			loaded, err := store.Load(ctx, key)
			if err != nil {
				t.Fatalf("Load after delete: %v", err)
			}
			if len(loaded) != 0 {
				t.Errorf("expected nil/empty after delete, got %d entries", len(loaded))
			}
		})

		// 10. Delete main cascades to subkeys.
		t.Run("DeleteMain_CascadesToSubkeys", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			sub1 := SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-1"}
			sub2 := SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-2"}
			otherSession := SessionKey{ProjectKey: "proj", SessionID: "sess2"}
			otherProj := SessionKey{ProjectKey: "other-proj", SessionID: "sess"}

			_ = store.Append(ctx, key, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, sub1, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, sub2, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, otherSession, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, otherProj, []SessionStoreEntry{te("n", 1)})

			if err := store.Delete(ctx, key); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			assertLoadEmpty(t, store, ctx, key, "main after cascade delete")
			assertLoadEmpty(t, store, ctx, sub1, "sub1 after cascade delete")
			assertLoadEmpty(t, store, ctx, sub2, "sub2 after cascade delete")

			// Other session in same project should survive.
			otherLoaded, err := store.Load(ctx, otherSession)
			if err != nil {
				t.Fatalf("Load otherSession: %v", err)
			}
			if len(otherLoaded) != 1 {
				t.Errorf("other session should survive cascade, got %d entries", len(otherLoaded))
			}

			// Same session ID in different project should survive.
			otherProjLoaded, err := store.Load(ctx, otherProj)
			if err != nil {
				t.Fatalf("Load otherProj: %v", err)
			}
			if len(otherProjLoaded) != 1 {
				t.Errorf("other-project session should survive cascade, got %d entries", len(otherProjLoaded))
			}

			if hasListSubkeys {
				lsKey := SessionListSubkeysKey{ProjectKey: "proj", SessionID: "sess"}
				subkeys, err := store.ListSubkeys(ctx, lsKey)
				if err != nil {
					t.Fatalf("ListSubkeys after cascade: %v", err)
				}
				if len(subkeys) != 0 {
					t.Errorf("expected 0 subkeys after cascade, got %v", subkeys)
				}
			}

			if hasListSessions {
				listed, err := store.ListSessions(ctx, "proj")
				if err != nil {
					t.Fatalf("ListSessions after cascade: %v", err)
				}
				for _, s := range listed {
					if s.SessionID == "sess" {
						t.Error("deleted session should not appear in ListSessions")
					}
				}
			}
		})

		// 11. Delete with subpath removes only that subkey.
		t.Run("DeleteSubpath_RemovesOnlyThatSubkey", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			sub1 := SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-1"}
			sub2 := SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-2"}

			_ = store.Append(ctx, key, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, sub1, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, sub2, []SessionStoreEntry{te("n", 1)})

			if err := store.Delete(ctx, sub1); err != nil {
				t.Fatalf("Delete sub1: %v", err)
			}

			assertLoadEmpty(t, store, ctx, sub1, "sub1 after targeted delete")

			// sub2 should survive.
			sub2Loaded, err := store.Load(ctx, sub2)
			if err != nil {
				t.Fatalf("Load sub2: %v", err)
			}
			if len(sub2Loaded) != 1 {
				t.Errorf("sub2 should survive targeted delete, got %d entries", len(sub2Loaded))
			}

			// Main should survive.
			mainLoaded, err := store.Load(ctx, key)
			if err != nil {
				t.Fatalf("Load main: %v", err)
			}
			if len(mainLoaded) != 1 {
				t.Errorf("main should survive targeted delete, got %d entries", len(mainLoaded))
			}

			if hasListSubkeys {
				lsKey := SessionListSubkeysKey{ProjectKey: "proj", SessionID: "sess"}
				subkeys, err := store.ListSubkeys(ctx, lsKey)
				if err != nil {
					t.Fatalf("ListSubkeys: %v", err)
				}
				if len(subkeys) != 1 || subkeys[0] != "subagents/agent-2" {
					t.Errorf("expected ['subagents/agent-2'], got %v", subkeys)
				}
			}
		})
	}

	// --- Optional: ListSubkeys -------------------------------------------

	if hasListSubkeys {
		// 12. ListSubkeys returns subpaths.
		t.Run("ListSubkeys_ReturnsSubpaths", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			lsKey := SessionListSubkeysKey{ProjectKey: "proj", SessionID: "sess"}

			_ = store.Append(ctx, key, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-1"}, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, SessionKey{ProjectKey: "proj", SessionID: "sess", Subpath: "subagents/agent-2"}, []SessionStoreEntry{te("n", 1)})
			_ = store.Append(ctx, SessionKey{ProjectKey: "proj", SessionID: "other-sess", Subpath: "subagents/agent-x"}, []SessionStoreEntry{te("n", 1)})

			subkeys, err := store.ListSubkeys(ctx, lsKey)
			if err != nil {
				t.Fatalf("ListSubkeys: %v", err)
			}
			sort.Strings(subkeys)
			want := []string{"subagents/agent-1", "subagents/agent-2"}
			if len(subkeys) != len(want) {
				t.Errorf("expected %d subkeys, got %d: %v", len(want), len(subkeys), subkeys)
			} else {
				for i, w := range want {
					if subkeys[i] != w {
						t.Errorf("subkeys[%d] = %q, want %q", i, subkeys[i], w)
					}
				}
			}

			// Cross-session subkey should not appear.
			for _, sk := range subkeys {
				if sk == "subagents/agent-x" {
					t.Error("subagents/agent-x from other session should not appear")
				}
			}
		})

		// 13. ListSubkeys excludes main transcript.
		t.Run("ListSubkeys_ExcludesMainTranscript", func(t *testing.T) {
			store := makeStore()
			ctx := context.Background()

			lsKey := SessionListSubkeysKey{ProjectKey: "proj", SessionID: "sess"}

			_ = store.Append(ctx, key, []SessionStoreEntry{te("n", 1)})

			subkeys, err := store.ListSubkeys(ctx, lsKey)
			if err != nil {
				t.Fatalf("ListSubkeys: %v", err)
			}
			if len(subkeys) != 0 {
				t.Errorf("expected 0 subkeys for main-only session, got %v", subkeys)
			}

			// Never-appended session returns empty.
			neverKey := SessionListSubkeysKey{ProjectKey: "proj", SessionID: "never-appended"}
			neverSubkeys, err := store.ListSubkeys(ctx, neverKey)
			if err != nil {
				t.Fatalf("ListSubkeys never-appended: %v", err)
			}
			if len(neverSubkeys) != 0 {
				t.Errorf("expected 0 subkeys for never-appended session, got %v", neverSubkeys)
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

// hasOptional checks whether the store supports a given optional method.
// It returns false if the method is in skipOptional or if calling the method
// returns ErrNotImplemented.
func hasOptional(
	store SessionStore,
	skipOptional map[OptionalMethodName]bool,
	name OptionalMethodName,
	call func() error,
) bool {
	if skipOptional[name] {
		return false
	}
	err := call()
	if errors.Is(err, ErrNotImplemented) {
		return false
	}
	return true
}

// te builds a test entry with "type": "x" plus the given key-value pairs.
// Adapters must treat entries as opaque pass-through blobs.
func te(kv ...interface{}) SessionStoreEntry {
	e := SessionStoreEntry{"type": "x"}
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		e[k] = kv[i+1]
	}
	return e
}

// entriesEqual compares two slices of entries for deep equality.
func entriesEqual(a, b []SessionStoreEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !mapEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// mapEqual compares two maps[string]interface{} for deep equality.
func mapEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if !valueEqual(va, vb) {
			return false
		}
	}
	return true
}

// valueEqual compares two interface{} values for deep equality.
func valueEqual(a, b interface{}) bool {
	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok {
			return false
		}
		return mapEqual(av, bv)
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !valueEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// sessionIDs extracts session IDs from a list of SessionStoreListEntry.
func sessionIDs(entries []SessionStoreListEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.SessionID
	}
	return ids
}

// summariesBySessionID builds a map from SessionID to SessionSummaryEntry.
func summariesBySessionID(summaries []SessionSummaryEntry) map[string]SessionSummaryEntry {
	m := make(map[string]SessionSummaryEntry, len(summaries))
	for _, s := range summaries {
		m[s.SessionID] = s
	}
	return m
}

// assertLoadEmpty asserts that Load returns an empty result for the given key.
func assertLoadEmpty(t *testing.T, store SessionStore, ctx context.Context, key SessionKey, label string) {
	t.Helper()
	loaded, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load %s: %v", label, err)
	}
	if len(loaded) != 0 {
		t.Errorf("%s: expected nil/empty, got %d entries", label, len(loaded))
	}
}

// ---------------------------------------------------------------------------
// Conformance test for InMemorySessionStore
// ---------------------------------------------------------------------------

func TestInMemorySessionStore_Conformance(t *testing.T) {
	RunSessionStoreConformance(t, func() SessionStore {
		return NewInMemorySessionStore()
	}, nil)
}
