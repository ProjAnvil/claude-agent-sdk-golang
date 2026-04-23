package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// RenameSession renames a session by appending a custom-title entry.
// list_sessions reads the LAST custom-title from the file tail, so repeated calls are safe.
func RenameSession(sessionID, title string, directory *string) error {
	if !validateUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	stripped := strings.TrimSpace(title)
	if stripped == "" {
		return fmt.Errorf("title must be non-empty")
	}

	data, _ := json.Marshal(map[string]interface{}{
		"type":        "custom-title",
		"customTitle": stripped,
		"sessionId":   sessionID,
	})

	return appendToSession(sessionID, string(data)+"\n", directory)
}

// TagSession tags a session. Pass nil tag to clear the tag.
// Appends a {type:'tag',tag:<tag>,sessionId:<id>} JSONL entry.
func TagSession(sessionID string, tag *string, directory *string) error {
	if !validateUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}

	tagValue := ""
	if tag != nil {
		sanitized := strings.TrimSpace(sanitizeUnicode(*tag))
		if sanitized == "" {
			return fmt.Errorf("tag must be non-empty (use nil to clear)")
		}
		tagValue = sanitized
	}

	data, _ := json.Marshal(map[string]interface{}{
		"type":      "tag",
		"tag":       tagValue,
		"sessionId": sessionID,
	})

	return appendToSession(sessionID, string(data)+"\n", directory)
}

// DeleteSession deletes a session by removing its JSONL file and the sibling
// subagent transcript directory (if present).
func DeleteSession(sessionID string, directory *string) error {
	if !validateUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}

	path := findSessionFile(sessionID, directory)
	if path == "" {
		dirMsg := ""
		if directory != nil {
			dirMsg = fmt.Sprintf(" in project directory for %s", *directory)
		}
		return fmt.Errorf("session %s not found%s", sessionID, dirMsg)
	}

	err := os.Remove(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %s not found", sessionID)
		}
		return err
	}

	// Cascade: also remove the sibling subagent transcript directory if present.
	// The directory lives at <projectDir>/<sessionID>/ (same base as the .jsonl).
	siblingDir := strings.TrimSuffix(path, ".jsonl")
	if info, statErr := os.Stat(siblingDir); statErr == nil && info.IsDir() {
		// Best-effort removal; ignore errors.
		_ = os.RemoveAll(siblingDir)
	}

	return nil
}

// ForkSessionResult contains the result of a fork operation.
type ForkSessionResult struct {
	SessionID string `json:"session_id"`
}

// ForkSessionOptions contains options for forking a session.
type ForkSessionOptions struct {
	// SessionID is the UUID of the source session to fork.
	SessionID string
	// Directory is the project directory path. When omitted, all project directories are searched.
	Directory *string
	// UpToMessageID slices transcript up to this message UUID (inclusive).
	UpToMessageID *string
	// Title is the custom title for the fork.
	Title *string
}

