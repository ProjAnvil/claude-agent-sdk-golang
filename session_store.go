package claude

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// ProjectKeyForDirectory derives a portable project key from a directory path.
// The key is normalised to a slash-separated path and truncated to 200 characters.
// When truncation occurs, a djb2 hash of the full path is appended so that
// distinct long paths with the same 200-character prefix are still unique.
func ProjectKeyForDirectory(dir string) string {
	// Normalise path separators to forward slashes for portability.
	clean := filepath.ToSlash(filepath.Clean(dir))

	const maxLen = 200

	if utf8.RuneCountInString(clean) <= maxLen {
		return clean
	}

	// Truncate to maxLen runes and append a djb2 hash of the original.
	runes := []rune(clean)
	truncated := string(runes[:maxLen])
	hash := djb2Hash(clean)
	return truncated + "-" + hash
}

// djb2Hash computes a simple djb2 hash of s and returns a lowercase hex string.
func djb2Hash(s string) string {
	var h uint32 = 5381
	for _, c := range s {
		h = h*33 + uint32(c)
	}
	return uint32ToHex(h)
}

// uint32ToHex converts a uint32 to a lowercase hex string without leading zeros.
func uint32ToHex(n uint32) string {
	if n == 0 {
		return "0"
	}
	const hexDigits = "0123456789abcdef"
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append([]byte{hexDigits[n&0xf]}, buf...)
		n >>= 4
	}
	return string(buf)
}

// FilePathToSessionKey converts a raw .jsonl file path to a SessionKey by
// deriving the project_key from the parent directory and the session_id from
// the file name.
//
// projectsDir is the root projects directory (e.g. ~/.claude/projects).
// Returns nil when the path cannot be parsed as a session file.
func FilePathToSessionKey(filePath, projectsDir string) *SessionKey {
	base := filepath.Base(filePath)
	if !strings.HasSuffix(base, ".jsonl") {
		return nil
	}
	sessionID := strings.TrimSuffix(base, ".jsonl")
	if !validateUUID(sessionID) {
		return nil
	}

	// The project key is stored in the parent directory name relative to projectsDir.
	parent := filepath.Dir(filePath)
	rel, err := filepath.Rel(projectsDir, parent)
	if err != nil {
		return nil
	}

	return &SessionKey{
		ProjectKey: filepath.ToSlash(rel),
		SessionID:  sessionID,
	}
}

// inMemoryEntry is a single appended batch stored in the InMemorySessionStore.
type inMemoryEntry struct {
	mtime   int64
	entries []SessionStoreEntry
}

// InMemorySessionStore is a thread-safe in-process implementation of SessionStore.
//
// It is intended for tests and lightweight integrations that do not need
// persistence.  The store maintains both the raw entry lists and a derived
// SessionSummaryEntry sidecar so that ListSessionSummaries is cheap.
type InMemorySessionStore struct {
	mu        sync.RWMutex
	data      map[string][]inMemoryEntry // key → batches
	summaries map[string]SessionSummaryEntry
	nextMtime int64 // monotonically increasing counter
}

// NewInMemorySessionStore creates an empty InMemorySessionStore.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		data:      make(map[string][]inMemoryEntry),
		summaries: make(map[string]SessionSummaryEntry),
	}
}

func (s *InMemorySessionStore) mkey(key SessionKey) string {
	if key.Subpath != "" {
		return key.ProjectKey + "|" + key.SessionID + "/" + key.Subpath
	}
	return key.ProjectKey + "|" + key.SessionID
}

func (s *InMemorySessionStore) nextMtimeVal() int64 {
	s.nextMtime++
	return s.nextMtime
}

