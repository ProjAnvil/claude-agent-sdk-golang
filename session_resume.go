package claude

// Materialize a SessionStore-backed resume into a temp CLAUDE_CONFIG_DIR.
//
// When options.Resume (or options.ContinueConversation) is paired with
// options.SessionStore, the session JSONL almost certainly does not exist on
// local disk — it lives in the external store. The CLI subprocess only knows
// how to resume from a local file. This module bridges the gap: it loads the
// session from the store, writes it to a temporary directory laid out exactly
// like ~/.claude/, and returns the path so the caller can point the subprocess
// at it via CLAUDE_CONFIG_DIR.
//
// Mirrors the behavior of the Python SDK _internal/session_resume.py.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// MaterializedResume is the result of materializeResumeSession.
type MaterializedResume struct {
	// ConfigDir is the temporary directory laid out like ~/.claude/ —
	// point the subprocess at it via CLAUDE_CONFIG_DIR.
	ConfigDir string
	// ResumeSessionID is the session ID to pass as --resume. When the input
	// was ContinueConversation, this is the most-recent session resolved via
	// SessionStore.ListSessions.
	ResumeSessionID string
	// Cleanup removes ConfigDir (best-effort). Call after the subprocess exits.
	Cleanup func() error
}

// applyMaterializedOptions returns a copy of options repointed at a
// materialized temp config dir. Sets CLAUDE_CONFIG_DIR in Env, sets Resume to
// the materialized session id, and clears ContinueConversation (already
// resolved to a concrete session id during materialization).
func applyMaterializedOptions(options *ClaudeAgentOptions, m *MaterializedResume) *ClaudeAgentOptions {
	if options == nil || m == nil {
		return options
	}
	// Shallow copy
	opts := *options

	// Merge env with CLAUDE_CONFIG_DIR override
	newEnv := make(map[string]string, len(options.Env)+1)
	for k, v := range options.Env {
		newEnv[k] = v
	}
	newEnv["CLAUDE_CONFIG_DIR"] = m.ConfigDir

	opts.Env = newEnv
	opts.Resume = m.ResumeSessionID
	opts.ContinueConversation = false
	return &opts
}

