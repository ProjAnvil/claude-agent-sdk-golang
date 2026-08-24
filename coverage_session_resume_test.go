package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resumeFakeStore is a scriptable SessionStore for resume-materialization
// helper tests.
type resumeFakeStore struct {
	*BaseSessionStore
	listing   []SessionStoreListEntry
	listErr   error
	loads     map[string][]SessionStoreEntry
	loadErr   map[string]error
	subkeys   []string
	subkeyErr error
}

func (s *resumeFakeStore) ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listing, nil
}

func (s *resumeFakeStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	if err, ok := s.loadErr[key.SessionID]; ok {
		return nil, err
	}
	return s.loads[key.SessionID], nil
}

func (s *resumeFakeStore) ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error) {
	if s.subkeyErr != nil {
		return nil, s.subkeyErr
	}
	return s.subkeys, nil
}

// ---------------------------------------------------------------------------
// loadCandidateWithTimeout
// ---------------------------------------------------------------------------

func TestLoadCandidateWithTimeoutCoverage(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Success.
	store := &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		loads:            map[string][]SessionStoreEntry{sid: {{"type": "user"}}},
	}
	entries, err := loadCandidateWithTimeout(ctx, store, "proj", sid, 5)
	if err != nil || len(entries) != 1 {
		t.Errorf("success: entries=%v err=%v", entries, err)
	}

	// No entries → nil, nil.
	store = &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}}
	entries, err = loadCandidateWithTimeout(ctx, store, "proj", sid, 5)
	if err != nil || entries != nil {
		t.Errorf("empty: entries=%v err=%v", entries, err)
	}

	// Generic error is wrapped.
	store = &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		loadErr:          map[string]error{sid: fmt.Errorf("disk gone")},
	}
	_, err = loadCandidateWithTimeout(ctx, store, "proj", sid, 5)
	if err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Errorf("generic error: %v", err)
	}

	// DeadlineExceeded produces the timeout message.
	store = &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		loadErr:          map[string]error{sid: context.DeadlineExceeded},
	}
	_, err = loadCandidateWithTimeout(ctx, store, "proj", sid, 5)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// resolveContinueCandidateWithTimeout
// ---------------------------------------------------------------------------

