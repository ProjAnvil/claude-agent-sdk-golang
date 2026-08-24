package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// deriveTitleFromEntries
// ---------------------------------------------------------------------------

func TestDeriveTitleFromEntriesCoverage(t *testing.T) {
	tests := []struct {
		name    string
		entries []SessionStoreEntry
		want    string
	}{
		{"no entries", nil, ""},
		{
			"no title entries",
			[]SessionStoreEntry{{"type": "user"}},
			"",
		},
		{
			"custom title",
			[]SessionStoreEntry{{"type": "custom-title", "customTitle": "Mine"}},
			"Mine",
		},
		{
			"ai title",
			[]SessionStoreEntry{{"type": "ai-title", "aiTitle": "Generated"}},
			"Generated",
		},
		{
			"custom wins over ai",
			[]SessionStoreEntry{
				{"type": "ai-title", "aiTitle": "Generated"},
				{"type": "custom-title", "customTitle": "Mine"},
			},
			"Mine",
		},
		{
			"ai title does not override custom",
			[]SessionStoreEntry{
				{"type": "custom-title", "customTitle": "Mine"},
				{"type": "ai-title", "aiTitle": "Generated"},
			},
			"Mine",
		},
		{
			"last custom title wins",
			[]SessionStoreEntry{
				{"type": "custom-title", "customTitle": "First"},
				{"type": "custom-title", "customTitle": "Second"},
			},
			"Second",
		},
		{
			"empty titles ignored",
			[]SessionStoreEntry{
				{"type": "custom-title", "customTitle": ""},
				{"type": "ai-title", "aiTitle": 42},
			},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveTitleFromEntries(tt.entries); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DeleteSessionViaStore
// ---------------------------------------------------------------------------

// failingDeleteStore implements Delete with a hard failure.
type failingDeleteStore struct {
	*BaseSessionStore
}

func (s *failingDeleteStore) Delete(ctx context.Context, key SessionKey) error {
	return fmt.Errorf("delete exploded")
}

func TestDeleteSessionViaStoreEdgeCases(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// WORM store (Delete unimplemented): silently skipped.
	worm := &BaseSessionStore{}
	if err := DeleteSessionViaStore(ctx, worm, sid, nil); err != nil {
		t.Errorf("WORM store should be a no-op, got %v", err)
	}

	// Real delete failure propagates.
	if err := DeleteSessionViaStore(ctx, &failingDeleteStore{&BaseSessionStore{}}, sid, nil); err == nil ||
		!strings.Contains(err.Error(), "delete exploded") {
		t.Errorf("expected delete error, got %v", err)
	}

	// Directory is used to derive the project key.
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	if err := store.Append(ctx, SessionKey{
		ProjectKey: ProjectKeyForDirectory(dir), SessionID: sid,
	}, []SessionStoreEntry{{"type": "user"}}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSessionViaStore(ctx, store, sid, &dir); err != nil {
		t.Fatalf("DeleteSessionViaStore failed: %v", err)
	}
	entries, err := store.Load(ctx, SessionKey{ProjectKey: ProjectKeyForDirectory(dir), SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Error("session should be deleted from the store")
	}
}

// ---------------------------------------------------------------------------
// findSessionFileWithDir
// ---------------------------------------------------------------------------

func TestFindSessionFileWithDirCoverage(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, wantPath := makeTestSessionFile(t, projectDir, withFirstPrompt("locate me"))

	// Found with directory.
	path, dir := findSessionFileWithDir(sid, &projectPath)
	if path != wantPath || dir != projectDir {
		t.Errorf("with directory: got (%q, %q), want (%q, %q)", path, dir, wantPath, projectDir)
	}

	// Found without directory (scan all projects).
	path, dir = findSessionFileWithDir(sid, nil)
	if path != wantPath {
		t.Errorf("without directory: got (%q, %q)", path, dir)
	}

	// Missing session: empty results both ways.
	missing := "550e8400-e29b-41d4-a716-446655440000"
	if path, _ := findSessionFileWithDir(missing, &projectPath); path != "" {
		t.Errorf("missing with directory: got %q", path)
	}
	if path, _ := findSessionFileWithDir(missing, nil); path != "" {
		t.Errorf("missing without directory: got %q", path)
	}

	// findSessionFile wrapper agrees.
	if got := findSessionFile(sid, &projectPath); got != wantPath {
		t.Errorf("findSessionFile: got %q", got)
	}
}

func TestFindSessionFileWithDirWorktreeFallback(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	configDir := filepath.Join(tmp, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	mainDir := filepath.Join(tmp, "repo")
	wtDir := initGitRepoWithWorktree(t, mainDir)

	// The session file lives only in the worktree's project dir.
	wtProjectDir := filepath.Join(projectsDir, sanitizePath(wtDir))
	if err := os.MkdirAll(wtProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid, wantPath := makeTestSessionFile(t, wtProjectDir, withFirstPrompt("worktree only"))

	path, dir := findSessionFileWithDir(sid, &mainDir)
	if path != wantPath || dir != wtProjectDir {
		t.Errorf("worktree fallback: got (%q, %q), want (%q, %q)", path, dir, wantPath, wtProjectDir)
	}
}

// ---------------------------------------------------------------------------
// appendToSession
// ---------------------------------------------------------------------------

func TestAppendToSessionCoverage(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, fp := makeTestSessionFile(t, projectDir, withFirstPrompt("append target"))

	// Append with directory.
	line := `{"type":"summary","summary":"appended"}` + "\n"
	if err := appendToSession(sid, line, &projectPath); err != nil {
		t.Fatalf("append with directory failed: %v", err)
	}
	data, _ := os.ReadFile(fp)
	if !strings.HasSuffix(string(data), line) {
		t.Error("line was not appended (with directory)")
	}

	// Append without directory (search all projects).
	line2 := `{"type":"summary","summary":"appended-2"}` + "\n"
	if err := appendToSession(sid, line2, nil); err != nil {
		t.Fatalf("append without directory failed: %v", err)
	}
	data, _ = os.ReadFile(fp)
	if !strings.HasSuffix(string(data), line2) {
		t.Error("line was not appended (without directory)")
	}

	// Missing session errors both ways.
	missing := "550e8400-e29b-41d4-a716-446655440000"
	if err := appendToSession(missing, line, &projectPath); err == nil {
		t.Error("expected error for missing session (with directory)")
	}
	if err := appendToSession(missing, line, nil); err == nil {
		t.Error("expected error for missing session (without directory)")
	}
}

// ---------------------------------------------------------------------------
// parseForkTranscript
// ---------------------------------------------------------------------------

func TestParseForkTranscriptCoverage(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	content := strings.Join([]string{
		`{"type":"user","uuid":"u1"}`,
		`not-json`,
		`{"type":"user"}`, // no uuid: skipped
		`{"type":"unknown-type","uuid":"u2"}`,
		`{"type":"content-replacement","sessionId":"` + sid + `","replacements":[{"a":1}]}`,
		`{"type":"content-replacement","sessionId":"other-session","replacements":[{"b":2}]}`,
		`{"type":"content-replacement","sessionId":"` + sid + `","replacements":"not-a-list"}`,
	}, "\n")

	transcript, replacements := parseForkTranscript([]byte(content), sid)
	if len(transcript) != 1 || transcript[0]["uuid"] != "u1" {
		t.Errorf("unexpected transcript: %v", transcript)
	}
	if len(replacements) != 1 {
		t.Errorf("expected 1 replacement for matching session, got %v", replacements)
	}
}

// ---------------------------------------------------------------------------
// Second batch: ForkSession, ForkSessionViaStore, Rename/Tag error paths
// ---------------------------------------------------------------------------

func TestForkSessionErrorPaths(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"

	if _, err := ForkSession(nil); err == nil {
		t.Error("expected error for nil options")
	}
	if _, err := ForkSession(&ForkSessionOptions{SessionID: "bad"}); err == nil {
		t.Error("expected error for invalid session id")
	}
	badUpTo := "not-a-uuid"
	if _, err := ForkSession(&ForkSessionOptions{SessionID: sid, UpToMessageID: &badUpTo}); err == nil {
		t.Error("expected error for invalid up_to_message_id")
	}

	_, projectPath, projectDir := setupSessionTestProject(t)

	// Not found, with and without directory.
	if _, err := ForkSession(&ForkSessionOptions{SessionID: sid, Directory: &projectPath}); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("not found with directory: %v", err)
	}
	if _, err := ForkSession(&ForkSessionOptions{SessionID: sid}); err == nil {
		t.Error("expected not found without directory")
	}

	// Sidechain-only session has no messages to fork.
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		{"type": "user", "uuid": "u1", "isSidechain": true},
	})
	if _, err := ForkSession(&ForkSessionOptions{SessionID: sid, Directory: &projectPath}); err == nil ||
		!strings.Contains(err.Error(), "no messages to fork") {
		t.Errorf("sidechain only: %v", err)
	}

	// Progress-only session has no writable messages.
	sid2 := "660e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid2, []map[string]interface{}{
		{"type": "progress", "uuid": "p1"},
	})
	if _, err := ForkSession(&ForkSessionOptions{SessionID: sid2, Directory: &projectPath}); err == nil ||
		!strings.Contains(err.Error(), "no messages to fork") {
		t.Errorf("progress only: %v", err)
	}

	// up_to_message_id not present in the transcript.
	sid3 := "770e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid3, []map[string]interface{}{
		makeTranscriptEntry("user", "u1", nil, sid3, "hello"),
	})
	missingUpTo := "880e8400-e29b-41d4-a716-446655440000"
	if _, err := ForkSession(&ForkSessionOptions{
		SessionID: sid3, Directory: &projectPath, UpToMessageID: &missingUpTo,
	}); err == nil || !strings.Contains(err.Error(), "not found in session") {
		t.Errorf("upTo not found: %v", err)
	}
}