// Append adds entries to the session identified by key.
func (s *InMemorySessionStore) Append(_ context.Context, key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mk := s.mkey(key)
	batch := inMemoryEntry{
		mtime:   s.nextMtimeVal(),
		entries: append([]SessionStoreEntry(nil), entries...),
	}
	s.data[mk] = append(s.data[mk], batch)

	// Update summary sidecar.
	allEntries := s.flatEntries(mk)
	prev, hasPrev := s.summaries[mk]
	if !hasPrev {
		prev = SessionSummaryEntry{
			SessionID: key.SessionID,
		}
	}
	s.summaries[mk] = FoldSessionSummary(&prev, key, allEntries)

	return nil
}

// flatEntries returns all stored entries for mk flattened into one slice.
// Must be called under s.mu.
func (s *InMemorySessionStore) flatEntries(mk string) []SessionStoreEntry {
	batches := s.data[mk]
	var out []SessionStoreEntry
	for _, b := range batches {
		out = append(out, b.entries...)
	}
	return out
}

// Load returns all stored entries for the given key.
func (s *InMemorySessionStore) Load(_ context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mk := s.mkey(key)
	return append([]SessionStoreEntry(nil), s.flatEntries(mk)...), nil
}

// ListSessions returns lightweight list entries for all sessions in a project.
func (s *InMemorySessionStore) ListSessions(_ context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := projectKey + "|"
	var out []SessionStoreListEntry
	seen := make(map[string]bool)
	for mk, batches := range s.data {
		if !strings.HasPrefix(mk, prefix) {
			continue
		}
		sessionID := strings.TrimPrefix(mk, prefix)
		// Skip subkey entries (e.g. "sessionID/subpath")
		if strings.Contains(sessionID, "/") {
			continue
		}
		if seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		var lastMtime int64
		for _, b := range batches {
			if b.mtime > lastMtime {
				lastMtime = b.mtime
			}
		}
		out = append(out, SessionStoreListEntry{SessionID: sessionID, Mtime: lastMtime})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime < out[j].Mtime })
	return out, nil
}

// ListSessionSummaries returns full summary entries for all sessions in a project.
func (s *InMemorySessionStore) ListSessionSummaries(_ context.Context, projectKey string) ([]SessionSummaryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := projectKey + "|"
	var out []SessionSummaryEntry
	for mk, summary := range s.summaries {
		if strings.HasPrefix(mk, prefix) {
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime < out[j].Mtime })
	return out, nil
}

// Delete removes all stored entries for the given key.
func (s *InMemorySessionStore) Delete(_ context.Context, key SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mk := s.mkey(key)
	delete(s.data, mk)
	delete(s.summaries, mk)
	return nil
}

// ListSubkeys returns all distinct subkeys (second path component) for a key.
// For a standard key (no Subpath set), this returns the set of subpaths stored
// under the same ProjectKey+SessionID prefix.
func (s *InMemorySessionStore) ListSubkeys(_ context.Context, key SessionListSubkeysKey) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := key.ProjectKey + "|" + key.SessionID
	var out []string
	seen := make(map[string]bool)
	for mk := range s.data {
		if !strings.HasPrefix(mk, prefix) {
			continue
		}
		rest := strings.TrimPrefix(mk, prefix)
		if rest == "" || rest[0] != '/' {
			continue
		}
		subkey := rest[1:]
		if !seen[subkey] {
			seen[subkey] = true
			out = append(out, subkey)
		}
	}
	sort.Strings(out)
	return out, nil
}

// GetEntries returns a copy of all stored entries for the given key.
// This is a test helper — it is not part of the SessionStore interface.
func (s *InMemorySessionStore) GetEntries(key SessionKey) []SessionStoreEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]SessionStoreEntry(nil), s.flatEntries(s.mkey(key))...)
}

// Size returns the total number of stored entries across all sessions.
// This is a test helper — it is not part of the SessionStore interface.
func (s *InMemorySessionStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, batches := range s.data {
		for _, b := range batches {
			total += len(b.entries)
		}
	}
	return total
}

// Clear removes all stored data from the store.
// This is a test helper — it is not part of the SessionStore interface.
func (s *InMemorySessionStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string][]inMemoryEntry)
	s.summaries = make(map[string]SessionSummaryEntry)
}