func TestResolveContinueCandidateWithTimeoutCoverage(t *testing.T) {
	ctx := context.Background()
	sidOld := "550e8400-e29b-41d4-a716-446655440000"
	sidNew := "660e8400-e29b-41d4-a716-446655440000"
	sidSide := "770e8400-e29b-41d4-a716-446655440000"

	// ListSessions error.
	store := &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}, listErr: fmt.Errorf("list broke")}
	if _, _, err := resolveContinueCandidateWithTimeout(ctx, store, "proj", 5); err == nil ||
		!strings.Contains(err.Error(), "list broke") {
		t.Errorf("list error: %v", err)
	}

	// ListSessions timeout.
	store = &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}, listErr: context.DeadlineExceeded}
	if _, _, err := resolveContinueCandidateWithTimeout(ctx, store, "proj", 5); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Errorf("list timeout: %v", err)
	}

	// Empty listing.
	store = &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}}
	sid, entries, err := resolveContinueCandidateWithTimeout(ctx, store, "proj", 5)
	if err != nil || sid != "" || entries != nil {
		t.Errorf("empty listing: sid=%q entries=%v err=%v", sid, entries, err)
	}

	// Newest valid session wins; non-UUID entries are ignored; sidechains and
	// sessions with no entries are skipped.
	store = &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		listing: []SessionStoreListEntry{
			{SessionID: "not-a-uuid", Mtime: 500},
			{SessionID: sidSide, Mtime: 300},
			{SessionID: sidOld, Mtime: 100},
			{SessionID: sidNew, Mtime: 200},
		},
		loads: map[string][]SessionStoreEntry{
			sidOld:  {{"type": "user"}},
			sidNew:  {{"type": "user"}},
			sidSide: {{"type": "user", "isSidechain": true}},
		},
	}
	sid, entries, err = resolveContinueCandidateWithTimeout(ctx, store, "proj", 5)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if sid != sidNew || len(entries) != 1 {
		t.Errorf("expected newest non-sidechain %s, got sid=%q entries=%v", sidNew, sid, entries)
	}

	// A load error aborts resolution.
	store = &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		listing:          []SessionStoreListEntry{{SessionID: sidOld, Mtime: 100}},
		loadErr:          map[string]error{sidOld: fmt.Errorf("load broke")},
	}
	if _, _, err := resolveContinueCandidateWithTimeout(ctx, store, "proj", 5); err == nil ||
		!strings.Contains(err.Error(), "load broke") {
		t.Errorf("load error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// writeRedactedCredentials
// ---------------------------------------------------------------------------

func TestWriteRedactedCredentialsCoverage(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, ".credentials.json")

	// Empty input: no file written.
	if err := writeRedactedCredentials("", dst); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("empty input should not create a file")
	}

	// Invalid JSON passes through unchanged.
	if err := writeRedactedCredentials("not json", dst); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "not json" {
		t.Errorf("invalid json passthrough: got %q", data)
	}

	// refreshToken is stripped, other fields kept.
	in := `{"claudeAiOauth":{"refreshToken":"secret","accessToken":"ok"}}`
	if err := writeRedactedCredentials(in, dst); err != nil {
		t.Fatalf("redact: %v", err)
	}
	data, _ = os.ReadFile(dst)
	if strings.Contains(string(data), "secret") {
		t.Errorf("refreshToken not redacted: %s", data)
	}
	if !strings.Contains(string(data), "accessToken") {
		t.Errorf("other fields should be kept: %s", data)
	}

	// OAuth block without refreshToken stays as-is.
	in = `{"claudeAiOauth":{"accessToken":"ok"}}`
	if err := writeRedactedCredentials(in, dst); err != nil {
		t.Fatalf("no refresh: %v", err)
	}
	data, _ = os.ReadFile(dst)
	if string(data) != in {
		t.Errorf("no-refresh passthrough: got %q", data)
	}

	// Unwritable destination errors.
	err := writeRedactedCredentials("{}", filepath.Join(dir, "missing-dir", "creds.json"))
	if err == nil {
		t.Error("expected write error")
	}
}

// ---------------------------------------------------------------------------
// writeJSONL
// ---------------------------------------------------------------------------

func TestWriteJSONLCoverage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "session.jsonl")

	// Unmarshalable entries are skipped; valid ones are written.
	entries := []SessionStoreEntry{
		{"type": "user", "uuid": "u1"},
		{"bad": func() {}}, // channels/funcs cannot be JSON-marshaled
		{"type": "assistant", "uuid": "u2"},
	}
	if err := writeJSONL(path, entries); err != nil {
		t.Fatalf("writeJSONL failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (bad entry skipped), got %d: %q", len(lines), data)
	}

	// Mode is restrictive.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600, got %o", info.Mode().Perm())
	}

	// Error path: the destination's parent is a regular file.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(filepath.Join(blocker, "session.jsonl"), entries); err == nil {
		t.Error("expected error when parent is a file")
	}
}

// ---------------------------------------------------------------------------
// readKeychainCredentials (error path — bogus account, never touches the
// real login keychain entry for Claude Code)
// ---------------------------------------------------------------------------

func TestReadKeychainCredentialsBogusUser(t *testing.T) {
	t.Setenv("USER", "definitely-not-a-real-user-claude-sdk-test")
	if got := readKeychainCredentials(); got != "" {
		t.Errorf("expected empty credentials for bogus user, got %q", got)
	}
}

// TestCopyAuthFilesKeychainFallback drives the keychain branch: no config dir
// override, no credentials file, no API keys — with HOME redirected so the
// real ~/.claude is never read and the keychain lookup fails harmlessly.
func TestCopyAuthFilesKeychainFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("USER", "definitely-not-a-real-user-claude-sdk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	tmpBase := t.TempDir()
	if err := copyAuthFiles(tmpBase, nil); err != nil {
		t.Fatalf("copyAuthFiles failed: %v", err)
	}
	// No credentials source exists, so no credentials file is written.
	if _, err := os.Stat(filepath.Join(tmpBase, ".credentials.json")); !os.IsNotExist(err) {
		t.Error("no credentials file should be written when no source exists")
	}
}