func TestForkSessionTitleAndParentResolution(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Transcript with a custom title, a progress entry in the parent chain,
	// and a logicalParentUuid to remap.
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		makeTranscriptEntry("user", "u1", nil, sid, "first prompt"),
		{"type": "progress", "uuid": "p1", "parentUuid": "u1", "sessionId": sid},
		{
			"type": "assistant", "uuid": "a1", "parentUuid": "p1", "sessionId": sid,
			"logicalParentUuid": "u1",
			"message":           map[string]interface{}{"content": "reply"},
		},
		{"type": "custom-title", "customTitle": "My Title", "sessionId": sid},
	})

	result, err := ForkSession(&ForkSessionOptions{SessionID: sid, Directory: &projectPath})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}
	if result.SessionID == sid {
		t.Error("fork should have a fresh session id")
	}

	forkedPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	data, err := os.ReadFile(forkedPath)
	if err != nil {
		t.Fatalf("forked file missing: %v", err)
	}
	content := string(data)

	// Title derived from the source's customTitle.
	if !strings.Contains(content, "My Title (fork)") {
		t.Errorf("derived title missing: %s", content)
	}
	// The progress entry is not written but the chain is re-linked past it:
	// a1's parentUuid must point at the remapped u1, not p1.
	var a1 map[string]interface{}
	var u1NewUUID string
	for _, line := range strings.Split(content, "\n") {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if src, ok := entry["forkedFrom"].(map[string]interface{}); ok {
			switch src["messageUuid"] {
			case "u1":
				u1NewUUID, _ = entry["uuid"].(string)
			case "a1":
				a1 = entry
			}
		}
	}
	if u1NewUUID == "" || a1 == nil {
		t.Fatalf("forked entries not found: u1=%q a1=%v", u1NewUUID, a1)
	}
	if a1["parentUuid"] != u1NewUUID {
		t.Errorf("progress ancestor not skipped: parentUuid=%v want %q", a1["parentUuid"], u1NewUUID)
	}
	if a1["logicalParentUuid"] != u1NewUUID {
		t.Errorf("logicalParentUuid not remapped: %v", a1["logicalParentUuid"])
	}

	// Without any title information, the first prompt seeds the fork title.
	sid2 := "660e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid2, []map[string]interface{}{
		makeTranscriptEntry("user", "u1", nil, sid2, "the seed prompt"),
	})
	result2, err := ForkSession(&ForkSessionOptions{SessionID: sid2, Directory: &projectPath})
	if err != nil {
		t.Fatalf("ForkSession 2 failed: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(projectDir, result2.SessionID+".jsonl"))
	if !strings.Contains(string(data2), "the seed prompt (fork)") {
		t.Errorf("first-prompt title missing: %s", data2)
	}
}

func TestAppendToSessionViaWorktree(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	configDir := filepath.Join(tmp, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	mainDir := filepath.Join(tmp, "repo")
	wtDir := initGitRepoWithWorktree(t, mainDir)

	// The session lives only in the linked worktree's project dir.
	wtProjectDir := filepath.Join(projectsDir, sanitizePath(wtDir))
	if err := os.MkdirAll(wtProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid, fp := makeTestSessionFile(t, wtProjectDir, withFirstPrompt("wt target"))

	line := `{"type":"summary","summary":"via worktree"}` + "\n"
	if err := appendToSession(sid, line, &mainDir); err != nil {
		t.Fatalf("appendToSession via worktree failed: %v", err)
	}
	data, _ := os.ReadFile(fp)
	if !strings.HasSuffix(string(data), line) {
		t.Error("line not appended via worktree")
	}
}

// failingAppendStore implements Append with a hard failure.
type failingAppendStore struct {
	*BaseSessionStore
}

func (s *failingAppendStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	return fmt.Errorf("append exploded")
}

func TestRenameAndTagViaStoreErrors(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"
	dir := t.TempDir()
	store := &failingAppendStore{&BaseSessionStore{}}

	if err := RenameSessionViaStore(ctx, store, sid, "title", &dir); err == nil ||
		!strings.Contains(err.Error(), "append exploded") {
		t.Errorf("rename: %v", err)
	}
	tag := "v1"
	if err := TagSessionViaStore(ctx, store, sid, &tag, &dir); err == nil ||
		!strings.Contains(err.Error(), "append exploded") {
		t.Errorf("tag: %v", err)
	}
}

func TestForkSessionViaStoreBranches(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Invalid up_to_message_id.
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: NewInMemorySessionStore(), SessionID: sid, UpToMessageID: "bad",
	}); err == nil {
		t.Error("expected error for invalid up_to_message_id")
	}

	// Load failure.
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: &failingLoadStore{&BaseSessionStore{}}, SessionID: sid,
	}); err == nil {
		t.Error("expected load error")
	}

	// Entries that cannot be serialized produce no JSONL.
	unmarshalable := &unmarshalableLoadStore{
		BaseSessionStore: &BaseSessionStore{},
		entries:          []SessionStoreEntry{{"bad": func() {}}},
	}
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: unmarshalable, SessionID: sid,
	}); err == nil || !strings.Contains(err.Error(), "no entries") {
		t.Errorf("unmarshalable: %v", err)
	}

	// All-sidechain transcript has nothing to fork.
	sidechainOnly := &unmarshalableLoadStore{
		BaseSessionStore: &BaseSessionStore{},
		entries: []SessionStoreEntry{
			{"type": "user", "uuid": "u1", "isSidechain": true},
		},
	}
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: sidechainOnly, SessionID: sid,
	}); err == nil || !strings.Contains(err.Error(), "no messages to fork") {
		t.Errorf("sidechain only: %v", err)
	}

	// Progress-only transcript has nothing writable.
	progressOnly := &unmarshalableLoadStore{
		BaseSessionStore: &BaseSessionStore{},
		entries: []SessionStoreEntry{
			{"type": "progress", "uuid": "p1"},
		},
	}
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: progressOnly, SessionID: sid,
	}); err == nil || !strings.Contains(err.Error(), "no messages to fork") {
		t.Errorf("progress only: %v", err)
	}

	// up_to_message_id missing from the transcript.
	store := NewInMemorySessionStore()
	projectKey := ProjectKeyForDirectory("")
	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sid}, []SessionStoreEntry{
		{"type": "user", "uuid": "u1", "message": map[string]interface{}{"content": "hi"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: store, SessionID: sid, UpToMessageID: "660e8400-e29b-41d4-a716-446655440000",
	}); err == nil || !strings.Contains(err.Error(), "not found in session") {
		t.Errorf("upTo missing: %v", err)
	}

	// Title derived from the source's ai-title entries; parent missing from
	// the transcript index; non-string logicalParentUuid passed through.
	source := []SessionStoreEntry{
		{"type": "ai-title", "aiTitle": "AI Name"},
		{"type": "user", "uuid": "u1", "parentUuid": "missing", "logicalParentUuid": 42,
			"message": map[string]interface{}{"content": "hi"}},
	}
	store2 := NewInMemorySessionStore()
	if err := store2.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sid}, source); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: store2, SessionID: sid,
	})
	if err != nil {
		t.Fatalf("ForkSessionViaStore failed: %v", err)
	}
	forked, err := store2.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: result.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var sawTitle, sawPassthrough bool
	for _, e := range forked {
		if t, _ := e["type"].(string); t == "custom-title" {
			if ct, _ := e["customTitle"].(string); ct == "AI Name (fork)" {
				sawTitle = true
			}
			continue
		}
		// Parent UUID missing from the index resolves to nil; the non-string
		// logicalParentUuid passes through unchanged.
		if e["parentUuid"] != nil {
			t.Errorf("missing parent should resolve to nil, got %v", e["parentUuid"])
		}
		if e["logicalParentUuid"] == float64(42) {
			sawPassthrough = true
		}
	}
	if !sawTitle {
		t.Error("derived ai-title fork title not found")
	}
	if !sawPassthrough {
		t.Error("non-string logicalParentUuid should pass through")
	}
}

