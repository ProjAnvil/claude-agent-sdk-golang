package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Path helpers: sanitizePath, canonicalizePath, abs, findProjectDir
// ---------------------------------------------------------------------------

func TestSanitizePathLongInput(t *testing.T) {
	long := "/" + strings.Repeat("a", 300)
	got := sanitizePath(long)
	if len(got) != maxSanitizedLength+1+len(simpleHash(long)) {
		t.Errorf("unexpected sanitized length: %d (%q...)", len(got), got[:20])
	}
	if !strings.HasPrefix(got, sanitizeRE.ReplaceAllString(long, "-")[:maxSanitizedLength]+"-") {
		t.Errorf("expected truncated prefix + hash, got %q", got[:50])
	}
}

func TestCanonicalizePathCoverage(t *testing.T) {
	dir := t.TempDir()
	got, err := canonicalizePath(dir)
	if err != nil {
		t.Fatalf("canonicalizePath failed: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}

	// A nonexistent path falls back to the original, no error.
	got, err = canonicalizePath(filepath.Join(dir, "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join(dir, "does-not-exist") {
		t.Errorf("expected original path on error, got %q", got)
	}
}

func TestAbsCoverage(t *testing.T) {
	if abs(-5) != 5 || abs(5) != 5 || abs(0) != 0 {
		t.Error("abs mismatch")
	}
}

func TestFindProjectDirCoverage(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)

	// Exact match.
	got, err := findProjectDir(projectPath)
	if err != nil || got != projectDir {
		t.Errorf("exact match: got %q, %v; want %q", got, err, projectDir)
	}

	// Short path with no project directory: error.
	short := filepath.Join(filepath.Dir(projectPath), "nope")
	if _, err := findProjectDir(short); err == nil {
		t.Error("expected error for missing short path")
	}

	// Long path (>200 sanitized chars): prefix match against a truncated dir.
	projectsDir := filepath.Dir(projectDir)
	longPath := "/" + strings.Repeat("b", 300)
	prefix := sanitizePath(longPath)[:maxSanitizedLength]
	truncatedDir := filepath.Join(projectsDir, prefix+"-deadbeef")
	if err := os.MkdirAll(truncatedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = findProjectDir(longPath)
	if err != nil || got != truncatedDir {
		t.Errorf("prefix match: got %q, %v; want %q", got, err, truncatedDir)
	}

	// Long path with no matching directory: error.
	missingLong := "/" + strings.Repeat("c", 300)
	if _, err := findProjectDir(missingLong); err == nil {
		t.Error("expected error for missing long path")
	}
}

// ---------------------------------------------------------------------------
// getWorktreePaths
// ---------------------------------------------------------------------------

// initGitRepoWithWorktree creates a git repo at mainDir plus a linked worktree.
// Returns the added worktree path. Skips when git is unavailable.
func initGitRepoWithWorktree(t *testing.T, mainDir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = mainDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init")
	run("commit", "--allow-empty", "-m", "init")
	wt := filepath.Join(filepath.Dir(mainDir), "linked-worktree")
	run("worktree", "add", wt)
	return wt
}

func TestGetWorktreePathsCoverage(t *testing.T) {
	// Non-git directory: error.
	plain := t.TempDir()
	if _, err := getWorktreePaths(plain); err == nil {
		t.Error("expected error for non-git directory")
	}

	// Repo with one linked worktree: both paths reported.
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	mainDir := filepath.Join(tmp, "repo")
	wt := initGitRepoWithWorktree(t, mainDir)

	paths, err := getWorktreePaths(mainDir)
	if err != nil {
		t.Fatalf("getWorktreePaths failed: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 worktrees, got %v", paths)
	}
	foundMain, foundWT := false, false
	for _, p := range paths {
		if p == mainDir {
			foundMain = true
		}
		if p == wt {
			foundWT = true
		}
	}
	if !foundMain || !foundWT {
		t.Errorf("expected %q and %q in %v", mainDir, wt, paths)
	}
}

// ---------------------------------------------------------------------------
// listSessionsForProject (incl. worktree-aware scanning)
// ---------------------------------------------------------------------------

func TestListSessionsForProjectCoverage(t *testing.T) {
	_, projectPath, _ := setupSessionTestProject(t)

	// includeWorktrees on a non-git directory: falls back to single-dir scan.
	sessions, err := listSessionsForProject(projectPath, nil, 0, true)
	if err != nil {
		t.Fatalf("listSessionsForProject failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected no sessions, got %d", len(sessions))
	}
}

func TestListSessionsForProjectWithWorktrees(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	configDir := filepath.Join(tmp, ".claude")
	projectsDir := filepath.Join(configDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	// A git repo with a linked worktree, each with a session on disk.
	mainDir := filepath.Join(tmp, "repo")
	wtDir := initGitRepoWithWorktree(t, mainDir)

	mainProjectDir := filepath.Join(projectsDir, sanitizePath(mainDir))
	wtProjectDir := filepath.Join(projectsDir, sanitizePath(wtDir))
	for _, d := range []string{mainProjectDir, wtProjectDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mainSID, _ := makeTestSessionFile(t, mainProjectDir, withFirstPrompt("main session"))
	wtSID, _ := makeTestSessionFile(t, wtProjectDir, withFirstPrompt("worktree session"))

	sessions, err := listSessionsForProject(mainDir, nil, 0, true)
	if err != nil {
		t.Fatalf("listSessionsForProject failed: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.SessionID] = true
	}
	if !ids[mainSID] {
		t.Errorf("main session %s not listed: %v", mainSID, ids)
	}
	if !ids[wtSID] {
		t.Errorf("worktree session %s not listed: %v", wtSID, ids)
	}
}

// ---------------------------------------------------------------------------
// readSessionLite
// ---------------------------------------------------------------------------

func TestReadSessionLiteCoverage(t *testing.T) {
	dir := t.TempDir()

	// Missing file.
	if lite := readSessionLite(filepath.Join(dir, "missing.jsonl")); lite != nil {
		t.Error("expected nil for missing file")
	}

	// Empty file.
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if lite := readSessionLite(empty); lite != nil {
		t.Error("expected nil for empty file")
	}

	// Small file: tail == head.
	small := filepath.Join(dir, "small.jsonl")
	if err := os.WriteFile(small, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lite := readSessionLite(small)
	if lite == nil {
		t.Fatal("expected lite for small file")
	}
	if lite.head != lite.tail {
		t.Error("small file: head and tail should match")
	}
	if lite.size != int64(len(`{"type":"user"}`+"\n")) {
		t.Errorf("unexpected size: %d", lite.size)
	}

	// Large file (> buffer): tail is read from an offset.
	large := filepath.Join(dir, "large.jsonl")
	var sb strings.Builder
	sb.WriteString(`{"type":"user","message":{"content":"first"}}` + "\n")
	for sb.Len() < liteReadBufSize+100 {
		sb.WriteString(`{"type":"assistant","message":{"content":"` + strings.Repeat("x", 200) + `"}}` + "\n")
	}
	sb.WriteString(`{"customTitle":"tail-title"}` + "\n")
	if err := os.WriteFile(large, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lite = readSessionLite(large)
	if lite == nil {
		t.Fatal("expected lite for large file")
	}
	if !strings.Contains(lite.head, `"first"`) {
		t.Error("head should contain the first line")
	}
	if !strings.Contains(lite.tail, "tail-title") {
		t.Error("tail should contain the last line")
	}
}

// ---------------------------------------------------------------------------
// readSessionFile / GetSessionInfo
// ---------------------------------------------------------------------------

func TestReadSessionFileCoverage(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, fp := makeTestSessionFile(t, projectDir, withFirstPrompt("find me"))
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}

	// Found via directory.
	content, err := readSessionFile(sid, projectPath)
	if err != nil || content != string(data) {
		t.Errorf("with directory: err=%v content mismatch", err)
	}

	// Directory without the session: empty content, no error.
	missingSID := "550e8400-e29b-41d4-a716-446655440000"
	content, err = readSessionFile(missingSID, projectPath)
	if err != nil || content != "" {
		t.Errorf("missing in directory: got %q, %v", content, err)
	}

	// Found via search across all projects.
	content, err = readSessionFile(sid, "")
	if err != nil || content != string(data) {
		t.Errorf("search all: err=%v content mismatch", err)
	}

	// Not found anywhere: empty.
	content, err = readSessionFile(missingSID, "")
	if err != nil || content != "" {
		t.Errorf("missing everywhere: got %q, %v", content, err)
	}
}

func TestGetSessionInfoCoverage(t *testing.T) {
	// Nil options and invalid UUID errors.
	if _, err := GetSessionInfo(nil); err == nil {
		t.Error("expected error for nil options")
	}
	if _, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: "nope"}); err == nil {
		t.Error("expected error for invalid session ID")
	}

	_, projectPath, projectDir := setupSessionTestProject(t)
	sid, _ := makeTestSessionFile(t, projectDir, withFirstPrompt("info target"))

	// Found with directory.
	info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: sid, Directory: &projectPath})
	if err != nil || info == nil {
		t.Fatalf("with directory: info=%v err=%v", info, err)
	}
	if info.SessionID != sid {
		t.Errorf("unexpected session id: %q", info.SessionID)
	}

	// Found without directory.
	info, err = GetSessionInfo(&GetSessionInfoOptions{SessionID: sid})
	if err != nil || info == nil {
		t.Fatalf("without directory: info=%v err=%v", info, err)
	}

	// Missing session returns nil, nil — with and without directory.
	missing := "550e8400-e29b-41d4-a716-446655440000"
	if info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: missing, Directory: &projectPath}); info != nil || err != nil {
		t.Errorf("missing with directory: info=%v err=%v", info, err)
	}
	if info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: missing}); info != nil || err != nil {
		t.Errorf("missing without directory: info=%v err=%v", info, err)
	}
}