// materializeResumeSession loads a session from options.SessionStore and
// writes it to a temp directory. Returns nil when no materialization is needed
// (no store, no resume/continue, store has no entries, or the resolved session
// ID is not a valid UUID). The caller must call m.Cleanup() when done.
func materializeResumeSession(ctx context.Context, options *ClaudeAgentOptions) (*MaterializedResume, error) {
	if options == nil || options.SessionStore == nil {
		return nil, nil
	}
	if options.Resume == "" && !options.ContinueConversation {
		return nil, nil
	}

	timeoutMs := options.LoadTimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 60000
	}
	timeoutSec := float64(timeoutMs) / 1000.0

	store := options.SessionStore
	cwd := options.CWD
	projectKey := ProjectKeyForDirectory(cwd)

	var sessionID string
	var entries []SessionStoreEntry

	if options.Resume != "" {
		// Explicit resume: validate UUID, load from store.
		if !validateUUID(options.Resume) {
			// Not a UUID — pass through to CLI unchanged.
			return nil, nil
		}
		loaded, err := loadCandidateWithTimeout(ctx, store, projectKey, options.Resume, timeoutSec)
		if err != nil {
			return nil, err
		}
		if loaded == nil {
			// Session not in store; let CLI handle it.
			return nil, nil
		}
		sessionID = options.Resume
		entries = loaded
	} else {
		// ContinueConversation: pick most-recent non-sidechain session.
		sid, loaded, err := resolveContinueCandidateWithTimeout(ctx, store, projectKey, timeoutSec)
		if err != nil {
			return nil, err
		}
		if loaded == nil {
			// Empty store means fresh session.
			return nil, nil
		}
		sessionID = sid
		entries = loaded
	}

	// Write to a temp directory.
	tmpBase, err := os.MkdirTemp("", "claude-resume-")
	if err != nil {
		return nil, fmt.Errorf("materializeResumeSession: failed to create temp dir: %w", err)
	}

	writeAndCleanup := func() error { return removeAllBestEffort(tmpBase) }

	projectDir := filepath.Join(tmpBase, "projects", projectKey)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		_ = writeAndCleanup()
		return nil, fmt.Errorf("materializeResumeSession: failed to create project dir: %w", err)
	}

	// Write main session JSONL.
	sessionFile := filepath.Join(projectDir, sessionID+".jsonl")
	if err := writeJSONL(sessionFile, entries); err != nil {
		_ = writeAndCleanup()
		return nil, fmt.Errorf("materializeResumeSession: failed to write session JSONL: %w", err)
	}

	// Copy auth files from the caller's effective config dir.
	if err := copyAuthFiles(tmpBase, options.Env); err != nil {
		_ = writeAndCleanup()
		return nil, fmt.Errorf("materializeResumeSession: failed to copy auth files: %w", err)
	}

	// Materialize subagent transcripts if store supports it.
	if err := probeListSubkeys(store); !errors.Is(err, ErrNotImplemented) {
		if err := materializeSubkeys(ctx, store, tmpBase, projectDir, projectKey, sessionID, timeoutSec); err != nil {
			// Non-fatal: subagent transcripts are optional. Log and continue.
			_ = err
		}
	}

	return &MaterializedResume{
		ConfigDir:       tmpBase,
		ResumeSessionID: sessionID,
		Cleanup:         writeAndCleanup,
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// loadCandidateWithTimeout loads entries for sessionID via the store with a
// timeout. Returns nil if the session has no entries.
func loadCandidateWithTimeout(ctx context.Context, store SessionStore, projectKey, sessionID string, timeoutSec float64) ([]SessionStoreEntry, error) {
	timeoutCtx, cancel := contextWithTimeoutSec(ctx, timeoutSec)
	defer cancel()
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	entries, err := store.Load(timeoutCtx, key)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("SessionStore.Load() for session %s timed out after %dms during resume materialization", sessionID, int(timeoutSec*1000))
		}
		return nil, fmt.Errorf("SessionStore.Load() for session %s failed during resume materialization: %w", sessionID, err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries, nil
}

// resolveContinueCandidateWithTimeout picks the most-recently-modified
// non-sidechain session from the store.
func resolveContinueCandidateWithTimeout(ctx context.Context, store SessionStore, projectKey string, timeoutSec float64) (string, []SessionStoreEntry, error) {
	timeoutCtx, cancel := contextWithTimeoutSec(ctx, timeoutSec)
	defer cancel()

	summaries, err := store.ListSessions(timeoutCtx, projectKey)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", nil, fmt.Errorf("SessionStore.ListSessions() timed out after %dms during resume materialization", int(timeoutSec*1000))
		}
		return "", nil, fmt.Errorf("SessionStore.ListSessions() failed during resume materialization: %w", err)
	}
	if len(summaries) == 0 {
		return "", nil, nil
	}

	// Sort newest first by mtime.
	type candidate struct {
		sessionID string
		mtime     int64
	}
	candidates := make([]candidate, 0, len(summaries))
	for _, s := range summaries {
		if validateUUID(s.SessionID) {
			candidates = append(candidates, candidate{sessionID: s.SessionID, mtime: s.Mtime})
		}
	}
	// Insertion sort (small list)
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].mtime > candidates[j-1].mtime; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	for _, c := range candidates {
		entries, err := loadCandidateWithTimeout(ctx, store, projectKey, c.sessionID, timeoutSec)
		if err != nil {
			return "", nil, err
		}
		if entries == nil {
			continue
		}
		// Skip sidechains.
		if len(entries) > 0 {
			if isSidechain, _ := entries[0]["isSidechain"].(bool); isSidechain {
				continue
			}
		}
		return c.sessionID, entries, nil
	}
	return "", nil, nil
}