// unmarshalableLoadStore returns scripted entries from Load.
type unmarshalableLoadStore struct {
	*BaseSessionStore
	entries []SessionStoreEntry
}

func (s *unmarshalableLoadStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	return s.entries, nil
}

// ---------------------------------------------------------------------------
// Third batch
// ---------------------------------------------------------------------------

func TestDeleteSessionRemoveError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are ineffective as root")
	}
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, _ := makeTestSessionFile(t, projectDir, withFirstPrompt("delete me"))

	// A read-only project dir makes os.Remove fail with a non-NotExist error.
	if err := os.Chmod(projectDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(projectDir, 0o755) })

	err := DeleteSession(sid, &projectPath)
	if err == nil || strings.Contains(err.Error(), "not found") {
		t.Errorf("expected remove error, got %v", err)
	}
}

func TestForkSessionParentAndReplacementEdges(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Entry with a parent UUID missing from the transcript, a non-string
	// logicalParentUuid passed through, and a content-replacement record.
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		{
			"type": "user", "uuid": "u1", "parentUuid": "missing",
			"logicalParentUuid": 42, "sessionId": sid,
			"message": map[string]interface{}{"content": "hi"},
		},
		{
			"type":      "content-replacement",
			"sessionId": sid,
			"replacements": []interface{}{
				map[string]interface{}{"uuid": "u1", "content": "redacted"},
			},
		},
	})

	result, err := ForkSession(&ForkSessionOptions{SessionID: sid, Directory: &projectPath})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(projectDir, result.SessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "content-replacement") {
		t.Error("content-replacement entry not carried into the fork")
	}
	if !strings.Contains(content, `"logicalParentUuid":42`) {
		t.Errorf("non-string logicalParentUuid should pass through: %s", content)
	}
}