// ---------------------------------------------------------------------------
// contextWithTimeoutSec
// ---------------------------------------------------------------------------

func TestContextWithTimeoutSecCoverage(t *testing.T) {
	// Positive timeout honored.
	ctx, cancel := contextWithTimeoutSec(context.Background(), 30)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	if d := time.Until(deadline); d <= 25*time.Second || d > 30*time.Second {
		t.Errorf("unexpected deadline delta: %v", d)
	}

	// Non-positive timeout falls back to the 60s default.
	ctx, cancel = contextWithTimeoutSec(context.Background(), 0)
	defer cancel()
	deadline, _ = ctx.Deadline()
	if d := time.Until(deadline); d <= 55*time.Second || d > 60*time.Second {
		t.Errorf("unexpected default deadline delta: %v", d)
	}
}

// ---------------------------------------------------------------------------
// materializeResumeSession: continue path with subagent materialization
// ---------------------------------------------------------------------------

func TestMaterializeResumeSessionContinueWithSubkeys(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	sid := "550e8400-e29b-41d4-a716-446655440000"
	projectKey := ProjectKeyForDirectory("")
	store := &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		listing:          []SessionStoreListEntry{{SessionID: sid, Mtime: 100}},
		loads: map[string][]SessionStoreEntry{
			sid: {{"type": "user", "uuid": "u1", "message": map[string]interface{}{"content": "hi"}}},
		},
		subkeys: []string{"subagents/agent-x"},
	}
	// The subagent transcript lives under a Subpath key.
	subEntries := []SessionStoreEntry{
		{"type": "agent_metadata", "toolUseId": "tu-1"},
		{"type": "user", "uuid": "su1", "message": map[string]interface{}{"content": "sub"}},
	}
	subStore := &resumeSubkeyLoadStore{resumeFakeStore: store, subEntries: subEntries}

	m, err := materializeResumeSession(context.Background(), &ClaudeAgentOptions{
		SessionStore:         subStore,
		ContinueConversation: true,
	})
	if err != nil {
		t.Fatalf("materializeResumeSession failed: %v", err)
	}
	if m == nil {
		t.Fatal("expected materialized resume")
	}
	defer m.Cleanup()

	if m.ResumeSessionID != sid {
		t.Errorf("unexpected session id: %q", m.ResumeSessionID)
	}

	// Main transcript written.
	mainFile := filepath.Join(m.ConfigDir, "projects", projectKey, sid+".jsonl")
	if _, err := os.Stat(mainFile); err != nil {
		t.Errorf("main transcript missing: %v", err)
	}

	// Subagent transcript + metadata sidecar written.
	subFile := filepath.Join(m.ConfigDir, "projects", projectKey, sid, "subagents", "agent-x.jsonl")
	data, err := os.ReadFile(subFile)
	if err != nil {
		t.Fatalf("subagent transcript missing: %v", err)
	}
	if strings.Contains(string(data), "agent_metadata") {
		t.Error("metadata entry should not be in the transcript file")
	}
	metaFile := agentMetadataSidecarPath(subFile)
	meta, err := os.ReadFile(metaFile)
	if err != nil {
		t.Fatalf("metadata sidecar missing: %v", err)
	}
	if !strings.Contains(string(meta), "tu-1") || strings.Contains(string(meta), "agent_metadata") {
		t.Errorf("unexpected sidecar content: %s", meta)
	}
}

// resumeSubkeyLoadStore routes Subpath loads to a separate entry set.
type resumeSubkeyLoadStore struct {
	*resumeFakeStore
	subEntries []SessionStoreEntry
}

func (s *resumeSubkeyLoadStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	if key.Subpath != "" {
		return s.subEntries, nil
	}
	return s.resumeFakeStore.Load(ctx, key)
}

// TestMaterializeResumeSessionContinueListError covers the error path of
// resolveContinueCandidateWithTimeout via the public entry point.
func TestMaterializeResumeSessionContinueListError(t *testing.T) {
	store := &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}, listErr: fmt.Errorf("list broke")}
	_, err := materializeResumeSession(context.Background(), &ClaudeAgentOptions{
		SessionStore:         store,
		ContinueConversation: true,
	})
	if err == nil || !strings.Contains(err.Error(), "list broke") {
		t.Errorf("expected list error, got %v", err)
	}
}

