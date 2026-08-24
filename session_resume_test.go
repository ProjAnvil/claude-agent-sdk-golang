package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// applyMaterializedOptions
// ---------------------------------------------------------------------------

func TestApplyMaterializedOptions_SetsConfigDirAndResume(t *testing.T) {
	opts := &ClaudeAgentOptions{
		ContinueConversation: true,
		Env:                  map[string]string{"EXISTING": "val"},
	}
	m := &MaterializedResume{
		ConfigDir:       "/tmp/test-resume-dir",
		ResumeSessionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	result := applyMaterializedOptions(opts, m)

	if result.Env["CLAUDE_CONFIG_DIR"] != "/tmp/test-resume-dir" {
		t.Errorf("CLAUDE_CONFIG_DIR not set correctly: %v", result.Env)
	}
	if result.Env["EXISTING"] != "val" {
		t.Errorf("existing env var was lost: %v", result.Env)
	}
	if result.Resume != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("Resume not set: %v", result.Resume)
	}
	if result.ContinueConversation {
		t.Error("ContinueConversation should be cleared")
	}
	// Original options should be unmodified
	if !opts.ContinueConversation {
		t.Error("original options should be unmodified")
	}
}

func TestApplyMaterializedOptions_NilMaterialized(t *testing.T) {
	opts := &ClaudeAgentOptions{Resume: "original"}
	result := applyMaterializedOptions(opts, nil)
	if result != opts {
		t.Error("nil materialized should return original options unchanged")
	}
}

func TestApplyMaterializedOptions_NilOptions(t *testing.T) {
	m := &MaterializedResume{ConfigDir: "/tmp/x", ResumeSessionID: "id"}
	result := applyMaterializedOptions(nil, m)
	if result != nil {
		t.Error("nil options should return nil")
	}
}

// ---------------------------------------------------------------------------
// materializeResumeSession
// ---------------------------------------------------------------------------

func TestMaterializeResumeSession_NoStore(t *testing.T) {
	opts := &ClaudeAgentOptions{ContinueConversation: true}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("no store should return nil,nil: %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_NoContinueOrResume(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{SessionStore: store}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("no resume/continue should return nil,nil: %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_InvalidUUID(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{
		SessionStore: store,
		Resume:       "not-a-uuid",
	}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("invalid UUID resume should return nil,nil: %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_EmptyStore(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{
		SessionStore:         store,
		ContinueConversation: true,
	}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil || m != nil {
		t.Errorf("empty store should return nil,nil (fresh session): %v, %v", m, err)
	}
}

func TestMaterializeResumeSession_WritesJSONL(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	sessionID := "11111111-2222-3333-4444-555555555555"
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(""), SessionID: sessionID}

	entries := []SessionStoreEntry{
		{"type": "user", "content": "hello", "uuid": sessionID},
	}
	if err := store.Append(ctx, key, entries); err != nil {
		t.Fatalf("Append: %v", err)
	}

	opts := &ClaudeAgentOptions{
		SessionStore:  store,
		Resume:        sessionID,
		LoadTimeoutMs: 5000,
	}
	m, err := materializeResumeSession(ctx, opts)
	if err != nil {
		t.Fatalf("materializeResumeSession: %v", err)
	}
	if m == nil {
		t.Fatal("expected MaterializedResume, got nil")
	}
	defer m.Cleanup()

	if m.ResumeSessionID != sessionID {
		t.Errorf("ResumeSessionID: got %q, want %q", m.ResumeSessionID, sessionID)
	}
	if m.ConfigDir == "" {
		t.Error("ConfigDir should be set")
	}

	// Verify the JSONL file was written under projects/<projectKey>/<sessionID>.jsonl
	projectKey := ProjectKeyForDirectory("")
	jsonlPath := filepath.Join(m.ConfigDir, "projects", projectKey, sessionID+".jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("JSONL file not found at %s: %v", jsonlPath, err)
	}
	if len(data) == 0 {
		t.Error("JSONL file is empty")
	}
}

func TestMaterializeResumeSession_CleanupRemovesDir(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	sessionID := "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(""), SessionID: sessionID}
	_ = store.Append(ctx, key, []SessionStoreEntry{{"type": "user", "uuid": sessionID}})

	opts := &ClaudeAgentOptions{SessionStore: store, Resume: sessionID}
	m, err := materializeResumeSession(ctx, opts)
	if err != nil || m == nil {
		t.Fatalf("unexpected: %v, %v", m, err)
	}

	tmpDir := m.ConfigDir
	if err := m.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("temp dir should be removed after Cleanup: %v", err)
	}
}