// ---------------------------------------------------------------------------
// resolveSubagentsDir / GetSubagentMessages
// ---------------------------------------------------------------------------

// setupSessionWithSubagent creates a session file plus a subagent transcript
// (and metadata sidecar) in the conventional layout. Returns IDs.
func setupSessionWithSubagent(t *testing.T) (projectPath, sessionID, agentID string) {
	t.Helper()
	_, projectPath, projDir := setupSessionTestProject(t)

	sid, _ := makeTestSessionFile(t, projDir, withFirstPrompt("parent session"))
	sessionID = sid
	agentID = "abc123"

	subDir := filepath.Join(projDir, sessionID)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, subDir, "agent-"+agentID, []map[string]interface{}{
		makeTranscriptEntry("user", "sub-u1", nil, sessionID, "subagent prompt"),
		makeTranscriptEntry("assistant", "sub-a1", strPtr("sub-u1"), sessionID, "subagent reply"),
	})
	meta := `{"toolUseId":"tool-use-9","parentAgentId":"parent-agent-7"}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectPath, sessionID, agentID
}

func TestResolveSubagentsDirCoverage(t *testing.T) {
	projectPath, sessionID, _ := setupSessionWithSubagent(t)

	// With directory: resolved.
	got := resolveSubagentsDir(sessionID, &projectPath)
	if got == "" || !strings.HasSuffix(got, sessionID) {
		t.Errorf("with directory: got %q", got)
	}

	// Without directory: found by scanning all projects.
	if got := resolveSubagentsDir(sessionID, nil); got == "" {
		t.Error("without directory: expected resolution")
	}

	// Unknown session: empty.
	missing := "550e8400-e29b-41d4-a716-446655440000"
	if got := resolveSubagentsDir(missing, &projectPath); got != "" {
		t.Errorf("missing with directory: got %q", got)
	}
	if got := resolveSubagentsDir(missing, nil); got != "" {
		t.Errorf("missing without directory: got %q", got)
	}
}

func TestGetSubagentMessagesCoverage(t *testing.T) {
	projectPath, sessionID, agentID := setupSessionWithSubagent(t)

	// Nil options / invalid IDs.
	if _, err := GetSubagentMessages(nil); err == nil {
		t.Error("expected error for nil options")
	}
	if _, err := GetSubagentMessages(&GetSubagentMessagesOptions{SessionID: "bad"}); err == nil {
		t.Error("expected error for invalid session id")
	}
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	if _, err := GetSubagentMessages(&GetSubagentMessagesOptions{SessionID: validUUID, AgentID: ""}); err == nil {
		t.Error("expected error for empty agent id")
	}

	// Unknown session.
	if _, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: validUUID, AgentID: agentID, Directory: &projectPath,
	}); err == nil {
		t.Error("expected error for unknown session")
	}

	// Unknown agent in a known session.
	if _, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: "nope", Directory: &projectPath,
	}); err == nil {
		t.Error("expected error for unknown agent")
	}

	// Full read with parent IDs from the sidecar.
	msgs, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath,
	})
	if err != nil {
		t.Fatalf("GetSubagentMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].ParentToolUseID == nil || *msgs[0].ParentToolUseID != "tool-use-9" {
		t.Errorf("unexpected parent tool use id: %v", msgs[0].ParentToolUseID)
	}
	if msgs[0].ParentAgentID == nil || *msgs[0].ParentAgentID != "parent-agent-7" {
		t.Errorf("unexpected parent agent id: %v", msgs[0].ParentAgentID)
	}

	// Offset and limit.
	msgs, err = GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath, Offset: 1,
	})
	if err != nil || len(msgs) != 1 {
		t.Errorf("offset=1: got %d msgs, err=%v", len(msgs), err)
	}
	limit := 1
	msgs, err = GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath, Limit: &limit,
	})
	if err != nil || len(msgs) != 1 {
		t.Errorf("limit=1: got %d msgs, err=%v", len(msgs), err)
	}
	msgs, err = GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath, Offset: 99,
	})
	if err != nil || len(msgs) != 0 {
		t.Errorf("offset beyond end: got %d msgs, err=%v", len(msgs), err)
	}
}

func TestGetSubagentMessagesEmptyTranscript(t *testing.T) {
	projectPath, sessionID, agentID := setupSessionWithSubagent(t)
	// Overwrite the transcript with an empty file.
	subDir := resolveSubagentsDir(sessionID, &projectPath)
	if subDir == "" {
		t.Fatal("subagents dir not resolved")
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-"+agentID+".jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath,
	})
	if err != nil || len(msgs) != 0 {
		t.Errorf("empty transcript: got %d msgs, err=%v", len(msgs), err)
	}
}

// ---------------------------------------------------------------------------
// mtimeFromJSONLTail
// ---------------------------------------------------------------------------

func TestMtimeFromJSONLTailCoverage(t *testing.T) {
	// ISO timestamp (nano) on the last line.
	got := mtimeFromJSONLTail("{\"a\":1}\n{\"timestamp\":\"2024-01-02T03:04:05.5Z\"}\n")
	want := time.Date(2024, 1, 2, 3, 4, 5, 500000000, time.UTC).UnixMilli()
	if got != want {
		t.Errorf("timestamp: got %d, want %d", got, want)
	}

	// RFC3339 without fractional seconds.
	got = mtimeFromJSONLTail("{\"timestamp\":\"2024-01-02T03:04:05Z\"}")
	want = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	if got != want {
		t.Errorf("RFC3339: got %d, want %d", got, want)
	}

	// Numeric mtime field.
	if got := mtimeFromJSONLTail("{\"mtime\":12345}"); got != 12345 {
		t.Errorf("mtime field: got %d", got)
	}

	// Last valid line wins; trailing junk is skipped.
	got = mtimeFromJSONLTail("{\"timestamp\":\"2024-01-02T03:04:05Z\"}\nnot-json\n\n")
	if got != want {
		t.Errorf("trailing junk: got %d, want %d", got, want)
	}

	// Nothing usable: falls back to ~now.
	before := time.Now().UnixMilli()
	got = mtimeFromJSONLTail("not json\n{\"other\":true}\n")
	after := time.Now().UnixMilli()
	if got < before || got > after {
		t.Errorf("fallback: got %d, want within [%d, %d]", got, before, after)
	}
}

// ---------------------------------------------------------------------------
// ListSessionsFromStore paths (fast/slow/gap-fill) and deriveInfosViaLoad
// ---------------------------------------------------------------------------

// listOnlyStore implements ListSessions + Load but not ListSessionSummaries,
// forcing the slow path.
type listOnlyStore struct {
	*BaseSessionStore
	listing []SessionStoreListEntry
	loads   map[string][]SessionStoreEntry
	loadErr map[string]error
	listErr error
}

func (s *listOnlyStore) ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listing, nil
}

func (s *listOnlyStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	if err, ok := s.loadErr[key.SessionID]; ok {
		return nil, err
	}
	return s.loads[key.SessionID], nil
}

func TestListSessionsFromStoreSlowPath(t *testing.T) {
	ctx := context.Background()
	goodSID := "550e8400-e29b-41d4-a716-446655440000"
	errSID := "660e8400-e29b-41d4-a716-446655440000"
	emptySID := "770e8400-e29b-41d4-a716-446655440000"
	sidechainSID := "880e8400-e29b-41d4-a716-446655440000"

	store := &listOnlyStore{
		BaseSessionStore: &BaseSessionStore{},
		listing: []SessionStoreListEntry{
			{SessionID: goodSID, Mtime: 100},
			{SessionID: errSID, Mtime: 90},
			{SessionID: emptySID, Mtime: 80},
			{SessionID: sidechainSID, Mtime: 70},
		},
		loads: map[string][]SessionStoreEntry{
			goodSID: {
				{"type": "user", "message": map[string]interface{}{"content": "hello from store"}},
			},
			sidechainSID: {
				{"type": "user", "isSidechain": true, "message": map[string]interface{}{"content": "side"}},
			},
		},
		loadErr: map[string]error{
			errSID: fmt.Errorf("load exploded"),
		},
	}

	infos, err := ListSessionsFromStore(ctx, &ListSessionsFromStoreOptions{Store: store})
	if err != nil {
		t.Fatalf("ListSessionsFromStore failed: %v", err)
	}

	byID := map[string]SDKSessionInfo{}
	for _, info := range infos {
		byID[info.SessionID] = info
	}
	// Good session parsed via the lite path.
	if info, ok := byID[goodSID]; !ok || info.Summary != "hello from store" {
		t.Errorf("good session: %+v", info)
	}
	// Load error degrades to a minimal entry (empty summary, listing mtime).
	if info, ok := byID[errSID]; !ok || info.Summary != "" || info.LastModified != 90 {
		t.Errorf("error session: %+v", info)
	}
	// Empty load and sidechain sessions are dropped.
	if _, ok := byID[emptySID]; ok {
		t.Error("empty session should be dropped")
	}
	if _, ok := byID[sidechainSID]; ok {
		t.Error("sidechain session should be dropped")
	}
}

func TestListSessionsFromStoreSlowPathListError(t *testing.T) {
	store := &listOnlyStore{
		BaseSessionStore: &BaseSessionStore{},
		listErr:          fmt.Errorf("list exploded"),
	}
	_, err := ListSessionsFromStore(context.Background(), &ListSessionsFromStoreOptions{Store: store})
	if err == nil || !strings.Contains(err.Error(), "ListSessions failed") {
		t.Errorf("expected list error, got %v", err)
	}
}

func TestListSessionsFromStoreGapFillAndPagination(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	projectKey := sanitizePath(func() string {
		p, err := canonicalizePath(dir)
		if err != nil {
			return dir
		}
		return p
	}())

	sidA := "550e8400-e29b-41d4-a716-446655440000"
	sidB := "660e8400-e29b-41d4-a716-446655440000"

	// sidA: entries appended → the in-memory store maintains a fresh summary
	// sidecar. sidB: entries present but its summary sidecar is made stale by
	// a newer ListSessions mtime, forcing the gap-fill path.
	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sidA}, []SessionStoreEntry{
		{"type": "user", "message": map[string]interface{}{"content": "session A"}, "timestamp": "2024-01-02T03:04:05Z"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sidB}, []SessionStoreEntry{
		{"type": "user", "message": map[string]interface{}{"content": "session B"}, "timestamp": "2024-01-02T03:04:05Z"},
	}); err != nil {
		t.Fatal(err)
	}

	infos, err := ListSessionsFromStore(ctx, &ListSessionsFromStoreOptions{Store: store, Directory: dir})
	if err != nil {
		t.Fatalf("ListSessionsFromStore failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(infos), infos)
	}
	summaries := map[string]bool{}
	for _, info := range infos {
		summaries[info.Summary] = true
	}
	if !summaries["session A"] || !summaries["session B"] {
		t.Errorf("unexpected summaries: %v", summaries)
	}

	// Offset beyond the result set returns empty.
	infos, err = ListSessionsFromStore(ctx, &ListSessionsFromStoreOptions{Store: store, Directory: dir, Offset: 10})
	if err != nil || len(infos) != 0 {
		t.Errorf("offset beyond end: got %d, err=%v", len(infos), err)
	}

	// Limit truncates.
	limit := 1
	infos, err = ListSessionsFromStore(ctx, &ListSessionsFromStoreOptions{Store: store, Directory: dir, Limit: &limit})
	if err != nil || len(infos) != 1 {
		t.Errorf("limit=1: got %d, err=%v", len(infos), err)
	}
}

// ---------------------------------------------------------------------------
// summaryEntryToSDKInfo branches
// ---------------------------------------------------------------------------

func TestSummaryEntryToSDKInfoCoverage(t *testing.T) {
	// Empty session ID and sidechain are rejected.
	if info := summaryEntryToSDKInfo(SessionSummaryEntry{Data: map[string]interface{}{}}, "/p"); info != nil {
		t.Error("empty session id should be nil")
	}
	if info := summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s", Data: map[string]interface{}{"is_sidechain": true},
	}, "/p"); info != nil {
		t.Error("sidechain should be nil")
	}
	// No usable summary is rejected.
	if info := summaryEntryToSDKInfo(SessionSummaryEntry{SessionID: "s", Data: map[string]interface{}{}}, "/p"); info != nil {
		t.Error("no summary should be nil")
	}

	// Title precedence: custom_title > ai_title; summary falls back through
	// last_prompt / summary_hint / first_prompt; metadata fields populated.
	info := summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s",
		Mtime:     42,
		Data: map[string]interface{}{
			"custom_title": "Custom",
			"ai_title":     "AI",
			"first_prompt": "prompt",
			"git_branch":   "main",
			"tag":          "v1",
			"created_at":   "2024-01-02T03:04:05Z",
		},
	}, "/proj")
	if info == nil {
		t.Fatal("expected info")
	}
	if info.Summary != "Custom" || info.CustomTitle == nil || *info.CustomTitle != "Custom" {
		t.Errorf("title precedence: %+v", info)
	}
	if info.GitBranch == nil || *info.GitBranch != "main" {
		t.Errorf("git branch: %+v", info)
	}
	if info.Tag == nil || *info.Tag != "v1" {
		t.Errorf("tag: %+v", info)
	}
	if info.CreatedAt == nil {
		t.Error("created_at should be parsed")
	}
	if info.CWD == nil || *info.CWD != "/proj" {
		t.Errorf("cwd fallback to project path: %+v", info)
	}

	// ai_title used when no custom title.
	info = summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s",
		Data:      map[string]interface{}{"ai_title": "AI"},
	}, "")
	if info == nil || info.Summary != "AI" {
		t.Errorf("ai title: %+v", info)
	}

	// last_prompt and summary_hint fallbacks.
	info = summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s",
		Data:      map[string]interface{}{"last_prompt": "lp", "summary_hint": "sh"},
	}, "")
	if info == nil || info.Summary != "lp" {
		t.Errorf("last_prompt fallback: %+v", info)
	}
	info = summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s",
		Data:      map[string]interface{}{"summary_hint": "sh", "first_prompt": "fp"},
	}, "")
	if info == nil || info.Summary != "sh" {
		t.Errorf("summary_hint fallback: %+v", info)
	}

	// created_at parsed via plain RFC3339 as well.
	info = summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s",
		Data:      map[string]interface{}{"first_prompt": "fp", "created_at": "2024-01-02T03:04:05+01:00"},
	}, "")
	if info == nil || info.CreatedAt == nil {
		t.Errorf("RFC3339 created_at: %+v", info)
	}
}

// ---------------------------------------------------------------------------
// Second batch
// ---------------------------------------------------------------------------

// summaryGapStore implements ListSessionSummaries + ListSessions + Load with
// scriptable contents to drive the ListSessionsFromStore fast path.
type summaryGapStore struct {
	*BaseSessionStore
	summaries []SessionSummaryEntry
	listing   []SessionStoreListEntry
	loads     map[string][]SessionStoreEntry
	loadErr   map[string]error
}

func (s *summaryGapStore) ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error) {
	return s.summaries, nil
}

func (s *summaryGapStore) ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	return s.listing, nil
}

func (s *summaryGapStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	if err, ok := s.loadErr[key.SessionID]; ok {
		return nil, err
	}
	return s.loads[key.SessionID], nil
}

func userEntry(prompt string) SessionStoreEntry {
	return SessionStoreEntry{"type": "user", "message": map[string]interface{}{"content": prompt}}
}

func TestListSessionsFromStoreFastPathBranches(t *testing.T) {
	ctx := context.Background()
	store := &summaryGapStore{
		BaseSessionStore: &BaseSessionStore{},
		listing: []SessionStoreListEntry{
			{SessionID: "a", Mtime: 100},
			{SessionID: "b", Mtime: 200},
			{SessionID: "c", Mtime: 300},
			{SessionID: "d", Mtime: 400},
			{SessionID: "e", Mtime: 500},
		},
		summaries: []SessionSummaryEntry{
			// Fresh: returned directly.
			{SessionID: "a", Mtime: 100, Data: map[string]interface{}{"first_prompt": "prompt A"}},
			// Stale sidecar (mtime < listing): skipped, gap-filled via Load.
			{SessionID: "b", Mtime: 150, Data: map[string]interface{}{"first_prompt": "stale B"}},
			// Not in list_sessions: skipped entirely.
			{SessionID: "x", Mtime: 100, Data: map[string]interface{}{"first_prompt": "ghost X"}},
			// Unusable sidecar data: dropped, no gap-fill slot.
			{SessionID: "d", Mtime: 400, Data: map[string]interface{}{}},
		},
		loads: map[string][]SessionStoreEntry{
			"b": {userEntry("loaded B")},
			// "c" has no sidecar at all: gap-filled straight from the listing.
			"c": {userEntry("loaded C")},
		},
		loadErr: map[string]error{
			// Gap-fill load failure degrades to a minimal entry.
			"e": fmt.Errorf("load exploded"),
		},
	}

	infos, err := ListSessionsFromStore(ctx, &ListSessionsFromStoreOptions{Store: store})
	if err != nil {
		t.Fatalf("ListSessionsFromStore failed: %v", err)
	}

	byID := map[string]SDKSessionInfo{}
	for _, info := range infos {
		byID[info.SessionID] = info
	}
	if info, ok := byID["a"]; !ok || info.Summary != "prompt A" {
		t.Errorf("fresh sidecar: %+v", info)
	}
	if info, ok := byID["b"]; !ok || info.Summary != "loaded B" || info.LastModified != 200 {
		t.Errorf("stale gap-fill: %+v", info)
	}
	if info, ok := byID["c"]; !ok || info.Summary != "loaded C" {
		t.Errorf("missing sidecar gap-fill: %+v", info)
	}
	if info, ok := byID["e"]; !ok || info.Summary != "" || info.LastModified != 500 {
		t.Errorf("load error degrade: %+v", info)
	}
	if _, ok := byID["x"]; ok {
		t.Error("ghost summary should be skipped")
	}
	if _, ok := byID["d"]; ok {
		t.Error("unusable summary should be dropped")
	}

	// Sorted by mtime descending: e(500), c(300), b(200), a(100).
	if len(infos) != 4 || infos[0].SessionID != "e" || infos[3].SessionID != "a" {
		t.Errorf("unexpected order: %+v", infos)
	}

	// Offset within range applies before gap-fill.
	infos, err = ListSessionsFromStore(ctx, &ListSessionsFromStoreOptions{Store: store, Offset: 3})
	if err != nil {
		t.Fatalf("offset: %v", err)
	}
	if len(infos) != 1 || infos[0].SessionID != "a" {
		t.Errorf("offset=3: %+v", infos)
	}
}

func TestGetSessionInfoWorktreeFallback(t *testing.T) {
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

	// The session file exists only in the linked worktree's project dir.
	wtProjectDir := filepath.Join(projectsDir, sanitizePath(wtDir))
	if err := os.MkdirAll(wtProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid, _ := makeTestSessionFile(t, wtProjectDir, withFirstPrompt("worktree session"))

	info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: sid, Directory: &mainDir})
	if err != nil {
		t.Fatalf("GetSessionInfo failed: %v", err)
	}
	if info == nil || info.SessionID != sid {
		t.Errorf("expected session info via worktree fallback, got %+v", info)
	}

	// readSessionFile resolves through the worktree as well.
	content, err := readSessionFile(sid, mainDir)
	if err != nil || content == "" {
		t.Errorf("readSessionFile via worktree: content=%q err=%v", content, err)
	}
}

func TestGetSessionMessagesEdges(t *testing.T) {
	// Nil options / invalid UUID.
	if _, err := GetSessionMessages(nil); err == nil {
		t.Error("expected error for nil options")
	}
	if _, err := GetSessionMessages(&GetSessionMessagesOptions{SessionID: "bad"}); err == nil {
		t.Error("expected error for invalid session id")
	}

	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		makeTranscriptEntry("user", "u1", nil, sid, "hello"),
		makeTranscriptEntry("assistant", "a1", strPtr("u1"), sid, "hi there"),
		makeTranscriptEntry("user", "u2", strPtr("a1"), sid, "again"),
	})

	msgs, err := GetSessionMessages(&GetSessionMessagesOptions{SessionID: sid, Directory: &projectPath})
	if err != nil || len(msgs) != 3 {
		t.Fatalf("basic: msgs=%d err=%v", len(msgs), err)
	}

	// Negative offset is treated as 0.
	msgs, err = GetSessionMessages(&GetSessionMessagesOptions{SessionID: sid, Directory: &projectPath, Offset: -5})
	if err != nil || len(msgs) != 3 {
		t.Errorf("negative offset: msgs=%d err=%v", len(msgs), err)
	}

	// Offset beyond the message count returns empty.
	msgs, err = GetSessionMessages(&GetSessionMessagesOptions{SessionID: sid, Directory: &projectPath, Offset: 10})
	if err != nil || len(msgs) != 0 {
		t.Errorf("offset beyond end: msgs=%d err=%v", len(msgs), err)
	}

	// Limit larger than the remaining messages clamps to the end.
	limit := 99
	msgs, err = GetSessionMessages(&GetSessionMessagesOptions{
		SessionID: sid, Directory: &projectPath, Offset: 1, Limit: &limit,
	})
	if err != nil || len(msgs) != 2 {
		t.Errorf("clamped limit: msgs=%d err=%v", len(msgs), err)
	}

	// Unknown session: empty content → empty message list.
	missing := "660e8400-e29b-41d4-a716-446655440000"
	msgs, err = GetSessionMessages(&GetSessionMessagesOptions{SessionID: missing, Directory: &projectPath})
	if err != nil || len(msgs) != 0 {
		t.Errorf("missing session: msgs=%d err=%v", len(msgs), err)
	}
}

func TestFilterTranscriptEntriesCoverage(t *testing.T) {
	raw := []SessionStoreEntry{
		{"type": "user", "uuid": "u1"},
		{"type": "agent_metadata", "toolUseId": "x"}, // unknown type: skipped
		{"type": "assistant", "uuid": "a1"},
	}
	// A long line exceeding the field-scan truncation still parses.
	longContent := strings.Repeat("y", 600)
	raw = append(raw, SessionStoreEntry{
		"type": "user", "uuid": "u2",
		"message": map[string]interface{}{"content": longContent},
	})

	out := filterTranscriptEntries(raw)
	if len(out) != 3 {
		t.Errorf("expected 3 entries, got %d: %+v", len(out), out)
	}
}

func TestEntriesToSubagentMessagesEdges(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	entries := []transcriptEntry{
		{Type: "user", UUID: "u1", SessionID: sid},
		{Type: "assistant", UUID: "a1", SessionID: sid, ParentUUID: strPtr("u1")},
	}

	// Negative offset is treated as 0.
	msgs := entriesToSubagentMessages(entries, nil, -1, nil, nil)
	if len(msgs) != 2 {
		t.Errorf("negative offset: got %d", len(msgs))
	}

	// Offset beyond the chain returns empty.
	msgs = entriesToSubagentMessages(entries, nil, 10, nil, nil)
	if len(msgs) != 0 {
		t.Errorf("offset beyond end: got %d", len(msgs))
	}

	// Limit larger than the remaining messages is ignored.
	limit := 99
	msgs = entriesToSubagentMessages(entries, &limit, 1, nil, nil)
	if len(msgs) != 1 {
		t.Errorf("limit clamp: got %d", len(msgs))
	}

	// Parent IDs are stamped on every message.
	tu, pa := "tu-1", "pa-1"
	msgs = entriesToSubagentMessages(entries, nil, 0, &tu, &pa)
	if msgs[0].ParentToolUseID == nil || *msgs[0].ParentToolUseID != "tu-1" {
		t.Errorf("parent tool use id: %+v", msgs[0].ParentToolUseID)
	}
	if msgs[0].ParentAgentID == nil || *msgs[0].ParentAgentID != "pa-1" {
		t.Errorf("parent agent id: %+v", msgs[0].ParentAgentID)
	}
}

func TestListSubagentsEdgeCases(t *testing.T) {
	// Nil options / invalid UUID.
	if _, err := ListSubagents(nil); err == nil {
		t.Error("expected error for nil options")
	}
	if _, err := ListSubagents(&ListSubagentsOptions{SessionID: "bad"}); err == nil {
		t.Error("expected error for invalid session id")
	}

	projectPath, sessionID, agentID := setupSessionWithSubagent(t)

	// A subdirectory inside the subagents dir is skipped even if its name
	// matches the agent file pattern.
	subDir := resolveSubagentsDir(sessionID, &projectPath)
	if err := os.MkdirAll(filepath.Join(subDir, "agent-dir.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-matching file is ignored.
	if err := os.WriteFile(filepath.Join(subDir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids, err := ListSubagents(&ListSubagentsOptions{SessionID: sessionID, Directory: &projectPath})
	if err != nil {
		t.Fatalf("ListSubagents failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != agentID {
		t.Errorf("unexpected agent ids: %v", ids)
	}
}

func TestListSubagentsUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are ineffective as root")
	}
	projectPath, sessionID, _ := setupSessionWithSubagent(t)
	subDir := resolveSubagentsDir(sessionID, &projectPath)
	if err := os.Chmod(subDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

	_, err := ListSubagents(&ListSubagentsOptions{SessionID: sessionID, Directory: &projectPath})
	if err == nil {
		t.Error("expected error for unreadable subagents dir")
	}
}

func TestBuildConversationChainEdges(t *testing.T) {
	// Two entries forming a parent cycle and no terminals: nil chain.
	cycle := []transcriptEntry{
		{Type: "user", UUID: "u1", ParentUUID: strPtr("a1")},
		{Type: "assistant", UUID: "a1", ParentUUID: strPtr("u1")},
	}
	if chain := buildConversationChain(cycle); chain != nil {
		t.Errorf("cycle: expected nil chain, got %d entries", len(chain))
	}

	// Terminal whose parent is missing: walk stops at the gap.
	orphan := []transcriptEntry{
		{Type: "user", UUID: "u1"},
		{Type: "assistant", UUID: "a1", ParentUUID: strPtr("missing")},
	}
	chain := buildConversationChain(orphan)
	if len(chain) != 1 || chain[0].UUID != "a1" {
		t.Errorf("orphan parent: %+v", chain)
	}

	// Progress-only terminals walking a cycle of progress entries: no leaves.
	progressCycle := []transcriptEntry{
		{Type: "progress", UUID: "pT", ParentUUID: strPtr("p1")},
		{Type: "progress", UUID: "p1", ParentUUID: strPtr("p2")},
		{Type: "progress", UUID: "p2", ParentUUID: strPtr("p1")},
	}
	if chain := buildConversationChain(progressCycle); chain != nil {
		t.Errorf("progress cycle: expected nil chain, got %d entries", len(chain))
	}
}

func TestExtractFirstPromptFromHeadEdges(t *testing.T) {
	// Content as an array of text blocks.
	head := `{"type":"user","message":{"content":[{"type":"text","text":"array prompt"}]}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "array prompt" {
		t.Errorf("array content: got %q", got)
	}

	// Slash-command messages are skipped but remembered as a fallback.
	head = `{"type":"user","message":{"content":"<command-name>review</command-name> please"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "review" {
		t.Errorf("command fallback: got %q", got)
	}

	// System-generated prompts are skipped.
	head = `{"type":"user","message":{"content":"<local-command-stdout>out</local-command-stdout>"}}` + "\n" +
		`{"type":"user","message":{"content":"real prompt"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "real prompt" {
		t.Errorf("skip pattern: got %q", got)
	}

	// Non-JSON lines and non-user entries are ignored.
	head = "not json\n" + `{"type":"assistant","message":{"content":"x"}}` + "\n" +
		`{"type":"user","message":{"content":"the prompt"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "the prompt" {
		t.Errorf("mixed lines: got %q", got)
	}

	// Long prompts are truncated at 200 chars with an ellipsis.
	longPrompt := strings.Repeat("z", 250)
	head = `{"type":"user","message":{"content":"` + longPrompt + `"}}` + "\n"
	got := extractFirstPromptFromHead(head)
	if len(got) != 200+len("\u2026") || !strings.HasSuffix(got, "\u2026") {
		t.Errorf("truncation: len=%d", len(got))
	}

	// Tool-result user messages and meta messages are skipped.
	head = `{"type":"user","message":{"content":[{"type":"tool_result","content":"x"}]}}` + "\n" +
		`{"type":"user","isMeta":true,"message":{"content":"meta"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "" {
		t.Errorf("tool result/meta skip: got %q", got)
	}
}

func TestJSONFieldExtractionEscapes(t *testing.T) {
	// Invalid escape sequences are returned raw.
	if got := unescapeJSONString(`a\zb`); got != `a\zb` {
		t.Errorf("invalid escape: got %q", got)
	}
	// No backslash: returned as-is.
	if got := unescapeJSONString("plain"); got != "plain" {
		t.Errorf("plain: got %q", got)
	}
	// Escaped quote inside the value.
	text := `{"k":"a\"b"}`
	if got := extractJSONStringField(text, "k"); got != `a"b` {
		t.Errorf("escaped quote: got %q", got)
	}
	// Escaped backslash before the closing quote.
	text = `{"k":"a\\"}`
	if got := extractJSONStringField(text, "k"); got != `a\` {
		t.Errorf("escaped backslash: got %q", got)
	}
	// extractLastJSONStringField scans past escaped quotes too.
	text = `{"k":"first\"x"} {"k":"second"}`
	if got := extractLastJSONStringField(text, "k"); got != "second" {
		t.Errorf("last field: got %q", got)
	}
}

func TestJSONLToLiteCoverage(t *testing.T) {
	if lite := jsonlToLite("", 1); lite != nil {
		t.Error("empty input should return nil")
	}

	// Larger than the buffer: head and tail come from different offsets.
	big := strings.Repeat("h", liteReadBufSize) + strings.Repeat("t", 100)
	lite := jsonlToLite(big, 42)
	if lite == nil {
		t.Fatal("expected lite")
	}
	if lite.mtime != 42 || lite.size != int64(len(big)) {
		t.Errorf("metadata: %+v", lite)
	}
	if !strings.HasPrefix(lite.head, "hhh") || !strings.HasSuffix(lite.tail, "ttt") {
		t.Error("head/tail split incorrect")
	}
}

func TestEntriesToJSONLCoverage(t *testing.T) {
	if got := entriesToJSONL(nil); got != "" {
		t.Errorf("nil entries: got %q", got)
	}
	// Unmarshalable entries are skipped.
	got := entriesToJSONL([]SessionStoreEntry{
		{"bad": func() {}},
		{"type": "user"},
	})
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, "user") {
		t.Errorf("skip unmarshalable: got %q", got)
	}
}

func TestSummaryEntryToSDKInfoBadCreatedAt(t *testing.T) {
	info := summaryEntryToSDKInfo(SessionSummaryEntry{
		SessionID: "s",
		Data: map[string]interface{}{
			"first_prompt": "fp",
			"created_at":   "not-a-timestamp",
		},
	}, "")
	if info == nil {
		t.Fatal("expected info")
	}
	if info.CreatedAt != nil {
		t.Errorf("invalid created_at should yield nil, got %v", *info.CreatedAt)
	}
}

func TestResolveSubagentsDirSkipsFiles(t *testing.T) {
	_, _, projectDir := setupSessionTestProject(t)
	projectsDir := filepath.Dir(projectDir)
	// A plain file inside the projects dir is skipped during the scan.
	if err := os.WriteFile(filepath.Join(projectsDir, "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := "550e8400-e29b-41d4-a716-446655440000"
	if got := resolveSubagentsDir(missing, nil); got != "" {
		t.Errorf("expected empty result, got %q", got)
	}
}

func TestGetSubagentMessagesNegativeOffsetAndUnreadable(t *testing.T) {
	projectPath, sessionID, agentID := setupSessionWithSubagent(t)

	// Negative offset is treated as 0.
	msgs, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath, Offset: -3,
	})
	if err != nil || len(msgs) != 2 {
		t.Errorf("negative offset: msgs=%d err=%v", len(msgs), err)
	}

	if os.Geteuid() != 0 {
		// Unreadable transcript file surfaces the read error.
		subDir := resolveSubagentsDir(sessionID, &projectPath)
		fp := filepath.Join(subDir, "agent-"+agentID+".jsonl")
		if err := os.Chmod(fp, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(fp, 0o644) })
		if _, err := GetSubagentMessages(&GetSubagentMessagesOptions{
			SessionID: sessionID, AgentID: agentID, Directory: &projectPath,
		}); err == nil {
			t.Error("expected read error for unreadable transcript")
		}
	}
}

func TestGetSessionMessagesFromStoreErrors(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Load unimplemented.
	_, err := GetSessionMessagesFromStore(ctx, &GetSessionMessagesFromStoreOptions{
		Store: &BaseSessionStore{}, SessionID: sid,
	})
	if err == nil || !strings.Contains(err.Error(), "Load()") {
		t.Errorf("expected Load() error, got %v", err)
	}

	// Load failure propagates.
	_, err = GetSessionMessagesFromStore(ctx, &GetSessionMessagesFromStoreOptions{
		Store: &failingLoadStore{&BaseSessionStore{}}, SessionID: sid,
	})
	if err == nil {
		t.Error("expected load error")
	}
}

// subkeysOnlyStore implements ListSubkeys but not Load.
type subkeysOnlyStore struct {
	*BaseSessionStore
	subkeys   []string
	subkeyErr error
}

func (s *subkeysOnlyStore) ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error) {
	if s.subkeyErr != nil {
		return nil, s.subkeyErr
	}
	return s.subkeys, nil
}

func TestGetSubagentMessagesFromStoreErrors(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Load unimplemented.
	_, err := GetSubagentMessagesFromStore(ctx, &GetSubagentMessagesFromStoreOptions{
		Store: &BaseSessionStore{}, SessionID: sid, AgentID: "a1",
	})
	if err == nil || !strings.Contains(err.Error(), "Load()") {
		t.Errorf("expected Load() error, got %v", err)
	}

	// ListSubkeys failure falls back to the canonical subpath; a store with
	// only metadata entries yields an empty message list.
	metaOnly := &metaOnlyLoadStore{subkeysOnlyStore: &subkeysOnlyStore{
		BaseSessionStore: &BaseSessionStore{},
		subkeys:          []string{"subagents/agent-a1"},
	}}
	msgs, err := GetSubagentMessagesFromStore(ctx, &GetSubagentMessagesFromStoreOptions{
		Store: metaOnly, SessionID: sid, AgentID: "a1",
	})
	if err != nil || len(msgs) != 0 {
		t.Errorf("metadata-only: msgs=%d err=%v", len(msgs), err)
	}
}

// metaOnlyLoadStore returns a single agent_metadata entry for subpath loads.
type metaOnlyLoadStore struct {
	*subkeysOnlyStore
}

func (s *metaOnlyLoadStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	return []SessionStoreEntry{{"type": "agent_metadata", "toolUseId": "tu-1"}}, nil
}

func TestListSubagentsFromStoreFiltering(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"
	store := &subkeysOnlyStore{
		BaseSessionStore: &BaseSessionStore{},
		subkeys: []string{
			"other/thing",                    // not under subagents/: skipped
			"subagents/agent-a",              // kept
			"subagents/notagent-b",           // last segment not agent-*: skipped
			"subagents/agent-a",              // duplicate: deduped
			"subagents/workflows/r1/agent-w", // nested: kept
		},
	}
	ids, err := ListSubagentsFromStore(ctx, &ListSubagentsFromStoreOptions{
		Store: store, SessionID: sid,
	})
	if err != nil {
		t.Fatalf("ListSubagentsFromStore failed: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" && ids[0] != "w" {
		t.Errorf("unexpected ids: %v", ids)
	}
}

func TestListSessionsForProjectSkipsFilesInProjectsDir(t *testing.T) {
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

	mainProjectDir := filepath.Join(projectsDir, sanitizePath(mainDir))
	wtProjectDir := filepath.Join(projectsDir, sanitizePath(wtDir))
	for _, d := range []string{mainProjectDir, wtProjectDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A stray file in the projects dir is skipped by the worktree scan.
	if err := os.WriteFile(filepath.Join(projectsDir, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtSID, _ := makeTestSessionFile(t, wtProjectDir, withFirstPrompt("wt"))

	sessions, err := listSessionsForProject(mainDir, nil, 0, true)
	if err != nil {
		t.Fatalf("listSessionsForProject failed: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.SessionID == wtSID {
			found = true
		}
	}
	if !found {
		t.Errorf("worktree session %s not found: %+v", wtSID, sessions)
	}
}

// ---------------------------------------------------------------------------
// Third batch
// ---------------------------------------------------------------------------

func TestFindProjectDirMissingProjectsDir(t *testing.T) {
	// CLAUDE_CONFIG_DIR without a projects subdirectory: the long-path
	// fallback's ReadDir fails.
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))

	longPath := "/" + strings.Repeat("d", 300)
	if _, err := findProjectDir(longPath); err == nil {
		t.Error("expected ReadDir error for missing projects dir")
	}
}

func TestExtractFirstPromptFromHeadMoreBranches(t *testing.T) {
	// isCompactSummary entries are skipped.
	head := `{"type":"user","isCompactSummary":true,"message":{"content":"summary"}}` + "\n" +
		`{"type":"user","message":{"content":"real"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "real" {
		t.Errorf("compact summary skip: got %q", got)
	}

	// Lines that look like user messages but are invalid JSON are skipped.
	head = `{"type":"user",bad-json` + "\n" + `{"type":"user","message":{"content":"real"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "real" {
		t.Errorf("invalid json skip: got %q", got)
	}

	// The parsed entry's type is not "user" (duplicate key, last wins).
	head = `{"type":"user","type":123,"message":{"content":"x"}}` + "\n" +
		`{"type":"user","message":{"content":"real"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "real" {
		t.Errorf("non-user type skip: got %q", got)
	}

	// The message field is not an object.
	head = `{"type":"user","message":"nope"}` + "\n" + `{"type":"user","message":{"content":"real"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "real" {
		t.Errorf("non-dict message skip: got %q", got)
	}

	// Whitespace-only content is skipped.
	head = `{"type":"user","message":{"content":"  \n  "}}` + "\n" +
		`{"type":"user","message":{"content":"real"}}` + "\n"
	if got := extractFirstPromptFromHead(head); got != "real" {
		t.Errorf("blank content skip: got %q", got)
	}
}

func TestReadSessionsFromDirMissingDir(t *testing.T) {
	if got := readSessionsFromDir(filepath.Join(t.TempDir(), "nope"), ""); got != nil {
		t.Errorf("expected nil for missing dir, got %v", got)
	}
}

func TestListSessionsForProjectNoProjectDir(t *testing.T) {
	setupSessionTestProject(t)
	// A directory whose project dir does not exist yields an empty list.
	missing := filepath.Join(t.TempDir(), "no-sessions-here")
	sessions, err := listSessionsForProject(missing, nil, 0, false)
	if err != nil || len(sessions) != 0 {
		t.Errorf("expected empty list, got %v, %v", sessions, err)
	}
}

func TestListSessionsForProjectWorktreeReadDirFallback(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	// Config dir exists but has no projects subdirectory.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))

	mainDir := filepath.Join(tmp, "repo")
	initGitRepoWithWorktree(t, mainDir)

	// Worktree-aware scanning can't read the projects dir: falls back to
	// whatever the canonical project dir yielded (nothing).
	sessions, err := listSessionsForProject(mainDir, nil, 0, true)
	if err != nil || len(sessions) != 0 {
		t.Errorf("expected empty fallback, got %v, %v", sessions, err)
	}
}

func TestListAllSessionsMissingProjectsDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))
	if _, err := listAllSessions(nil, 0); err == nil {
		t.Error("expected ReadDir error for missing projects dir")
	}
}

func TestReadSessionFileScanAllEdges(t *testing.T) {
	// Missing projects dir: ReadDir error.
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))
	sid := "550e8400-e29b-41d4-a716-446655440000"
	if _, err := readSessionFile(sid, ""); err == nil {
		t.Error("expected ReadDir error for missing projects dir")
	}

	// A stray file inside the projects dir is skipped during the scan.
	_, _, projectDir := setupSessionTestProject(t)
	projectsDir := filepath.Dir(projectDir)
	if err := os.WriteFile(filepath.Join(projectsDir, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := readSessionFile(sid, "")
	if err != nil || content != "" {
		t.Errorf("scan with stray file: content=%q err=%v", content, err)
	}
}

func TestGetSessionInfoScanAllEdges(t *testing.T) {
	// Missing projects dir: nil, nil.
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))
	sid := "550e8400-e29b-41d4-a716-446655440000"
	if info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: sid}); info != nil || err != nil {
		t.Errorf("missing projects dir: info=%v err=%v", info, err)
	}

	// A stray file inside the projects dir is skipped during the scan.
	_, _, projectDir := setupSessionTestProject(t)
	projectsDir := filepath.Dir(projectDir)
	if err := os.WriteFile(filepath.Join(projectsDir, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := GetSessionInfo(&GetSessionInfoOptions{SessionID: sid}); info != nil || err != nil {
		t.Errorf("scan with stray file: info=%v err=%v", info, err)
	}
}

func TestParseSessionInfoFromLiteMetadataOnly(t *testing.T) {
	// No title, summary, or prompt anywhere: metadata-only session → nil.
	lite := &liteSessionFile{
		head:  `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n",
		tail:  `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n",
		mtime: 1,
		size:  10,
	}
	if info := parseSessionInfoFromLite("550e8400-e29b-41d4-a716-446655440000", lite, ""); info != nil {
		t.Errorf("expected nil for metadata-only session, got %+v", info)
	}
}

func TestParseTranscriptEntriesSkipsNoUUID(t *testing.T) {
	_, projectPath, projectDir := setupSessionTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	writeTranscript(t, projectDir, sid, []map[string]interface{}{
		{"type": "user"},                // no uuid: skipped
		{"type": "bogus", "uuid": "u1"}, // unknown type: skipped
		makeTranscriptEntry("user", "u2", nil, sid, "only real"),
	})
	msgs, err := GetSessionMessages(&GetSessionMessagesOptions{SessionID: sid, Directory: &projectPath})
	if err != nil {
		t.Fatalf("GetSessionMessages failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].UUID != "u2" {
		t.Errorf("unexpected messages: %+v", msgs)
	}
}

func TestBuildConversationChainMoreEdges(t *testing.T) {
	// Terminal progress entry whose parent is missing: no leaf reachable.
	missingParent := []transcriptEntry{
		{Type: "progress", UUID: "p1", ParentUUID: strPtr("nope")},
	}
	if chain := buildConversationChain(missingParent); chain != nil {
		t.Errorf("missing parent: expected nil chain, got %d", len(chain))
	}

	// Sidechain-only conversation: falls back to non-main leaves, and the
	// visible filter drops everything.
	sidechain := []transcriptEntry{
		{Type: "user", UUID: "u1", IsSidechain: true},
		{Type: "assistant", UUID: "a1", ParentUUID: strPtr("u1"), IsSidechain: true},
	}
	chain := buildConversationChain(sidechain)
	if len(chain) != 2 {
		t.Errorf("sidechain fallback: expected 2-entry chain, got %d", len(chain))
	}
	if visible := filterVisibleMessages(chain); len(visible) != 0 {
		t.Errorf("sidechain entries must be filtered: %d", len(visible))
	}

	// A parent cycle reachable from a real leaf terminates via the seen set.
	cycleFromLeaf := []transcriptEntry{
		{Type: "user", UUID: "u1", ParentUUID: strPtr("a1")},
		{Type: "assistant", UUID: "a1", ParentUUID: strPtr("u1")},
		{Type: "progress", UUID: "pT", ParentUUID: strPtr("a1")},
	}
	chain = buildConversationChain(cycleFromLeaf)
	if len(chain) != 2 {
		t.Errorf("cycle from leaf: expected 2-entry chain, got %d: %+v", len(chain), chain)
	}
}

func TestListSubagentsSessionNotFound(t *testing.T) {
	_, projectPath, _ := setupSessionTestProject(t)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	ids, err := ListSubagents(&ListSubagentsOptions{SessionID: sid, Directory: &projectPath})
	if err != nil || len(ids) != 0 {
		t.Errorf("expected empty list, got %v, %v", ids, err)
	}
}

func TestGetSubagentMessagesLimitClamp(t *testing.T) {
	projectPath, sessionID, agentID := setupSessionWithSubagent(t)
	limit := 99
	msgs, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &projectPath, Limit: &limit,
	})
	if err != nil || len(msgs) != 2 {
		t.Errorf("limit clamp: msgs=%d err=%v", len(msgs), err)
	}
}

func TestMtimeFromJSONLTailMoreBranches(t *testing.T) {
	// Blank lines between entries are skipped.
	got := mtimeFromJSONLTail("{\"timestamp\":\"2024-01-02T03:04:05Z\"}\n\n   \n")
	want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	if got != want {
		t.Errorf("blank lines: got %d, want %d", got, want)
	}

	// An unparsable timestamp falls through to the numeric mtime field.
	if got := mtimeFromJSONLTail("{\"timestamp\":\"not-a-time\",\"mtime\":777}"); got != 777 {
		t.Errorf("mtime fallback: got %d", got)
	}
}

func TestFilterTranscriptEntriesSkipsBroken(t *testing.T) {
	raw := []SessionStoreEntry{
		{"bad": func() {}},             // marshal error: skipped
		{"type": float64(123)},         // unmarshal into struct fails: skipped
		{"type": "user", "uuid": "u1"}, // kept
	}
	out := filterTranscriptEntries(raw)
	if len(out) != 1 || out[0].UUID != "u1" {
		t.Errorf("unexpected output: %+v", out)
	}
}

func TestGetSessionInfoFromStoreEdges(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Load error propagates.
	if _, err := GetSessionInfoFromStore(ctx, &GetSessionInfoFromStoreOptions{
		Store: &failingLoadStore{&BaseSessionStore{}}, SessionID: sid,
	}); err == nil {
		t.Error("expected load error")
	}

	// Sidechain sessions yield nil.
	store := &unmarshalableLoadStore{
		BaseSessionStore: &BaseSessionStore{},
		entries: []SessionStoreEntry{
			{"type": "user", "isSidechain": true, "message": map[string]interface{}{"content": "x"}},
		},
	}
	info, err := GetSessionInfoFromStore(ctx, &GetSessionInfoFromStoreOptions{
		Store: store, SessionID: sid,
	})
	if err != nil || info != nil {
		t.Errorf("sidechain: info=%v err=%v", info, err)
	}
}

func TestGetSubagentMessagesFromStoreLoadEdges(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Load error (not ErrNotImplemented) propagates.
	if _, err := GetSubagentMessagesFromStore(ctx, &GetSubagentMessagesFromStoreOptions{
		Store: &failingLoadStore{&BaseSessionStore{}}, SessionID: sid, AgentID: "a1",
	}); err == nil {
		t.Error("expected load error")
	}

	// Empty entry list yields an empty message slice.
	store := &unmarshalableLoadStore{BaseSessionStore: &BaseSessionStore{}}
	msgs, err := GetSubagentMessagesFromStore(ctx, &GetSubagentMessagesFromStoreOptions{
		Store: store, SessionID: sid, AgentID: "a1",
	})
	if err != nil || len(msgs) != 0 {
		t.Errorf("empty: msgs=%d err=%v", len(msgs), err)
	}
}

func TestListSubagentsFromStoreGenericError(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"
	store := &subkeysOnlyStore{
		BaseSessionStore: &BaseSessionStore{},
		subkeyErr:        fmt.Errorf("subkeys broke"),
	}
	_, err := ListSubagentsFromStore(ctx, &ListSubagentsFromStoreOptions{
		Store: store, SessionID: sid,
	})
	if err == nil || !strings.Contains(err.Error(), "subkeys broke") {
		t.Errorf("expected list error, got %v", err)
	}
}