// TestGetProjectsDirFromEnvFallback covers the env-miss fallthrough.
func TestGetProjectsDirFromEnvFallback(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	got := getProjectsDirFromEnv(nil)
	if !strings.HasSuffix(got, filepath.Join("projects")) {
		t.Errorf("unexpected projects dir: %q", got)
	}
	want := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Second batch
// ---------------------------------------------------------------------------

func TestResolveContinueCandidateAllSkipped(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"

	// Every candidate is unusable (sidechain / no entries): nil result.
	store := &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		listing: []SessionStoreListEntry{
			{SessionID: sid, Mtime: 100},
			{SessionID: "660e8400-e29b-41d4-a716-446655440000", Mtime: 50},
		},
		loads: map[string][]SessionStoreEntry{
			sid: {{"type": "user", "isSidechain": true}},
			// The other session has no entries at all.
		},
	}
	gotSID, entries, err := resolveContinueCandidateWithTimeout(ctx, store, "proj", 5)
	if err != nil || gotSID != "" || entries != nil {
		t.Errorf("all skipped: sid=%q entries=%v err=%v", gotSID, entries, err)
	}
}

func TestWriteJSONLOpenError(t *testing.T) {
	dir := t.TempDir()

	// MkdirAll fails when a parent component is a regular file.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(filepath.Join(blocker, "session.jsonl"), []SessionStoreEntry{{"type": "user"}}); err == nil {
		t.Error("expected MkdirAll error when parent is a file")
	}

	// OpenFile fails when the destination path is a directory.
	target := filepath.Join(dir, "session.jsonl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(target, []SessionStoreEntry{{"type": "user"}}); err == nil {
		t.Error("expected OpenFile error for directory path")
	}
}

func TestReadKeychainCredentialsEmptyUser(t *testing.T) {
	// USER unset: the fallback account name is used; the lookup still fails.
	t.Setenv("USER", "")
	if got := readKeychainCredentials(); got != "" {
		t.Errorf("expected empty credentials, got %q", got)
	}
}

func TestCopyAuthFilesFromHomeCreds(t *testing.T) {
	// No CLAUDE_CONFIG_DIR override: credentials are read from ~/.claude,
	// with HOME redirected so the real home directory is never touched.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("USER", "definitely-not-a-real-user-claude-sdk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	creds := `{"claudeAiOauth":{"refreshToken":"secret","accessToken":"ok"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	tmpBase := t.TempDir()
	if err := copyAuthFiles(tmpBase, nil); err != nil {
		t.Fatalf("copyAuthFiles failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpBase, ".credentials.json"))
	if err != nil {
		t.Fatalf("redacted credentials not written: %v", err)
	}
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "accessToken") {
		t.Errorf("unexpected redacted credentials: %s", data)
	}
}

func TestMaterializeSubkeysErrorPaths(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"
	tmpBase := t.TempDir()
	projectDir := filepath.Join(tmpBase, "projects", "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// ListSubkeys failure propagates.
	store := &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}, subkeyErr: fmt.Errorf("subkeys broke")}
	if err := materializeSubkeys(ctx, store, tmpBase, projectDir, "proj", sid, 5); err == nil ||
		!strings.Contains(err.Error(), "subkeys broke") {
		t.Errorf("ListSubkeys error: %v", err)
	}

	// Mixed subkeys: unsafe paths, load failures and empty loads are skipped;
	// valid ones are written.
	store = &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		subkeys: []string{
			"../escape",              // unsafe: skipped
			"/absolute",              // unsafe: skipped
			"subagents/agent-empty",  // no entries: skipped
			"subagents/agent-broken", // load error: skipped
			"subagents/agent-good",
		},
		loads: map[string][]SessionStoreEntry{
			sid + "/good-unused": nil,
		},
	}
	subStore := &subkeyRoutingStore{resumeFakeStore: store}
	if err := materializeSubkeys(ctx, subStore, tmpBase, projectDir, "proj", sid, 5); err != nil {
		t.Fatalf("materializeSubkeys failed: %v", err)
	}

	goodFile := filepath.Join(projectDir, sid, "subagents", "agent-good.jsonl")
	if _, err := os.Stat(goodFile); err != nil {
		t.Errorf("good subkey not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, sid, "subagents", "agent-empty.jsonl")); !os.IsNotExist(err) {
		t.Error("empty subkey should be skipped")
	}
}