// writeJSONL writes entries as one JSON line each to path (mode 0o600).
func writeJSONL(path string, entries []SessionStoreEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range entries {
		data, err := json.Marshal(map[string]interface{}(e))
		if err != nil {
			continue
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// copyAuthFiles copies .credentials.json (with refreshToken redacted) and
// .claude.json from the caller's effective config dir to tmpBase.
func copyAuthFiles(tmpBase string, optEnv map[string]string) error {
	callerConfigDir := ""
	if optEnv != nil {
		callerConfigDir = optEnv["CLAUDE_CONFIG_DIR"]
	}
	if callerConfigDir == "" {
		callerConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}

	var sourceConfigDir string
	if callerConfigDir != "" {
		sourceConfigDir = callerConfigDir
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil // Best effort
		}
		sourceConfigDir = filepath.Join(home, ".claude")
	}

	// Try to read credentials JSON.
	var credsJSON string
	credsPath := filepath.Join(sourceConfigDir, ".credentials.json")
	if data, err := os.ReadFile(credsPath); err == nil {
		credsJSON = string(data)
	}

	// On macOS with default setup, check Keychain if no file and no API key.
	if credsJSON == "" && callerConfigDir == "" {
		hasAPIKey := (optEnv != nil && optEnv["ANTHROPIC_API_KEY"] != "") ||
			os.Getenv("ANTHROPIC_API_KEY") != ""
		hasOAuthToken := (optEnv != nil && optEnv["CLAUDE_CODE_OAUTH_TOKEN"] != "") ||
			os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != ""
		if !hasAPIKey && !hasOAuthToken {
			if kc := readKeychainCredentials(); kc != "" {
				credsJSON = kc
			}
		}
	}

	// Write credentials with refreshToken removed.
	if err := writeRedactedCredentials(credsJSON, filepath.Join(tmpBase, ".credentials.json")); err != nil {
		return err
	}

	// Copy .claude.json
	var claudeJSONSrc string
	if callerConfigDir != "" {
		claudeJSONSrc = filepath.Join(callerConfigDir, ".claude.json")
	} else {
		home, _ := os.UserHomeDir()
		claudeJSONSrc = filepath.Join(home, ".claude.json")
	}
	copyIfPresent(claudeJSONSrc, filepath.Join(tmpBase, ".claude.json"))

	return nil
}

// writeRedactedCredentials writes credsJSON with claudeAiOauth.refreshToken
// removed. The resumed subprocess runs under a redirected CLAUDE_CONFIG_DIR;
// if it refreshed, the single-use refresh token would be consumed server-side.
func writeRedactedCredentials(credsJSON string, dst string) error {
	if credsJSON == "" {
		return nil
	}
	out := credsJSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(credsJSON), &data); err == nil {
		if oauth, ok := data["claudeAiOauth"].(map[string]interface{}); ok {
			if _, has := oauth["refreshToken"]; has {
				delete(oauth, "refreshToken")
				b, err := json.Marshal(data)
				if err == nil {
					out = string(b)
				}
			}
		}
	}
	if err := os.WriteFile(dst, []byte(out), 0o600); err != nil {
		return err
	}
	return nil
}

// copyIfPresent copies src to dst, ignoring missing source.
func copyIfPresent(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0o600)
}

// readKeychainCredentials reads OAuth credentials from the macOS Keychain.
// Returns empty string on any error or non-macOS platforms.
func readKeychainCredentials() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	userName := os.Getenv("USER")
	if userName == "" {
		userName = "claude-code-user"
	}
	cmd := exec.Command("security", "find-generic-password",
		"-a", userName, "-w", "-s", "Claude Code-credentials")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// materializeSubkeys loads and writes all subagent transcripts under sessionID.