// ForkSession forks a session into a new branch with fresh UUIDs.
func ForkSession(opts *ForkSessionOptions) (*ForkSessionResult, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}
	if !validateUUID(opts.SessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", opts.SessionID)
	}
	if opts.UpToMessageID != nil && !validateUUID(*opts.UpToMessageID) {
		return nil, fmt.Errorf("invalid up_to_message_id: %s", *opts.UpToMessageID)
	}

	filePath, projectDir := findSessionFileWithDir(opts.SessionID, opts.Directory)
	if filePath == "" {
		dirMsg := ""
		if opts.Directory != nil {
			dirMsg = fmt.Sprintf(" in project directory for %s", *opts.Directory)
		}
		return nil, fmt.Errorf("session %s not found%s", opts.SessionID, dirMsg)
	}

	content, err := os.ReadFile(filePath)
	if err != nil || len(content) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", opts.SessionID)
	}

	transcript, contentReplacements := parseForkTranscript(content, opts.SessionID)

	// Filter out sidechains
	filtered := make([]map[string]interface{}, 0, len(transcript))
	for _, entry := range transcript {
		if isSidechain, _ := entry["isSidechain"].(bool); !isSidechain {
			filtered = append(filtered, entry)
		}
	}
	transcript = filtered

	if len(transcript) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", opts.SessionID)
	}

	if opts.UpToMessageID != nil {
		cutoff := -1
		for i, entry := range transcript {
			if uuid, _ := entry["uuid"].(string); uuid == *opts.UpToMessageID {
				cutoff = i
				break
			}
		}
		if cutoff == -1 {
			return nil, fmt.Errorf("message %s not found in session %s", *opts.UpToMessageID, opts.SessionID)
		}
		transcript = transcript[:cutoff+1]
	}

	// Build UUID mapping (including progress entries for parentUuid chain)
	uuidMapping := make(map[string]string)
	for _, entry := range transcript {
		if uuid, _ := entry["uuid"].(string); uuid != "" {
			uuidMapping[uuid] = generateUUID()
		}
	}

	// Filter out progress messages from writable output
	writable := make([]map[string]interface{}, 0, len(transcript))
	for _, entry := range transcript {
		if entryType, _ := entry["type"].(string); entryType != "progress" {
			writable = append(writable, entry)
		}
	}
	if len(writable) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", opts.SessionID)
	}

	// Index by uuid for parent resolution
	byUUID := make(map[string]map[string]interface{})
	for _, entry := range transcript {
		if uuid, _ := entry["uuid"].(string); uuid != "" {
			byUUID[uuid] = entry
		}
	}

	forkedSessionID := generateUUID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	now = strings.Replace(now, "+00:00", "Z", 1)
	if !strings.HasSuffix(now, "Z") {
		now = strings.TrimRight(now, "0")
		if strings.HasSuffix(now, ".") {
			now = now[:len(now)-1]
		}
		now += "Z"
	}

	lines := make([]string, 0, len(writable)+2)

	for i, original := range writable {
		origUUID, _ := original["uuid"].(string)
		newUUID := uuidMapping[origUUID]

		// Resolve parentUuid, skipping progress ancestors
		var newParentUUID interface{} = nil
		parentID, _ := original["parentUuid"].(string)
		for parentID != "" {
			parent, ok := byUUID[parentID]
			if !ok {
				break
			}
			if parentType, _ := parent["type"].(string); parentType != "progress" {
				if mapped, ok := uuidMapping[parentID]; ok {
					newParentUUID = mapped
				}
				break
			}
			parentID, _ = parent["parentUuid"].(string)
		}

		// Only update timestamp on the last message
		timestamp := now
		if i != len(writable)-1 {
			if ts, ok := original["timestamp"].(string); ok {
				timestamp = ts
			}
		}

		// Remap logicalParentUuid
		var newLogicalParent interface{} = nil
		if logical, ok := original["logicalParentUuid"].(string); ok && logical != "" {
			if mapped, ok := uuidMapping[logical]; ok {
				newLogicalParent = mapped
			}
		} else if original["logicalParentUuid"] != nil {
			newLogicalParent = original["logicalParentUuid"]
		}

		forked := make(map[string]interface{})
		for k, v := range original {
			forked[k] = v
		}
		forked["uuid"] = newUUID
		forked["parentUuid"] = newParentUUID
		forked["logicalParentUuid"] = newLogicalParent
		forked["sessionId"] = forkedSessionID
		forked["timestamp"] = timestamp
		forked["isSidechain"] = false
		forked["forkedFrom"] = map[string]interface{}{
			"sessionId":   opts.SessionID,
			"messageUuid": origUUID,
		}

		// Remove fields that would leak state
		delete(forked, "teamName")
		delete(forked, "agentName")
		delete(forked, "slug")
		delete(forked, "sourceToolAssistantUUID")

		line, _ := json.Marshal(forked)
		lines = append(lines, string(line))
	}

	// Append content-replacement entry if any
	if len(contentReplacements) > 0 {
		crEntry, _ := json.Marshal(map[string]interface{}{
			"type":         "content-replacement",
			"sessionId":    forkedSessionID,
			"replacements": contentReplacements,
		})
		lines = append(lines, string(crEntry))
	}

	// Derive title
	var forkTitle string
	if opts.Title != nil {
		forkTitle = strings.TrimSpace(*opts.Title)
	}
	if forkTitle == "" {
		bufLen := len(content)
		headEnd := bufLen
		if headEnd > liteReadBufSize {
			headEnd = liteReadBufSize
		}
		head := string(content[:headEnd])

		tailStart := bufLen - liteReadBufSize
		if tailStart < 0 {
			tailStart = 0
		}
		tail := string(content[tailStart:])

		base := extractLastJSONStringField(tail, "customTitle")
		if base == "" {
			base = extractLastJSONStringField(head, "customTitle")
		}
		if base == "" {
			base = extractLastJSONStringField(tail, "aiTitle")
		}
		if base == "" {
			base = extractLastJSONStringField(head, "aiTitle")
		}
		if base == "" {
			base = extractFirstPromptFromHead(head)
		}
		if base == "" {
			base = "Forked session"
		}
		forkTitle = base + " (fork)"
	}

	titleEntry, _ := json.Marshal(map[string]interface{}{
		"type":        "custom-title",
		"sessionId":   forkedSessionID,
		"customTitle": forkTitle,
	})
	lines = append(lines, string(titleEntry))

	forkPath := filepath.Join(projectDir, forkedSessionID+".jsonl")
	f, err := os.OpenFile(forkPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	_, err = f.WriteString(strings.Join(lines, "\n") + "\n")
	if err != nil {
		return nil, err
	}

	return &ForkSessionResult{SessionID: forkedSessionID}, nil
}