// subkeyRoutingStore answers Load based on the subpath.
type subkeyRoutingStore struct {
	*resumeFakeStore
}

func (s *subkeyRoutingStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	switch key.Subpath {
	case "subagents/agent-broken":
		return nil, fmt.Errorf("sub load broke")
	case "subagents/agent-good":
		return []SessionStoreEntry{{"type": "user", "uuid": "u1"}}, nil
	default:
		return nil, nil
	}
}

func TestIsSafeSubpathExtraBranches(t *testing.T) {
	sessionDir := t.TempDir()
	tests := []struct {
		subpath string
		want    bool
	}{
		{`\windows\style`, false}, // backslash-absolute
		{"C:drive", false},        // drive letter
		{"a\x00b", false},         // NUL byte
		{"subagents/agent-1", true},
	}
	for _, tt := range tests {
		if got := isSafeSubpath(tt.subpath, sessionDir); got != tt.want {
			t.Errorf("isSafeSubpath(%q) = %v, want %v", tt.subpath, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Third batch
// ---------------------------------------------------------------------------

func TestMaterializeResumeSessionNotInStore(t *testing.T) {
	// The store does not have the requested session: no materialization,
	// the CLI handles resume itself.
	m, err := materializeResumeSession(context.Background(), &ClaudeAgentOptions{
		SessionStore: &resumeFakeStore{BaseSessionStore: &BaseSessionStore{}},
		Resume:       "550e8400-e29b-41d4-a716-446655440000",
	})
	if err != nil || m != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", m, err)
	}
}

func TestMaterializeResumeSessionSubkeysFailureIsNonFatal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	sid := "550e8400-e29b-41d4-a716-446655440000"
	projectKey := ProjectKeyForDirectory("")

	store := &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		loads: map[string][]SessionStoreEntry{
			sid: {{"type": "user", "uuid": "u1"}},
		},
		subkeyErr: fmt.Errorf("subkeys broke"),
	}
	m, err := materializeResumeSession(context.Background(), &ClaudeAgentOptions{
		SessionStore: store,
		Resume:       sid,
	})
	if err != nil {
		t.Fatalf("subkeys failure should be non-fatal: %v", err)
	}
	if m == nil {
		t.Fatal("expected materialized resume")
	}
	defer m.Cleanup()
	if _, err := os.Stat(filepath.Join(m.ConfigDir, "projects", projectKey, sid+".jsonl")); err != nil {
		t.Errorf("main transcript missing: %v", err)
	}
}

func TestCopyAuthFilesUnwritableTmpBase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are ineffective as root")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"ok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// A read-only tmpBase makes writeRedactedCredentials fail.
	tmpBase := t.TempDir()
	if err := os.Chmod(tmpBase, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tmpBase, 0o755) })

	if err := copyAuthFiles(tmpBase, nil); err == nil {
		t.Error("expected error for unwritable tmpBase")
	}
}

func TestMaterializeSubkeysWriteFailureSkipped(t *testing.T) {
	ctx := context.Background()
	sid := "550e8400-e29b-41d4-a716-446655440000"
	tmpBase := t.TempDir()
	projectDir := filepath.Join(tmpBase, "projects", "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The session directory path exists as a regular file, so writing any
	// subagent transcript under it fails and is skipped.
	sessionDir := filepath.Join(projectDir, sid)
	if err := os.WriteFile(sessionDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &resumeFakeStore{
		BaseSessionStore: &BaseSessionStore{},
		subkeys:          []string{"subagents/agent-good"},
	}
	subStore := &subkeyRoutingStore{resumeFakeStore: store}
	if err := materializeSubkeys(ctx, subStore, tmpBase, projectDir, "proj", sid, 5); err != nil {
		t.Errorf("write failures should be skipped, got %v", err)
	}
}