func TestForkSessionTitleFallbacks(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	// No title and no usable first prompt: the default title applies.
	sid := "550e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		{"type": "assistant", "uuid": "a1", "sessionId": sid,
			"message": map[string]interface{}{"content": "assistant only"}},
	})
	result, err := ForkSession(&ForkSessionOptions{SessionID: sid, Directory: &projectPath})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(projectDir, result.SessionID+".jsonl"))
	if !strings.Contains(string(data), "Forked session") {
		t.Errorf("default fork title missing: %s", data)
	}

	// Large file (> head buffer): the title is found in the tail.
	sid2 := "660e8400-e29b-41d4-a716-446655440000"
	var lines []string
	lines = append(lines, compactJSON(map[string]interface{}{
		"type": "user", "uuid": "u1", "sessionId": sid2,
		"message": map[string]interface{}{"content": "start"},
	}))
	for i := 0; i < 400; i++ {
		lines = append(lines, compactJSON(map[string]interface{}{
			"type": "assistant", "uuid": fmt.Sprintf("a%d", i), "sessionId": sid2,
			"message": map[string]interface{}{"content": strings.Repeat("x", 300)},
		}))
	}
	lines = append(lines, compactJSON(map[string]interface{}{
		"type": "custom-title", "customTitle": "Tail Title", "sessionId": sid2,
	}))
	if err := os.WriteFile(filepath.Join(projectDir, sid2+".jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result2, err := ForkSession(&ForkSessionOptions{SessionID: sid2, Directory: &projectPath})
	if err != nil {
		t.Fatalf("ForkSession 2 failed: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(projectDir, result2.SessionID+".jsonl"))
	if !strings.Contains(string(data2), "Tail Title (fork)") {
		t.Errorf("tail-derived title missing")
	}
}

func TestForkSessionWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are ineffective as root")
	}
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		makeTranscriptEntry("user", "u1", nil, sid, "hello"),
	})

	if err := os.Chmod(projectDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(projectDir, 0o755) })

	if _, err := ForkSession(&ForkSessionOptions{SessionID: sid, Directory: &projectPath}); err == nil {
		t.Error("expected error writing the fork into a read-only dir")
	}
}

func TestFindSessionFileWithDirNoProjectsDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))
	sid := "550e8400-e29b-41d4-a716-446655440000"
	if path, dir := findSessionFileWithDir(sid, nil); path != "" || dir != "" {
		t.Errorf("expected empty results, got (%q, %q)", path, dir)
	}
}

func TestAppendToSessionNoProjectsDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))
	sid := "550e8400-e29b-41d4-a716-446655440000"
	err := appendToSession(sid, "x\n", nil)
	if err == nil || !strings.Contains(err.Error(), "no projects directory") {
		t.Errorf("expected no-projects-directory error, got %v", err)
	}
}

func TestTagSessionViaStoreWhitespaceTag(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	blank := "   "
	err := TagSessionViaStore(context.Background(), NewInMemorySessionStore(), sid, &blank, nil)
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("expected non-empty tag error, got %v", err)
	}
}

func TestForkSessionViaStoreMoreBranches(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Invalid session id.
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: NewInMemorySessionStore(), SessionID: "bad",
	}); err == nil {
		t.Error("expected error for invalid session id")
	}

	// Append failure at the end propagates.
	appendFail := &appendFailWithEntries{
		BaseSessionStore: &BaseSessionStore{},
		entries: []SessionStoreEntry{
			{"type": "user", "uuid": "u1", "message": map[string]interface{}{"content": "hi"}},
		},
	}
	if _, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: appendFail, SessionID: sid,
	}); err == nil || !strings.Contains(err.Error(), "append exploded") {
		t.Errorf("append failure: %v", err)
	}

	// Content-replacement records are carried into the fork, and a string
	// logicalParentUuid missing from the mapping resolves to nil.
	store := NewInMemorySessionStore()
	projectKey := ProjectKeyForDirectory("")
	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sid}, []SessionStoreEntry{
		{"type": "user", "uuid": "u1", "logicalParentUuid": "not-in-mapping",
			"message": map[string]interface{}{"content": "hi"}},
		{"type": "content-replacement", "sessionId": sid,
			"replacements": []interface{}{map[string]interface{}{"uuid": "u1"}}},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: store, SessionID: sid,
	})
	if err != nil {
		t.Fatalf("ForkSessionViaStore failed: %v", err)
	}
	forked, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: result.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var sawReplacement bool
	for _, e := range forked {
		if t, _ := e["type"].(string); t == "content-replacement" {
			sawReplacement = true
		}
		if u, _ := e["uuid"].(string); u != "" && e["type"] == "user" {
			if e["logicalParentUuid"] != nil {
				t.Errorf("unmapped logicalParentUuid should be nil, got %v", e["logicalParentUuid"])
			}
		}
	}
	if !sawReplacement {
		t.Error("content-replacement not carried into the fork")
	}
}