// findSessionFile finds the path to a session's JSONL file.
func findSessionFile(sessionID string, directory *string) string {
	result, _ := findSessionFileWithDir(sessionID, directory)
	return result
}

// findSessionFileWithDir finds a session file and its containing project directory.
func findSessionFileWithDir(sessionID string, directory *string) (string, string) {
	fileName := sessionID + ".jsonl"

	tryDir := func(projectDir string) (string, string) {
		path := filepath.Join(projectDir, fileName)
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			return path, projectDir
		}
		return "", ""
	}

	if directory != nil {
		canonical, err := canonicalizePath(*directory)
		if err != nil {
			return "", ""
		}

		projectDir, err := findProjectDir(canonical)
		if err == nil {
			if path, dir := tryDir(projectDir); path != "" {
				return path, dir
			}
		}

		worktreePaths, _ := getWorktreePaths(canonical)
		for _, wt := range worktreePaths {
			if wt == canonical {
				continue
			}
			wtProjectDir, err := findProjectDir(wt)
			if err == nil {
				if path, dir := tryDir(wtProjectDir); path != "" {
					return path, dir
				}
			}
		}
		return "", ""
	}

	projectsDir, err := getProjectsDir()
	if err != nil {
		return "", ""
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", ""
	}

	for _, entry := range entries {
		if path, dir := tryDir(filepath.Join(projectsDir, entry.Name())); path != "" {
			return path, dir
		}
	}
	return "", ""
}

// generateUUID generates a new random UUID v4.
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// appendToSession appends data to an existing session file.
func appendToSession(sessionID, data string, directory *string) error {
	fileName := sessionID + ".jsonl"

	tryAppend := func(path string) bool {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return false
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil || info.Size() == 0 {
			return false
		}

		_, err = f.WriteString(data)
		return err == nil
	}

	if directory != nil {
		canonical, err := canonicalizePath(*directory)
		if err != nil {
			return fmt.Errorf("session %s not found in project directory for %s", sessionID, *directory)
		}

		projectDir, err := findProjectDir(canonical)
		if err == nil {
			if tryAppend(filepath.Join(projectDir, fileName)) {
				return nil
			}
		}

		worktreePaths, _ := getWorktreePaths(canonical)
		for _, wt := range worktreePaths {
			if wt == canonical {
				continue
			}
			wtProjectDir, err := findProjectDir(wt)
			if err == nil {
				if tryAppend(filepath.Join(wtProjectDir, fileName)) {
					return nil
				}
			}
		}

		return fmt.Errorf("session %s not found in project directory for %s", sessionID, *directory)
	}

	projectsDir, err := getProjectsDir()
	if err != nil {
		return fmt.Errorf("session %s not found (no projects directory)", sessionID)
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return fmt.Errorf("session %s not found (no projects directory)", sessionID)
	}

	for _, entry := range entries {
		if tryAppend(filepath.Join(projectsDir, entry.Name(), fileName)) {
			return nil
		}
	}

	return fmt.Errorf("session %s not found in any project directory", sessionID)
}

var (
	forkTranscriptTypes = map[string]bool{
		"user": true, "assistant": true, "attachment": true,
		"system": true, "progress": true,
	}
)

