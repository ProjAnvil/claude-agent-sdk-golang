package claude

import (
	"testing"
)

// ---- TestFoldSessionSummary ----

func TestFoldSessionSummary_NilPrev(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "s1", Subpath: "main"}
	entries := []SessionStoreEntry{
		{
			"type": "user",
			"message": map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "hello world"},
				},
			},
			"timestamp": "2024-01-01T00:00:00Z",
		},
	}
	result := FoldSessionSummary(nil, key, entries)
	if result.SessionID != "s1" {
		t.Errorf("SessionID mismatch: %s", result.SessionID)
	}
	if result.Mtime == 0 {
		t.Error("Expected non-zero Mtime")
	}
}

// TestFoldSessionSummary_SetOnce verifies that set-once fields like cwd and
// created_at are not overwritten on subsequent calls.
func TestFoldSessionSummary_SetOnce(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "s1", Subpath: "main"}

	entries1 := []SessionStoreEntry{
		{
			"type":      "system",
			"cwd":       "/original/path",
			"timestamp": "2024-01-01T00:00:00Z",
		},
	}
	result1 := FoldSessionSummary(nil, key, entries1)
	if v, _ := result1.Data["cwd"].(string); v != "/original/path" {
		t.Errorf("Expected cwd=/original/path, got %v", result1.Data["cwd"])
	}

	// Second fold should NOT overwrite cwd
	entries2 := []SessionStoreEntry{
		{
			"type":      "system",
			"cwd":       "/new/path",
			"timestamp": "2024-01-02T00:00:00Z",
		},
	}
	result2 := FoldSessionSummary(&result1, key, entries2)
	if v, _ := result2.Data["cwd"].(string); v != "/original/path" {
		t.Errorf("Expected cwd to stay /original/path (set-once), got %v", result2.Data["cwd"])
	}
}

// TestFoldSessionSummary_LastWins verifies that last-wins fields like
// custom_title are overwritten on subsequent calls.
func TestFoldSessionSummary_LastWins(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "s1", Subpath: "main"}

	entries1 := []SessionStoreEntry{
		{
			"type":        "system",
			"customTitle": "First Title",
			"timestamp":   "2024-01-01T00:00:00Z",
		},
	}
	result1 := FoldSessionSummary(nil, key, entries1)

	entries2 := []SessionStoreEntry{
		{
			"type":        "system",
			"customTitle": "Second Title",
			"timestamp":   "2024-01-02T00:00:00Z",
		},
	}
	result2 := FoldSessionSummary(&result1, key, entries2)
	if v, _ := result2.Data["custom_title"].(string); v != "Second Title" {
		t.Errorf("Expected last-wins custom_title='Second Title', got %v", result2.Data["custom_title"])
	}
}

// TestFoldSessionSummary_FirstPromptSetOnce verifies that first_prompt is set
// on the first user message and not overwritten.
func TestFoldSessionSummary_FirstPromptSetOnce(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "s1", Subpath: "main"}

	entries1 := []SessionStoreEntry{
		{
			"type": "user",
			"message": map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "First question"},
				},
			},
			"timestamp": "2024-01-01T00:00:00Z",
		},
	}
	result1 := FoldSessionSummary(nil, key, entries1)
	fp1, _ := result1.Data["first_prompt"].(string)
	if fp1 == "" {
		t.Error("Expected first_prompt to be set")
	}

	entries2 := []SessionStoreEntry{
		{
			"type": "user",
			"message": map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Second question"},
				},
			},
			"timestamp": "2024-01-02T00:00:00Z",
		},
	}
	result2 := FoldSessionSummary(&result1, key, entries2)
	fp2, _ := result2.Data["first_prompt"].(string)
	if fp2 != fp1 {
		t.Errorf("Expected first_prompt to remain %q (set-once), got %q", fp1, fp2)
	}
}

// TestFoldSessionSummary_MtimeUpdates verifies that Mtime increases with each fold.
func TestFoldSessionSummary_MtimeUpdates(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "s1", Subpath: "main"}

	r1 := FoldSessionSummary(nil, key, []SessionStoreEntry{{"type": "system", "cwd": "/a", "timestamp": "2024-01-01T00:00:00Z"}})
	r2 := FoldSessionSummary(&r1, key, []SessionStoreEntry{{"type": "system", "cwd": "/b", "timestamp": "2024-01-02T00:00:00Z"}})

	if r2.Mtime < r1.Mtime {
		t.Errorf("Expected Mtime to be non-decreasing: r1=%d r2=%d", r1.Mtime, r2.Mtime)
	}
}

// TestFoldSessionSummary_EmptyEntries verifies no panic on empty entry slice.
func TestFoldSessionSummary_EmptyEntries(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: "s1", Subpath: "main"}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Unexpected panic: %v", r)
		}
	}()
	FoldSessionSummary(nil, key, []SessionStoreEntry{})
}