// ---------------------------------------------------------------------------
// isSafeSubpath
// ---------------------------------------------------------------------------

func TestIsSafeSubpath(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")

	cases := []struct {
		subpath string
		safe    bool
	}{
		{"subagents/foo", true},
		{"subagents/foo/bar", true},
		{"", false},
		{"/absolute/path", false},
		{"../escape", false},
		{"sub/../escape", false},
		{".", false},
		{"..", false},
	}
	for _, tc := range cases {
		got := isSafeSubpath(tc.subpath, sessionDir)
		if got != tc.safe {
			t.Errorf("isSafeSubpath(%q) = %v, want %v", tc.subpath, got, tc.safe)
		}
	}
}

// ---------------------------------------------------------------------------
// writeJSONL
// ---------------------------------------------------------------------------

func TestWriteJSONL_CreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.jsonl")
	entries := []SessionStoreEntry{
		{"type": "user", "content": "hello"},
		{"type": "assistant", "content": "hi"},
	}
	if err := writeJSONL(path, entries); err != nil {
		t.Fatalf("writeJSONL: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := splitLines(string(data))
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
}

func splitLines(s string) []string {
	var lines []string
	for _, l := range splitOnNewline(s) {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func splitOnNewline(s string) []string {
	var result []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// ---------------------------------------------------------------------------
// getProjectsDirFromEnv
// ---------------------------------------------------------------------------

func TestGetProjectsDirFromEnv_UsesEnvOverride(t *testing.T) {
	dir := getProjectsDirFromEnv(map[string]string{"CLAUDE_CONFIG_DIR": "/custom/config"})
	want := filepath.Join("/custom/config", "projects")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

func TestGetProjectsDirFromEnv_IgnoresEmptyEnvValue(t *testing.T) {
	dir := getProjectsDirFromEnv(map[string]string{"CLAUDE_CONFIG_DIR": ""})
	if dir == "" {
		t.Error("expected non-empty projects dir when CLAUDE_CONFIG_DIR is blank")
	}
}

func TestGetProjectsDirFromEnv_NilEnvFallsThrough(t *testing.T) {
	dir := getProjectsDirFromEnv(nil)
	if dir == "" {
		t.Error("expected non-empty projects dir for nil env map")
	}
	if filepath.Base(dir) != "projects" {
		t.Errorf("expected projects dir to end in projects, got %q", dir)
	}
}

// ---------------------------------------------------------------------------
// buildMirrorBatcher
// ---------------------------------------------------------------------------

func TestBuildMirrorBatcher_ReturnsNonNil(t *testing.T) {
	store := NewInMemorySessionStore()
	batcher := buildMirrorBatcher(store, nil, nil, SessionStoreFlushModeBatched, nil)
	if batcher == nil {
		t.Fatal("buildMirrorBatcher returned nil")
	}
	_ = batcher.Close(context.Background())
}

func TestBuildMirrorBatcher_UsesMaterializedDir(t *testing.T) {
	mat := &MaterializedResume{ConfigDir: t.TempDir()}
	store := NewInMemorySessionStore()
	batcher := buildMirrorBatcher(store, mat, nil, SessionStoreFlushModeEager, nil)
	if batcher == nil {
		t.Fatal("buildMirrorBatcher returned nil with materialized dir")
	}
	_ = batcher.Close(context.Background())
}

func TestBuildMirrorBatcher_UsesEnvOverride(t *testing.T) {
	env := map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/test-env-config"}
	store := NewInMemorySessionStore()
	batcher := buildMirrorBatcher(store, nil, env, SessionStoreFlushModeBatched, nil)
	if batcher == nil {
		t.Fatal("buildMirrorBatcher returned nil with env override")
	}
	_ = batcher.Close(context.Background())
}

// ---------------------------------------------------------------------------
// sessionStoreWrapper
// ---------------------------------------------------------------------------

func TestSessionStoreWrapper_AppendRaw_UnresolvablePath(t *testing.T) {
	store := NewInMemorySessionStore()
	w := &sessionStoreWrapper{store: store, projectsDir: "/some/projects"}
	err := w.AppendRaw(context.Background(), "/unrelated/path/x.jsonl",
		[]map[string]interface{}{{"type": "user"}})
	if err != nil {
		t.Errorf("expected nil error for unresolvable path, got %v", err)
	}
}

func TestSessionStoreWrapper_AppendRaw_ResolvablePath(t *testing.T) {
	store := NewInMemorySessionStore()
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, "projects")
	os.MkdirAll(projectsDir, 0755)

	sessionID := generateUUID()
	sanitized := "-Users-test-myproject"
	projectDir := filepath.Join(projectsDir, sanitized)
	os.MkdirAll(projectDir, 0755)
	filePath := filepath.Join(projectDir, sessionID+".jsonl")

	w := &sessionStoreWrapper{store: store, projectsDir: projectsDir}
	err := w.AppendRaw(context.Background(), filePath,
		[]map[string]interface{}{{"type": "user", "content": "hello"}})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// copyAuthFiles — user settings seeding (#1197)
// ---------------------------------------------------------------------------

// setupSeedConfigDir creates a caller config dir with the given files and
// returns its path.
func setupSeedConfigDir(t *testing.T, files map[string][]byte) string {
	t.Helper()
	configDir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(configDir, name), content, 0o600); err != nil {
			t.Fatalf("write seed %s: %v", name, err)
		}
	}
	return configDir
}

func TestStripSettingsForResume(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}

	t.Run("strips plugins and config-dir env", func(t *testing.T) {
		original := []byte(`{"apiKeyHelper":"/bin/print-key","enabledPlugins":{"p@m":true},` +
			`"extraKnownMarketplaces":{"m":{"source":"github","repo":"o/r"}},` +
			`"env":{"CLAUDE_CONFIG_DIR":"/elsewhere","KEEP":"1"},` +
			`"permissions":{"allow":["Bash(ls)"]}}`)
		// A UTF-8 BOM (PowerShell-written settings) is tolerated.
		out := stripSettingsForResume(append(bom, original...))

		var got map[string]interface{}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("stripped output is not valid JSON: %v (%q)", err, out)
		}
		want := map[string]interface{}{
			"apiKeyHelper": "/bin/print-key",
			"env":          map[string]interface{}{"KEEP": "1"},
			"permissions":  map[string]interface{}{"allow": []interface{}{"Bash(ls)"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("nothing to strip returns bytes untouched", func(t *testing.T) {
		content := []byte(`{"apiKeyHelper": "/bin/print-key", "env": {"FOO": "bar"}}`)
		if out := stripSettingsForResume(content); !bytes.Equal(out, content) {
			t.Errorf("expected byte-exact passthrough, got %q", out)
		}
	})

	t.Run("malformed JSON passes through", func(t *testing.T) {
		content := []byte(`{not json`)
		if out := stripSettingsForResume(content); !bytes.Equal(out, content) {
			t.Errorf("expected byte-exact passthrough, got %q", out)
		}
	})

	t.Run("non-object JSON passes through", func(t *testing.T) {
		for _, content := range [][]byte{[]byte(`[1, 2]`), []byte(`42`), []byte(`null`)} {
			if out := stripSettingsForResume(content); !bytes.Equal(out, content) {
				t.Errorf("expected byte-exact passthrough of %q, got %q", content, out)
			}
		}
	})

	t.Run("non-object env passes through", func(t *testing.T) {
		content := []byte(`{"env": "nope", "a": 1}`)
		if out := stripSettingsForResume(content); !bytes.Equal(out, content) {
			t.Errorf("expected byte-exact passthrough, got %q", out)
		}
	})

	t.Run("overflow float falls back to original bytes", func(t *testing.T) {
		// 1e999 is valid JSON that parses to inf; re-serializing after a strip
		// would emit a token the CLI rejects, so the transform must give up and
		// pass the original bytes through.
		content := []byte(`{"enabledPlugins": {"p@m": true}, "threshold": 1e999}`)
		if out := stripSettingsForResume(content); !bytes.Equal(out, content) {
			t.Errorf("expected byte-exact passthrough, got %q", out)
		}
	})
}

func TestCopyAuthFiles_SeedsUserSettings(t *testing.T) {
	settings := []byte(`{"apiKeyHelper": "/bin/print-key", "env": {"FOO": "bar"}}`)
	configDir := setupSeedConfigDir(t, map[string][]byte{
		"settings.json":        settings,
		"cowork_settings.json": settings,
	})
	tmpBase := t.TempDir()

	if err := copyAuthFiles(tmpBase, map[string]string{"CLAUDE_CONFIG_DIR": configDir}); err != nil {
		t.Fatalf("copyAuthFiles: %v", err)
	}

	// Nothing to strip → bytes copied through untouched.
	for _, name := range []string{"settings.json", "cowork_settings.json"} {
		got, err := os.ReadFile(filepath.Join(tmpBase, name))
		if err != nil {
			t.Fatalf("read seeded %s: %v", name, err)
		}
		if !bytes.Equal(got, settings) {
			t.Errorf("%s: got %q, want %q", name, got, settings)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(tmpBase, name))
			if err != nil {
				t.Fatalf("stat seeded %s: %v", name, err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %o, want 600", name, info.Mode().Perm())
			}
		}
	}
}

func TestCopyAuthFiles_ConfigDirPrecedence(t *testing.T) {
	custom := setupSeedConfigDir(t, map[string][]byte{
		"settings.json": []byte(`{"apiKeyHelper":"/from/env"}`),
	})
	// A ~/.claude/settings.json must NOT win over CLAUDE_CONFIG_DIR.
	home := t.TempDir()
	homeConfig := filepath.Join(home, ".claude")
	if err := os.MkdirAll(homeConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeConfig, "settings.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	t.Run("via options env", func(t *testing.T) {
		tmpBase := t.TempDir()
		if err := copyAuthFiles(tmpBase, map[string]string{"CLAUDE_CONFIG_DIR": custom}); err != nil {
			t.Fatalf("copyAuthFiles: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(tmpBase, "settings.json"))
		if err != nil {
			t.Fatalf("read seeded settings.json: %v", err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(got, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed["apiKeyHelper"] != "/from/env" {
			t.Errorf("got %v, want settings from options env config dir", parsed)
		}
	})

	t.Run("via process env", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", custom)
		tmpBase := t.TempDir()
		if err := copyAuthFiles(tmpBase, nil); err != nil {
			t.Fatalf("copyAuthFiles: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(tmpBase, "settings.json"))
		if err != nil {
			t.Fatalf("read seeded settings.json: %v", err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(got, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed["apiKeyHelper"] != "/from/env" {
			t.Errorf("got %v, want settings from process env config dir", parsed)
		}
	})
}

func TestCopyAuthFiles_AbsentSettingsWritesNothing(t *testing.T) {
	configDir := t.TempDir()
	tmpBase := t.TempDir()
	if err := copyAuthFiles(tmpBase, map[string]string{"CLAUDE_CONFIG_DIR": configDir}); err != nil {
		t.Fatalf("copyAuthFiles: %v", err)
	}
	for _, name := range []string{"settings.json", "cowork_settings.json", ".claude.json"} {
		if _, err := os.Stat(filepath.Join(tmpBase, name)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist, stat err=%v", name, err)
		}
	}
}

func TestCopyAuthFiles_MalformedSettingsCopiedThrough(t *testing.T) {
	configDir := setupSeedConfigDir(t, map[string][]byte{
		"settings.json":        []byte(`{not json`),
		"cowork_settings.json": []byte(`{"env": "nope", "a": 1}`),
	})
	tmpBase := t.TempDir()
	if err := copyAuthFiles(tmpBase, map[string]string{"CLAUDE_CONFIG_DIR": configDir}); err != nil {
		t.Fatalf("copyAuthFiles: %v", err)
	}
	for name, want := range map[string]string{
		"settings.json":        `{not json`,
		"cowork_settings.json": `{"env": "nope", "a": 1}`,
	} {
		got, err := os.ReadFile(filepath.Join(tmpBase, name))
		if err != nil {
			t.Fatalf("read seeded %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

// materializeWithStore seeds a one-entry session into an InMemorySessionStore
// and materializes a resume for it.
func materializeWithStore(t *testing.T, sessionID string, env map[string]string) *MaterializedResume {
	t.Helper()
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(""), SessionID: sessionID}
	if err := store.Append(context.Background(), key, []SessionStoreEntry{
		{"type": "user", "uuid": "u1"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	opts := &ClaudeAgentOptions{SessionStore: store, Resume: sessionID, Env: env}
	m, err := materializeResumeSession(context.Background(), opts)
	if err != nil {
		t.Fatalf("materializeResumeSession: %v", err)
	}
	if m == nil {
		t.Fatal("expected MaterializedResume, got nil")
	}
	return m
}

func TestMaterializeResumeSession_UserSettingsMaterialized(t *testing.T) {
	settings := []byte(`{"apiKeyHelper": "/bin/print-key", "env": {"FOO": "bar"}}`)
	configDir := setupSeedConfigDir(t, map[string][]byte{
		"settings.json":        settings,
		"cowork_settings.json": settings,
		".claude.json":         []byte(`{"theme":"dark"}`),
	})

	m := materializeWithStore(t, generateUUID(), map[string]string{"CLAUDE_CONFIG_DIR": configDir})
	defer m.Cleanup()

	for _, name := range []string{"settings.json", "cowork_settings.json"} {
		got, err := os.ReadFile(filepath.Join(m.ConfigDir, name))
		if err != nil {
			t.Fatalf("read seeded %s: %v", name, err)
		}
		if !bytes.Equal(got, settings) {
			t.Errorf("%s: got %q, want %q", name, got, settings)
		}
	}
	if _, err := os.Stat(filepath.Join(m.ConfigDir, ".claude.json")); err != nil {
		t.Errorf(".claude.json should be seeded: %v", err)
	}
}

func TestMaterializeResumeSession_UnreadableSeedFilesDoNotAbort(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	// Directories where files are expected → not regular files; they must be
	// skipped, not abort the resume.
	for _, name := range []string{"settings.json", ".credentials.json"} {
		if err := os.MkdirAll(filepath.Join(configDir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	// Skip the macOS Keychain fallback so no .credentials.json is synthesized.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	sessionID := generateUUID()
	m := materializeWithStore(t, sessionID, nil)
	defer m.Cleanup()

	for _, name := range []string{"settings.json", ".credentials.json", ".claude.json"} {
		if _, err := os.Stat(filepath.Join(m.ConfigDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist, stat err=%v", name, err)
		}
	}
	// The transcript itself was still materialized.
	jsonlPath := filepath.Join(m.ConfigDir, "projects", ProjectKeyForDirectory(""), sessionID+".jsonl")
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Errorf("session JSONL should exist: %v", err)
	}
}

// TestCopyIfPresent_WriteFailureLeavesNoPartialDst verifies that a failed
// write removes any partial destination instead of leaving it for the
// subprocess to misparse.
func TestCopyIfPresent_WriteFailureLeavesNoPartialDst(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(src, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The destination's parent does not exist, so the write must fail.
	dst := filepath.Join(t.TempDir(), "missing-dir", "settings.json")
	copyIfPresent(src, dst, nil)
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("partial dst should be removed, stat err=%v", err)
	}
}

// TestMaterializeResumeSession_SubagentMetadataLastWins verifies that
// agent_metadata entries are split from transcript lines during subkey
// materialization: the transcript JSONL excludes them and the .meta.json
// sidecar reflects the last metadata entry (rewritten on resume).
func TestMaterializeResumeSession_SubagentMetadataLastWins(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()
	sessionID := generateUUID()
	projectKey := ProjectKeyForDirectory("")

	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: sessionID}, []SessionStoreEntry{
		{"type": "user", "uuid": "u1"},
	}); err != nil {
		t.Fatalf("Append main: %v", err)
	}
	subKey := SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: "subagents/agent-x"}
	if err := store.Append(ctx, subKey, []SessionStoreEntry{
		{"type": "agent_metadata", "agentType": "gp", "toolUseId": "toolu_old"},
		{"type": "user", "uuid": "u2", "sessionId": sessionID},
		{"type": "agent_metadata", "agentType": "gp", "toolUseId": "toolu_new", "parentAgentId": "a-parent"},
	}); err != nil {
		t.Fatalf("Append sub: %v", err)
	}

	opts := &ClaudeAgentOptions{SessionStore: store, Resume: sessionID}
	m, err := materializeResumeSession(ctx, opts)
	if err != nil {
		t.Fatalf("materializeResumeSession: %v", err)
	}
	if m == nil {
		t.Fatal("expected MaterializedResume, got nil")
	}
	defer m.Cleanup()

	sessionDir := filepath.Join(m.ConfigDir, "projects", projectKey, sessionID)

	// Transcript JSONL contains only the transcript line.
	transcript, err := os.ReadFile(filepath.Join(sessionDir, "subagents", "agent-x.jsonl"))
	if err != nil {
		t.Fatalf("read materialized transcript: %v", err)
	}
	if strings.Contains(string(transcript), "agent_metadata") {
		t.Errorf("transcript JSONL must exclude agent_metadata entries: %q", transcript)
	}
	if !strings.Contains(string(transcript), `"u2"`) {
		t.Errorf("transcript JSONL missing the transcript entry: %q", transcript)
	}

	// The sidecar holds the last metadata entry, without the synthetic type.
	metaBytes, err := os.ReadFile(filepath.Join(sessionDir, "subagents", "agent-x.meta.json"))
	if err != nil {
		t.Fatalf("read materialized sidecar: %v", err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["toolUseId"] != "toolu_new" {
		t.Errorf("last metadata entry should win, got %v", meta)
	}
	if meta["parentAgentId"] != "a-parent" {
		t.Errorf("parentAgentId missing from sidecar: %v", meta)
	}
	if _, ok := meta["type"]; ok {
		t.Errorf("synthetic type field must be stripped: %v", meta)
	}
}