// parseForkTranscript parses JSONL content into transcript entries + content-replacement records.
func parseForkTranscript(content []byte, sessionID string) ([]map[string]interface{}, []interface{}) {
	var transcript []map[string]interface{}
	var contentReplacements []interface{}

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		entryType, _ := entry["type"].(string)
		uuid, hasUUID := entry["uuid"].(string)

		if forkTranscriptTypes[entryType] && hasUUID && uuid != "" {
			transcript = append(transcript, entry)
		} else if entryType == "content-replacement" {
			if sid, _ := entry["sessionId"].(string); sid == sessionID {
				if replacements, ok := entry["replacements"].([]interface{}); ok {
					contentReplacements = append(contentReplacements, replacements...)
				}
			}
		}
	}

	return transcript, contentReplacements
}

// Unicode sanitization regex
var unicodeStripRE = regexp.MustCompile(
	"[\u200b-\u200f\u202a-\u202e\u2066-\u2069\ufeff\ue000-\uf8ff]",
)

// sanitizeUnicode removes dangerous Unicode characters from a string.
func sanitizeUnicode(value string) string {
	current := value
	for range 10 {
		previous := current
		// Strip format (Cf), private use (Co) categories and explicit ranges
		filtered := make([]rune, 0, len(current))
		for _, r := range current {
			if !isFormatCategory(r) {
				filtered = append(filtered, r)
			}
		}
		current = string(filtered)
		// Explicit range stripping
		current = unicodeStripRE.ReplaceAllString(current, "")
		if current == previous {
			break
		}
	}
	return current
}

// isFormatCategory checks if a rune is in Cf, Co, or Cn Unicode categories.
func isFormatCategory(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) ||
		!unicode.IsGraphic(r) && !unicode.IsSpace(r) && r != '\n' && r != '\r' && r != '\t'
}

// ---------------------------------------------------------------------------
// Store-backed session mutations
// ---------------------------------------------------------------------------

// RenameSessionViaStore renames a session by appending a custom-title entry
// to a SessionStore.
//
// This is the store-backed counterpart to RenameSession.
func RenameSessionViaStore(ctx context.Context, store SessionStore, sessionID, title string, directory *string) error {
	if !validateUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	stripped := strings.TrimSpace(title)
	if stripped == "" {
		return fmt.Errorf("title must be non-empty")
	}
	dir := ""
	if directory != nil {
		dir = *directory
	}
	projectKey := ProjectKeyForDirectory(dir)
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	entry := SessionStoreEntry{
		"type":        "custom-title",
		"customTitle": stripped,
		"sessionId":   sessionID,
		"uuid":        generateUUID(),
		"timestamp":   isoNow(),
	}
	return store.Append(ctx, key, []SessionStoreEntry{entry})
}

// TagSessionViaStore tags a session by appending a tag entry to a SessionStore.
// Pass nil for tag to clear the tag.
//
// This is the store-backed counterpart to TagSession.
func TagSessionViaStore(ctx context.Context, store SessionStore, sessionID string, tag *string, directory *string) error {
	if !validateUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	dir := ""
	if directory != nil {
		dir = *directory
	}
	var tagValue string
	if tag != nil {
		sanitized := strings.TrimSpace(sanitizeUnicode(*tag))
		if sanitized == "" {
			return fmt.Errorf("tag must be non-empty (use nil to clear)")
		}
		tagValue = sanitized
	}
	projectKey := ProjectKeyForDirectory(dir)
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	entry := SessionStoreEntry{
		"type":      "tag",
		"tag":       tagValue, // empty string means clear
		"sessionId": sessionID,
		"uuid":      generateUUID(),
		"timestamp": isoNow(),
	}
	return store.Append(ctx, key, []SessionStoreEntry{entry})
}

// DeleteSessionViaStore deletes a session from a SessionStore.
//
// This is the store-backed counterpart to DeleteSession. If the store does not
// implement Delete (returns ErrNotImplemented), deletion is silently skipped —
// appropriate for WORM/append-only backends.
func DeleteSessionViaStore(ctx context.Context, store SessionStore, sessionID string, directory *string) error {
	if !validateUUID(sessionID) {
		return fmt.Errorf("invalid session_id: %s", sessionID)
	}
	dir := ""
	if directory != nil {
		dir = *directory
	}
	projectKey := ProjectKeyForDirectory(dir)
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	if err := store.Delete(ctx, key); err != nil {
		if errors.Is(err, ErrNotImplemented) {
			return nil // no-op for WORM stores
		}
		return err
	}
	return nil
}