// appendFailWithEntries loads fine but fails on Append.
type appendFailWithEntries struct {
	*BaseSessionStore
	entries []SessionStoreEntry
}

func (s *appendFailWithEntries) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	return s.entries, nil
}

func (s *appendFailWithEntries) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	return fmt.Errorf("append exploded")
}

func TestForkSessionViaStoreDirectoryAndParentWalk(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"
	dir := t.TempDir()

	store := NewInMemorySessionStore()
	projectKey := ProjectKeyForDirectory(dir)
	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sid}, []SessionStoreEntry{
		{"type": "user", "uuid": "u1", "message": map[string]interface{}{"content": "hi"}},
		{"type": "progress", "uuid": "p1", "parentUuid": "u1"},
		{"type": "assistant", "uuid": "a1", "parentUuid": "p1", "logicalParentUuid": "u1",
			"message": map[string]interface{}{"content": "yo"}},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := ForkSessionViaStore(ctx, &ForkSessionViaStoreOptions{
		Store: store, SessionID: sid, Directory: &dir,
	})
	if err != nil {
		t.Fatalf("ForkSessionViaStore failed: %v", err)
	}

	forked, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: result.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	// The forked entries use fresh UUIDs; the assistant's parent chain must
	// skip the progress entry and land on the remapped user message.
	var userUUID string
	for _, e := range forked {
		if t, _ := e["type"].(string); t == "user" {
			userUUID, _ = e["uuid"].(string)
		}
	}
	if userUUID == "" {
		t.Fatal("forked user entry not found")
	}
	var assistant SessionStoreEntry
	for _, e := range forked {
		if t, _ := e["type"].(string); t == "assistant" {
			assistant = e
		}
	}
	if assistant == nil {
		t.Fatal("forked assistant entry not found")
	}
	if assistant["parentUuid"] != userUUID {
		t.Errorf("progress ancestor not skipped: parentUuid=%v", assistant["parentUuid"])
	}
	if assistant["logicalParentUuid"] != userUUID {
		t.Errorf("logicalParentUuid not remapped: %v", assistant["logicalParentUuid"])
	}
}