func materializeSubkeys(ctx context.Context, store SessionStore, tmpBase, projectDir, projectKey, sessionID string, timeoutSec float64) error {
	timeoutCtx, cancel := contextWithTimeoutSec(ctx, timeoutSec)
	defer cancel()

	key := SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID}
	subkeys, err := store.ListSubkeys(timeoutCtx, key)
	if err != nil {
		return fmt.Errorf("SessionStore.ListSubkeys() for session %s failed: %w", sessionID, err)
	}

	sessionDir := filepath.Join(projectDir, sessionID)

	for _, subpath := range subkeys {
		if !isSafeSubpath(subpath, sessionDir) {
			continue
		}

		subKey := SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}
		subTimeoutCtx, subCancel := contextWithTimeoutSec(ctx, timeoutSec)
		subEntries, err := store.Load(subTimeoutCtx, subKey)
		subCancel()
		if err != nil || len(subEntries) == 0 {
			continue
		}

		// Partition: agent_metadata entries → .meta.json; rest → .jsonl
		var metadata []SessionStoreEntry
		var transcript []SessionStoreEntry
		for _, e := range subEntries {
			if t, _ := e["type"].(string); t == "agent_metadata" {
				metadata = append(metadata, e)
			} else {
				transcript = append(transcript, e)
			}
		}

		subFile := filepath.Join(sessionDir, subpath) + ".jsonl"
		if len(transcript) > 0 {
			if err := writeJSONL(subFile, transcript); err != nil {
				continue
			}
		}

		if len(metadata) > 0 {
			// Last metadata entry wins; strip the synthetic "type" field.
			last := map[string]interface{}(metadata[len(metadata)-1])
			metaContent := make(map[string]interface{}, len(last))
			for k, v := range last {
				if k != "type" {
					metaContent[k] = v
				}
			}
			metaName := strings.TrimSuffix(filepath.Base(subFile), ".jsonl") + ".meta.json"
			metaFile := filepath.Join(filepath.Dir(subFile), metaName)
			if data, err := json.Marshal(metaContent); err == nil {
				_ = os.MkdirAll(filepath.Dir(metaFile), 0o700)
				_ = os.WriteFile(metaFile, data, 0o600)
			}
		}
	}
	return nil
}

// isSafeSubpath returns true if subpath is safe to use as a filesystem path
// component under sessionDir.
func isSafeSubpath(subpath string, sessionDir string) bool {
	if subpath == "" {
		return false
	}
	if filepath.IsAbs(subpath) {
		return false
	}
	if strings.HasPrefix(subpath, "/") || strings.HasPrefix(subpath, "\\") {
		return false
	}
	// Check for drive letters (Windows)
	driveRe := regexp.MustCompile(`^[a-zA-Z]:`)
	if driveRe.MatchString(subpath) {
		return false
	}
	for _, part := range regexp.MustCompile(`[\\/]`).Split(subpath, -1) {
		if part == "." || part == ".." {
			return false
		}
	}
	if strings.ContainsRune(subpath, 0) {
		return false
	}
	// Confirm resolution stays under sessionDir.
	target := filepath.Join(sessionDir, subpath) + ".jsonl"
	target = filepath.Clean(target)
	clean := filepath.Clean(sessionDir)
	if !strings.HasPrefix(target, clean+string(os.PathSeparator)) && target != clean {
		return false
	}
	return true
}

// probeListSubkeys calls ListSubkeys with a sentinel key to detect whether the
// store provides a real implementation. Returns ErrNotImplemented when the
// store only has BaseSessionStore; nil or other error when overridden.
func probeListSubkeys(store SessionStore) error {
	_, err := store.ListSubkeys(context.Background(), SessionListSubkeysKey{})
	return err
}

// removeAllBestEffort removes a directory tree, ignoring errors.
func removeAllBestEffort(path string) error {
	return os.RemoveAll(path)
}

// contextWithTimeoutSec returns a context with a timeout in seconds.
func contextWithTimeoutSec(parent context.Context, secs float64) (context.Context, context.CancelFunc) {
	ms := int64(secs * 1000)
	if ms <= 0 {
		ms = 60000
	}
	return context.WithTimeout(parent, time.Duration(ms)*time.Millisecond)
}