// ForkSessionViaStoreOptions contains options for ForkSessionViaStore.
type ForkSessionViaStoreOptions struct {
	// Store is the session store to fork from/to. Required.
	Store SessionStore
	// SessionID is the UUID of the source session.
	SessionID string
	// Directory is the project directory used to compute the project_key.
	Directory *string
	// UpToMessageID slices the transcript at this message UUID (inclusive).
	// If empty, copies the full transcript.
	UpToMessageID string
	// Title is the custom title for the fork.
	// If empty, derives from the original title + " (fork)".
	Title string
}

// ForkSessionViaStore forks a session into a new branch with fresh UUIDs via
// a SessionStore.
//
// This is the store-backed counterpart to ForkSession. Runs the fork transform
// directly over the objects returned by SessionStore.Load — no JSONL round-trip.
//
// Returns ForkSessionResult with the new session's UUID, or an error if the
// source session is not found or has no messages.
func ForkSessionViaStore(ctx context.Context, opts *ForkSessionViaStoreOptions) (*ForkSessionResult, error) {
	if opts == nil || opts.Store == nil {
		return nil, fmt.Errorf("opts.Store is required")
	}
	if !validateUUID(opts.SessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", opts.SessionID)
	}
	if opts.UpToMessageID != "" && !validateUUID(opts.UpToMessageID) {
		return nil, fmt.Errorf("invalid up_to_message_id: %s", opts.UpToMessageID)
	}

	dir := ""
	if opts.Directory != nil {
		dir = *opts.Directory
	}
	projectKey := ProjectKeyForDirectory(dir)
	srcKey := SessionKey{ProjectKey: projectKey, SessionID: opts.SessionID}
	loaded, err := opts.Store.Load(ctx, srcKey)
	if err != nil {
		return nil, err
	}
	if len(loaded) == 0 {
		return nil, fmt.Errorf("session %s not found", opts.SessionID)
	}

	// Serialize to JSONL and parse using the existing fork machinery.
	jsonl := entriesToJSONL(loaded)
	if jsonl == "" {
		return nil, fmt.Errorf("session %s has no entries", opts.SessionID)
	}

	// Derive title from entries if not provided.
	var titlePtr *string
	if opts.Title != "" {
		titlePtr = &opts.Title
	} else {
		if derived := deriveTitleFromEntries(loaded); derived != "" {
			t := derived + " (fork)"
			titlePtr = &t
		}
	}

	var upTo *string
	if opts.UpToMessageID != "" {
		upTo = &opts.UpToMessageID
	}

	transcript, contentReplacements := parseForkTranscript([]byte(jsonl), opts.SessionID)

	// Filter sidechains
	filtered := make([]map[string]interface{}, 0, len(transcript))
	for _, entry := range transcript {
		if isSidechain, _ := entry["isSidechain"].(bool); !isSidechain {
			filtered = append(filtered, entry)
		}
	}
	transcript = filtered
	if len(transcript) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", opts.SessionID)
	}

	// Handle upToMessageID slicing
	if upTo != nil {
		cutoff := -1
		for i, entry := range transcript {
			if uuid, _ := entry["uuid"].(string); uuid == *upTo {
				cutoff = i
				break
			}
		}
		if cutoff == -1 {
			return nil, fmt.Errorf("message %s not found in session %s", *upTo, opts.SessionID)
		}
		transcript = transcript[:cutoff+1]
	}

	// Build UUID mapping
	uuidMapping := make(map[string]string)
	for _, entry := range transcript {
		if uuid, _ := entry["uuid"].(string); uuid != "" {
			uuidMapping[uuid] = generateUUID()
		}
	}

	// Filter progress messages for output
	writable := make([]map[string]interface{}, 0, len(transcript))
	for _, entry := range transcript {
		if entryType, _ := entry["type"].(string); entryType != "progress" {
			writable = append(writable, entry)
		}
	}
	if len(writable) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", opts.SessionID)
	}

	// Index for parent resolution
	byUUID := make(map[string]map[string]interface{})
	for _, entry := range transcript {
		if uuid, _ := entry["uuid"].(string); uuid != "" {
			byUUID[uuid] = entry
		}
	}

	forkedSessionID := generateUUID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	now = strings.Replace(now, "+00:00", "Z", 1)
	if !strings.HasSuffix(now, "Z") {
		now = strings.TrimRight(now, "0")
		if strings.HasSuffix(now, ".") {
			now = now[:len(now)-1]
		}
		now += "Z"
	}

	lines := make([]string, 0, len(writable)+2)
	for i, original := range writable {
		origUUID, _ := original["uuid"].(string)
		newUUID := uuidMapping[origUUID]

		var newParentUUID interface{} = nil
		parentID, _ := original["parentUuid"].(string)
		for parentID != "" {
			parent, ok := byUUID[parentID]
			if !ok {
				break
			}
			if parentType, _ := parent["type"].(string); parentType != "progress" {
				if mapped, ok := uuidMapping[parentID]; ok {
					newParentUUID = mapped
				}
				break
			}
			parentID, _ = parent["parentUuid"].(string)
		}

		timestamp := now
		if i != len(writable)-1 {
			if ts, ok := original["timestamp"].(string); ok {
				timestamp = ts
			}
		}

		var newLogicalParent interface{} = nil
		if logical, ok := original["logicalParentUuid"].(string); ok && logical != "" {
			if mapped, ok := uuidMapping[logical]; ok {
				newLogicalParent = mapped
			}
		} else if original["logicalParentUuid"] != nil {
			newLogicalParent = original["logicalParentUuid"]
		}

		forked := make(map[string]interface{})
		for k, v := range original {
			forked[k] = v
		}
		forked["uuid"] = newUUID
		forked["parentUuid"] = newParentUUID
		forked["logicalParentUuid"] = newLogicalParent
		forked["sessionId"] = forkedSessionID
		forked["timestamp"] = timestamp
		forked["isSidechain"] = false
		forked["forkedFrom"] = map[string]interface{}{
			"sessionId":   opts.SessionID,
			"messageUuid": origUUID,
		}
		delete(forked, "teamName")
		delete(forked, "agentName")
		delete(forked, "slug")
		delete(forked, "sourceToolAssistantUUID")

		line, _ := json.Marshal(forked)
		lines = append(lines, string(line))
	}

	if len(contentReplacements) > 0 {
		crEntry, _ := json.Marshal(map[string]interface{}{
			"type":         "content-replacement",
			"sessionId":    forkedSessionID,
			"replacements": contentReplacements,
		})
		lines = append(lines, string(crEntry))
	}

	// Title entry
	var forkTitle string
	if titlePtr != nil {
		forkTitle = strings.TrimSpace(*titlePtr)
	}
	if forkTitle == "" {
		forkTitle = "Forked session"
	}
	titleEntry, _ := json.Marshal(map[string]interface{}{
		"type":        "custom-title",
		"sessionId":   forkedSessionID,
		"customTitle": forkTitle,
	})
	lines = append(lines, string(titleEntry))

	// Parse lines back to entries and write to store
	var newEntries []SessionStoreEntry
	for _, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			newEntries = append(newEntries, SessionStoreEntry(entry))
		}
	}
	if len(newEntries) == 0 {
		return nil, fmt.Errorf("session %s has no messages to fork", opts.SessionID)
	}
	dstKey := SessionKey{ProjectKey: projectKey, SessionID: forkedSessionID}
	if err := opts.Store.Append(ctx, dstKey, newEntries); err != nil {
		return nil, err
	}
	return &ForkSessionResult{SessionID: forkedSessionID}, nil
}

// deriveTitleFromEntries scans SessionStoreEntry values for the last
// custom-title or ai-title to use as the fork source title.
func deriveTitleFromEntries(entries []SessionStoreEntry) string {
	title := ""
	for _, e := range entries {
		t, _ := e["type"].(string)
		switch t {
		case "custom-title":
			if ct, ok := e["customTitle"].(string); ok && ct != "" {
				title = ct
			}
		case "ai-title":
			if at, ok := e["aiTitle"].(string); ok && at != "" {
				if title == "" {
					title = at
				}
			}
		}
	}
	return title
}

// isoNow returns the current time as an ISO 8601 / RFC3339 timestamp string.
func isoNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}
