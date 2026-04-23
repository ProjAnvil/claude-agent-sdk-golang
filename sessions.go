package claude

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
	"sort"
	"strings"
	"sync"
	"time"
)

// Constants for session management
const (
	liteReadBufSize    = 65536
	maxSanitizedLength = 200
)

// Regular expressions
var (
	uuidRE = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// Pattern matching auto-generated or system messages that should be skipped
	skipFirstPromptPattern = regexp.MustCompile(`^(?:<local-command-stdout>|<session-start-hook>|<tick>|<goal>|` +
		`\[Request interrupted by user[^\]]*\]|` +
		`\s*<ide_opened_file>[\s\S]*</ide_opened_file>\s*$|` +
		`\s*<ide_selection>[\s\S]*</ide_selection>\s*$)`)

	commandNameRE = regexp.MustCompile(`<command-name>(.*?)</command-name>`)
	sanitizeRE    = regexp.MustCompile(`[^a-zA-Z0-9]`)
)

// liteSessionFile represents the result of reading a session file's head, tail, mtime and size.
type liteSessionFile struct {
	mtime int64
	size  int64
	head  string
	tail  string
}

// ListSessionsOptions contains options for listing sessions.
type ListSessionsOptions struct {
	// Directory to list sessions for. If nil, returns sessions across all projects.
	Directory *string
	// Maximum number of sessions to return.
	Limit *int
	// Number of sessions to skip from the start of the sorted result set.
	// Use with Limit for pagination. Defaults to 0.
	Offset int
	// Include sessions from git worktrees when directory is provided.
	IncludeWorktrees bool
}

// ListSessions returns session metadata extracted from stat + head/tail reads.
//
// When Directory is provided, returns sessions for that project directory and its
// git worktrees. When omitted, returns sessions across all projects.
func ListSessions(opts *ListSessionsOptions) ([]SDKSessionInfo, error) {
	if opts == nil {
		opts = &ListSessionsOptions{}
	}

	if opts.Directory != nil {
		return listSessionsForProject(*opts.Directory, opts.Limit, opts.Offset, opts.IncludeWorktrees)
	}
	return listAllSessions(opts.Limit, opts.Offset)
}

// GetSessionMessagesOptions contains options for getting session messages.
type GetSessionMessagesOptions struct {
	// SessionID is the UUID of the session to read.
	SessionID string
	// Directory to find the session in. If nil, searches all project directories.
	Directory *string
	// Maximum number of messages to return.
	Limit *int
	// Number of messages to skip from the start.
	Offset int
}

// GetSessionMessages reads a session's conversation messages from its JSONL transcript file.
//
// Parses the full JSONL, builds the conversation chain via parentUuid links,
// and returns user/assistant messages in chronological order.
func GetSessionMessages(opts *GetSessionMessagesOptions) ([]SessionMessage, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}

	if !validateUUID(opts.SessionID) {
		return nil, fmt.Errorf("invalid session ID: %s", opts.SessionID)
	}

	var directory string
	if opts.Directory != nil {
		directory = *opts.Directory
	}

	content, err := readSessionFile(opts.SessionID, directory)
	if err != nil {
		return nil, err
	}
	if content == "" {
		return []SessionMessage{}, nil
	}

	entries := parseTranscriptEntries(content)
	chain := buildConversationChain(entries)
	visible := filterVisibleMessages(chain)
	messages := toSessionMessages(visible)

	// Apply offset and limit
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(messages) {
		return []SessionMessage{}, nil
	}

	if opts.Limit != nil && *opts.Limit > 0 {
		end := offset + *opts.Limit
		if end > len(messages) {
			end = len(messages)
		}
		return messages[offset:end], nil
	}
	return messages[offset:], nil
}

// validateUUID returns the string if it is a valid UUID, else empty string.
func validateUUID(maybeUUID string) bool {
	return uuidRE.MatchString(maybeUUID)
}

// simpleHash implements a 32-bit integer hash, base36 encoded.
// Port of the JS simpleHash function.
func simpleHash(s string) string {
	h := 0
	for _, ch := range s {
		char := ch
		h = (h << 5) - h + int(char)
		// Emulate JS hash |= 0 (coerce to 32-bit signed int)
		h = h & 0xFFFFFFFF
		if h >= 0x80000000 {
			h -= 0x100000000
		}
	}
	h = abs(h)
	if h == 0 {
		return "0"
	}

	digits := "0123456789abcdefghijklmnopqrstuvwxyz"
	out := []rune{}
	n := h
	for n > 0 {
		out = append(out, rune(digits[n%36]))
		n /= 36
	}
	// Reverse the slice
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// sanitizePath makes a string safe for use as a directory name.
func sanitizePath(name string) string {
	sanitized := sanitizeRE.ReplaceAllString(name, "-")
	if len(sanitized) <= maxSanitizedLength {
		return sanitized
	}
	h := simpleHash(name)
	return sanitized[:maxSanitizedLength] + "-" + h
}

// getClaudeConfigHomeDir returns the Claude config directory (respects CLAUDE_CONFIG_DIR).
func getClaudeConfigHomeDir() (string, error) {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir != "" {
		return configDir, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".claude"), nil
}

// getProjectsDir returns the projects directory under .claude.
func getProjectsDir() (string, error) {
	configDir, err := getClaudeConfigHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "projects"), nil
}

// getProjectDir returns the project directory for a given project path.
func getProjectDir(projectPath string) (string, error) {
	projectsDir, err := getProjectsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(projectsDir, sanitizePath(projectPath)), nil
}

// canonicalizePath resolves a directory path to its canonical form.
func canonicalizePath(d string) (string, error) {
	// Use EvalSymlinks to resolve the real path (cross-platform equivalent of realpath)
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		return d, nil // Return original on error
	}
	return filepath.Clean(resolved), nil
}

// findProjectDir finds the project directory for a given path.
// Tolerates hash mismatches for long paths (>200 chars).
func findProjectDir(projectPath string) (string, error) {
	exact, err := getProjectDir(projectPath)
	if err != nil {
		return "", err
	}

	// Check if exact match exists
	if info, err := os.Stat(exact); err == nil && info.IsDir() {
		return exact, nil
	}

	// For short paths, no match means no sessions
	sanitized := sanitizePath(projectPath)
	if len(sanitized) <= maxSanitizedLength {
		return "", fmt.Errorf("project directory not found")
	}

	// For long paths, try prefix matching
	projectsDir, err := getProjectsDir()
	if err != nil {
		return "", err
	}

	prefix := sanitized[:maxSanitizedLength]
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix+"-") {
			return filepath.Join(projectsDir, entry.Name()), nil
		}
	}

	return "", fmt.Errorf("project directory not found")
}

// unescapeJSONString unescapes a JSON string value extracted as raw text.
func unescapeJSONString(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	// Try to unescape by wrapping in quotes and parsing as JSON
	var result string
	err := json.Unmarshal([]byte(`"`+raw+`"`), &result)
	if err == nil {
		return result
	}
	return raw
}

// extractJSONStringField extracts a simple JSON string field value without full parsing.
func extractJSONStringField(text, key string) string {
	patterns := []string{`"` + key + `":"`, `"` + key + `": "`}
	for _, pattern := range patterns {
		idx := strings.Index(text, pattern)
		if idx < 0 {
			continue
		}

		valueStart := idx + len(pattern)
		i := valueStart
		for i < len(text) {
			if text[i] == '\\' {
				i += 2
				continue
			}
			if text[i] == '"' {
				return unescapeJSONString(text[valueStart:i])
			}
			i++
		}
	}
	return ""
}

// extractLastJSONStringField finds the LAST occurrence of a JSON string field.
func extractLastJSONStringField(text, key string) string {
	patterns := []string{`"` + key + `":"`, `"` + key + `": "`}
	var lastValue string

	for _, pattern := range patterns {
		searchFrom := 0
		for {
			idx := strings.Index(text[searchFrom:], pattern)
			if idx < 0 {
				break
			}
			idx += searchFrom

			valueStart := idx + len(pattern)
			i := valueStart
			for i < len(text) {
				if text[i] == '\\' {
					i += 2
					continue
				}
				if text[i] == '"' {
					lastValue = unescapeJSONString(text[valueStart:i])
					break
				}
				i++
			}
			searchFrom = i + 1
		}
	}
	return lastValue
}

// extractFirstPromptFromHead extracts the first meaningful user prompt from a JSONL head chunk.
func extractFirstPromptFromHead(head string) string {
	lines := strings.Split(head, "\n")
	commandFallback := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Must be a user message
		if !strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type": "user"`) {
			continue
		}
		// Skip tool_result messages
		if strings.Contains(line, `"tool_result"`) {
			continue
		}
		// Skip isMeta and isCompactSummary messages
		if strings.Contains(line, `"isMeta":true`) || strings.Contains(line, `"isMeta": true`) {
			continue
		}
		if strings.Contains(line, `"isCompactSummary":true`) || strings.Contains(line, `"isCompactSummary": true`) {
			continue
		}

		// Parse the line as JSON
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entryType, ok := entry["type"].(string); !ok || entryType != "user" {
			continue
		}

		message, ok := entry["message"].(map[string]interface{})
		if !ok {
			continue
		}

		content := message["content"]
		var texts []string

		if contentStr, ok := content.(string); ok {
			texts = append(texts, contentStr)
		} else if contentList, ok := content.([]interface{}); ok {
			for _, block := range contentList {
				if blockMap, ok := block.(map[string]interface{}); ok {
					if blockType, ok := blockMap["type"].(string); ok && blockType == "text" {
						if text, ok := blockMap["text"].(string); ok {
							texts = append(texts, text)
						}
					}
				}
			}
		}

		for _, raw := range texts {
			result := strings.ReplaceAll(raw, "\n", " ")
			result = strings.TrimSpace(result)
			if result == "" {
				continue
			}

			// Skip slash-command messages but remember first as fallback
			if match := commandNameRE.FindStringSubmatch(result); len(match) > 1 {
				if commandFallback == "" {
					commandFallback = match[1]
				}
				continue
			}

			if skipFirstPromptPattern.MatchString(result) {
				continue
			}

			if len(result) > 200 {
				result = result[:200] + "\u2026"
			}
			return result
		}
	}

	if commandFallback != "" {
		return commandFallback
	}
	return ""
}

// readSessionLite opens a session file, stats it, and reads head + tail.
func readSessionLite(filePath string) *liteSessionFile {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil
	}

	size := stat.Size()
	mtime := stat.ModTime().UnixMilli()

	if size == 0 {
		return nil
	}

	// Read head
	headBuf := make([]byte, min(size, liteReadBufSize))
	if _, err := file.Read(headBuf); err != nil {
		return nil
	}
	head := string(headBuf)

	// Read tail
	var tail string
	tailOffset := max(0, size-liteReadBufSize)
	if tailOffset == 0 {
		tail = head
	} else {
		if _, err := file.Seek(tailOffset, 0); err != nil {
			return nil
		}
		tailBuf := make([]byte, min(size-tailOffset, liteReadBufSize))
		if _, err := file.Read(tailBuf); err != nil {
			return nil
		}
		tail = string(tailBuf)
	}

	return &liteSessionFile{
		mtime: mtime,
		size:  size,
		head:  head,
		tail:  tail,
	}
}

// getWorktreePaths returns absolute worktree paths for the git repo containing cwd.
func getWorktreePaths(cwd string) ([]string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = cwd
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var paths []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimSpace(line[len("worktree "):])
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// parseSessionInfoFromLite parses SDKSessionInfo fields from a lite session read.
// Returns nil for sidechain sessions or metadata-only sessions with no extractable summary.
// Shared by readSessionsFromDir and GetSessionInfo.
func parseSessionInfoFromLite(sessionID string, lite *liteSessionFile, projectPath string) *SDKSessionInfo {
	head, tail, mtime, size := lite.head, lite.tail, lite.mtime, lite.size

	// Check first line for sidechain sessions
	firstNewline := strings.Index(head, "\n")
	firstLine := head
	if firstNewline >= 0 {
		firstLine = head[:firstNewline]
	}
	if strings.Contains(firstLine, `"isSidechain":true`) || strings.Contains(firstLine, `"isSidechain": true`) {
		return nil
	}

	// User-set title (customTitle) wins over AI-generated title (aiTitle).
	// Head fallback covers short sessions where the title entry may not be in tail.
	customTitle := extractLastJSONStringField(tail, "customTitle")
	if customTitle == "" {
		customTitle = extractLastJSONStringField(head, "customTitle")
	}
	if customTitle == "" {
		customTitle = extractLastJSONStringField(tail, "aiTitle")
	}
	if customTitle == "" {
		customTitle = extractLastJSONStringField(head, "aiTitle")
	}

	firstPrompt := extractFirstPromptFromHead(head)

	// lastPrompt tail entry shows what the user was most recently doing.
	summary := customTitle
	if summary == "" {
		summary = extractLastJSONStringField(tail, "lastPrompt")
	}
	if summary == "" {
		summary = extractLastJSONStringField(tail, "summary")
	}
	if summary == "" {
		summary = firstPrompt
	}

	// Skip metadata-only sessions (no title, no summary, no prompt)
	if summary == "" {
		return nil
	}

	gitBranch := extractLastJSONStringField(tail, "gitBranch")
	if gitBranch == "" {
		gitBranch = extractJSONStringField(head, "gitBranch")
	}

	sessionCWD := extractJSONStringField(head, "cwd")
	if sessionCWD == "" && projectPath != "" {
		sessionCWD = projectPath
	}

	// Scope tag extraction to {"type":"tag"} lines — a bare tail scan for
	// "tag" would match tool_use inputs (git tag, Docker tags, etc.)
	var tag string
	tailLines := strings.Split(tail, "\n")
	for i := len(tailLines) - 1; i >= 0; i-- {
		if strings.HasPrefix(tailLines[i], `{"type":"tag"`) {
			tag = extractLastJSONStringField(tailLines[i], "tag")
			break
		}
	}

	// created_at from first entry's ISO timestamp (epoch ms)
	var createdAt *int64
	firstTimestamp := extractJSONStringField(firstLine, "timestamp")
	if firstTimestamp != "" {
		// Go's time.Parse doesn't handle trailing 'Z' formatting issues like Python
		ts := firstTimestamp
		if strings.HasSuffix(ts, "Z") {
			ts = ts[:len(ts)-1] + "+00:00"
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			millis := t.UnixMilli()
			createdAt = &millis
		} else if t, err := time.Parse("2006-01-02T15:04:05.999999999-07:00", ts); err == nil {
			millis := t.UnixMilli()
			createdAt = &millis
		}
	}

	sessionInfo := SDKSessionInfo{
		SessionID:    sessionID,
		Summary:      summary,
		LastModified: mtime,
		FileSize:     &size,
		CreatedAt:    createdAt,
	}
	if customTitle != "" {
		sessionInfo.CustomTitle = &customTitle
	}
	if firstPrompt != "" {
		sessionInfo.FirstPrompt = &firstPrompt
	}
	if gitBranch != "" {
		sessionInfo.GitBranch = &gitBranch
	}
	if sessionCWD != "" {
		sessionInfo.CWD = &sessionCWD
	}
	if tag != "" {
		sessionInfo.Tag = &tag
	}

	return &sessionInfo
}

// readSessionsFromDir reads session files from a single project directory.
func readSessionsFromDir(projectDir, projectPath string) []SDKSessionInfo {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}

	results := []SDKSessionInfo{}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		sessionID := name[:len(name)-6] // Remove .jsonl suffix
		if !validateUUID(sessionID) {
			continue
		}

		lite := readSessionLite(filepath.Join(projectDir, name))
		if lite == nil {
			continue
		}

		info := parseSessionInfoFromLite(sessionID, lite, projectPath)
		if info != nil {
			results = append(results, *info)
		}
	}

	return results
}

// deduplicateBySessionID deduplicates sessions by session_id, keeping the newest last_modified.
func deduplicateBySessionID(sessions []SDKSessionInfo) []SDKSessionInfo {
	byID := make(map[string]SDKSessionInfo)
	for _, s := range sessions {
		existing, ok := byID[s.SessionID]
		if !ok || s.LastModified > existing.LastModified {
			byID[s.SessionID] = s
		}
	}
	result := make([]SDKSessionInfo, 0, len(byID))
	for _, s := range byID {
		result = append(result, s)
	}
	return result
}

// applySortLimitOffset sorts sessions by last_modified descending and applies offset + limit.
func applySortLimitOffset(sessions []SDKSessionInfo, limit *int, offset int) []SDKSessionInfo {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastModified > sessions[j].LastModified
	})
	if offset > 0 && offset < len(sessions) {
		sessions = sessions[offset:]
	} else if offset >= len(sessions) {
		return []SDKSessionInfo{}
	}
	if limit != nil && *limit > 0 && *limit < len(sessions) {
		return sessions[:*limit]
	}
	return sessions
}

// listSessionsForProject lists sessions for a specific project directory (and its worktrees).
func listSessionsForProject(directory string, limit *int, offset int, includeWorktrees bool) ([]SDKSessionInfo, error) {
	canonicalDir, err := canonicalizePath(directory)
	if err != nil {
		return nil, err
	}

	var worktreePaths []string
	if includeWorktrees {
		worktreePaths, _ = getWorktreePaths(canonicalDir)
	}

	// No worktrees - just scan the single project dir
	if len(worktreePaths) <= 1 {
		projectDir, err := findProjectDir(canonicalDir)
		if err != nil {
			return []SDKSessionInfo{}, nil
		}
		sessions := readSessionsFromDir(projectDir, canonicalDir)
		return applySortLimitOffset(sessions, limit, offset), nil
	}

	// Worktree-aware scanning
	projectsDir, err := getProjectsDir()
	if err != nil {
		return nil, err
	}

	// Sort worktree paths by sanitized prefix length (longest first)
	type indexedPath struct {
		path   string
		prefix string
	}
	indexed := make([]indexedPath, len(worktreePaths))
	for i, wt := range worktreePaths {
		sanitized := sanitizePath(wt)
		prefix := strings.ToLower(sanitized)
		if runtime.GOOS != "windows" {
			prefix = sanitized
		}
		indexed[i] = indexedPath{path: wt, prefix: prefix}
	}
	sort.Slice(indexed, func(i, j int) bool {
		return len(indexed[i].prefix) > len(indexed[j].prefix)
	})

	// Read all project directories
	allSessions := []SDKSessionInfo{}
	seenDirs := make(map[string]bool)
	caseInsensitive := runtime.GOOS == "windows"

	// Always include the user's actual directory
	canonicalProjectDir, err := findProjectDir(canonicalDir)
	if err == nil {
		dirBase := filepath.Base(canonicalProjectDir)
		seenKey := dirBase
		if caseInsensitive {
			seenKey = strings.ToLower(dirBase)
		}
		seenDirs[seenKey] = true
		sessions := readSessionsFromDir(canonicalProjectDir, canonicalDir)
		allSessions = append(allSessions, sessions...)
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		// Fall back to single project dir
		return applySortLimitOffset(allSessions, limit, offset), nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()
		seenKey := dirName
		if caseInsensitive {
			seenKey = strings.ToLower(dirName)
		}
		if seenDirs[seenKey] {
			continue
		}

		for _, ip := range indexed {
			// Only use startswith for truncated paths
			isMatch := seenKey == ip.prefix || (len(ip.prefix) >= maxSanitizedLength && strings.HasPrefix(seenKey, ip.prefix+"-"))
			if isMatch {
				seenDirs[seenKey] = true
				sessions := readSessionsFromDir(filepath.Join(projectsDir, dirName), ip.path)
				allSessions = append(allSessions, sessions...)
				break
			}
		}
	}

	deduped := deduplicateBySessionID(allSessions)
	return applySortLimitOffset(deduped, limit, offset), nil
}

// listAllSessions lists sessions across all project directories.
func listAllSessions(limit *int, offset int) ([]SDKSessionInfo, error) {
	projectsDir, err := getProjectsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

	allSessions := []SDKSessionInfo{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(projectsDir, entry.Name())
		sessions := readSessionsFromDir(projectDir, "")
		allSessions = append(allSessions, sessions...)
	}

	deduped := deduplicateBySessionID(allSessions)
	return applySortLimitOffset(deduped, limit, offset), nil
}

// readSessionFile finds and reads a session JSONL file.
func readSessionFile(sessionID, directory string) (string, error) {
	fileName := sessionID + ".jsonl"

	if directory != "" {
		canonicalDir, err := canonicalizePath(directory)
		if err != nil {
			return "", err
		}

		// Try exact/prefix-matched project directory first
		projectDir, err := findProjectDir(canonicalDir)
		if err == nil {
			content, _ := os.ReadFile(filepath.Join(projectDir, fileName))
			if len(content) > 0 {
				return string(content), nil
			}
		}

		// Try worktree paths
		worktreePaths, _ := getWorktreePaths(canonicalDir)
		for _, wt := range worktreePaths {
			if wt == canonicalDir {
				continue
			}
			wtProjectDir, err := findProjectDir(wt)
			if err == nil {
				content, _ := os.ReadFile(filepath.Join(wtProjectDir, fileName))
				if len(content) > 0 {
					return string(content), nil
				}
			}
		}

		return "", nil
	}

	// No directory provided - search all project directories
	projectsDir, err := getProjectsDir()
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(projectsDir, entry.Name(), fileName))
		if err == nil && len(content) > 0 {
			return string(content), nil
		}
	}

	return "", nil
}

// GetSessionInfoOptions contains options for getting single session metadata.
type GetSessionInfoOptions struct {
	// SessionID is the UUID of the session to look up.
	SessionID string
	// Directory is the project directory path. When omitted, all project directories are searched.
	Directory *string
}

// GetSessionInfo reads metadata for a single session by ID.
// Wraps readSessionLite for one file — no O(n) directory scan.
func GetSessionInfo(opts *GetSessionInfoOptions) (*SDKSessionInfo, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}

	if !validateUUID(opts.SessionID) {
		return nil, fmt.Errorf("invalid session ID: %s", opts.SessionID)
	}

	fileName := opts.SessionID + ".jsonl"

	if opts.Directory != nil {
		canonical, err := canonicalizePath(*opts.Directory)
		if err != nil {
			return nil, nil
		}

		projectDir, err := findProjectDir(canonical)
		if err == nil {
			lite := readSessionLite(filepath.Join(projectDir, fileName))
			if lite != nil {
				return parseSessionInfoFromLite(opts.SessionID, lite, canonical), nil
			}
		}

		// Worktree fallback
		worktreePaths, _ := getWorktreePaths(canonical)
		for _, wt := range worktreePaths {
			if wt == canonical {
				continue
			}
			wtProjectDir, err := findProjectDir(wt)
			if err == nil {
				lite := readSessionLite(filepath.Join(wtProjectDir, fileName))
				if lite != nil {
					return parseSessionInfoFromLite(opts.SessionID, lite, wt), nil
				}
			}
		}

		return nil, nil
	}

	// No directory — search all project directories for the session file.
	projectsDir, err := getProjectsDir()
	if err != nil {
		return nil, nil
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lite := readSessionLite(filepath.Join(projectsDir, entry.Name(), fileName))
		if lite != nil {
			return parseSessionInfoFromLite(opts.SessionID, lite, ""), nil
		}
	}

	return nil, nil
}

// transcriptEntry represents a parsed JSONL transcript entry.
type transcriptEntry struct {
	Type             string                 `json:"type"`
	UUID             string                 `json:"uuid"`
	ParentUUID       *string                `json:"parentUuid,omitempty"`
	SessionID        string                 `json:"sessionId,omitempty"`
	Message          map[string]interface{} `json:"message,omitempty"`
	IsSidechain      bool                   `json:"isSidechain,omitempty"`
	IsMeta           bool                   `json:"isMeta,omitempty"`
	IsCompactSummary bool                   `json:"isCompactSummary,omitempty"`
	TeamName         string                 `json:"teamName,omitempty"`
}

// parseTranscriptEntries parses JSONL content into transcript entries.
func parseTranscriptEntries(content string) []transcriptEntry {
	validTypes := map[string]bool{
		"user":       true,
		"assistant":  true,
		"progress":   true,
		"system":     true,
		"attachment": true,
	}

	entries := []transcriptEntry{}
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry transcriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entry.UUID == "" {
			continue
		}

		if !validTypes[entry.Type] {
			continue
		}

		entries = append(entries, entry)
	}

	return entries
}

// buildConversationChain builds the conversation chain by finding the leaf and walking parentUuid.
func buildConversationChain(entries []transcriptEntry) []transcriptEntry {
	if len(entries) == 0 {
		return nil
	}

	// Index by uuid for O(1) parent lookup
	byUUID := make(map[string]transcriptEntry)
	for _, entry := range entries {
		byUUID[entry.UUID] = entry
	}

	// Build index of entry positions (file order) for tie-breaking
	entryIndex := make(map[string]int)
	for i, entry := range entries {
		entryIndex[entry.UUID] = i
	}

	// Find terminal messages (no children point to them via parentUuid)
	parentUUIDs := make(map[string]bool)
	for _, entry := range entries {
		if entry.ParentUUID != nil {
			parentUUIDs[*entry.ParentUUID] = true
		}
	}

	var terminals []transcriptEntry
	for _, entry := range entries {
		if !parentUUIDs[entry.UUID] {
			terminals = append(terminals, entry)
		}
	}

	// From each terminal, walk back to find the nearest user/assistant leaf
	var leaves []transcriptEntry
	for _, terminal := range terminals {
		seen := make(map[string]bool)
		current := terminal
		for current.UUID != "" {
			if seen[current.UUID] {
				break
			}
			seen[current.UUID] = true

			if current.Type == "user" || current.Type == "assistant" {
				leaves = append(leaves, current)
				break
			}

			if current.ParentUUID != nil {
				if parent, ok := byUUID[*current.ParentUUID]; ok {
					current = parent
				} else {
					break
				}
			} else {
				break
			}
		}
	}

	if len(leaves) == 0 {
		return nil
	}

	// Pick the best leaf (main chain, not sidechain/team/meta, highest file position)
	var mainLeaves []transcriptEntry
	for _, leaf := range leaves {
		if !leaf.IsSidechain && leaf.TeamName == "" && !leaf.IsMeta {
			mainLeaves = append(mainLeaves, leaf)
		}
	}

	var bestLeaf transcriptEntry
	candidates := mainLeaves
	if len(candidates) == 0 {
		candidates = leaves
	}

	bestIdx := -1
	for _, cur := range candidates {
		idx := entryIndex[cur.UUID]
		if idx > bestIdx {
			bestLeaf = cur
			bestIdx = idx
		}
	}

	// Walk from leaf to root via parentUuid
	var chain []transcriptEntry
	seen := make(map[string]bool)
	current := bestLeaf
	for current.UUID != "" {
		if seen[current.UUID] {
			break
		}
		seen[current.UUID] = true
		chain = append(chain, current)

		if current.ParentUUID != nil {
			if parent, ok := byUUID[*current.ParentUUID]; ok {
				current = parent
			} else {
				break
			}
		} else {
			break
		}
	}

	// Reverse to get chronological order
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	return chain
}

// isVisibleMessage returns true if the entry should be included in returned messages.
func isVisibleMessage(entry transcriptEntry) bool {
	if entry.Type != "user" && entry.Type != "assistant" {
		return false
	}
	if entry.IsMeta {
		return false
	}
	if entry.IsSidechain {
		return false
	}
	// Note: isCompactSummary messages are intentionally included
	return entry.TeamName == ""
}

// filterVisibleMessages filters the chain to only visible messages.
func filterVisibleMessages(chain []transcriptEntry) []transcriptEntry {
	result := []transcriptEntry{}
	for _, entry := range chain {
		if isVisibleMessage(entry) {
			result = append(result, entry)
		}
	}
	return result
}

// toSessionMessages converts transcript entries to SessionMessage objects.
func toSessionMessages(entries []transcriptEntry) []SessionMessage {
	result := []SessionMessage{}
	for _, entry := range entries {
		msgType := SessionMessageTypeUser
		if entry.Type == "assistant" {
			msgType = SessionMessageTypeAssistant
		}

		msg := SessionMessage{
			Type:      msgType,
			UUID:      entry.UUID,
			SessionID: entry.SessionID,
			Message:   entry.Message,
		}

		result = append(result, msg)
	}
	return result
}

// resolveSubagentsDir returns the subagents directory for a session.
// The subagent transcripts live at <sessionDir>/<sessionID>/  (e.g.
// ~/.claude/projects/<projectKey>/<sessionID>/agent-<agentID>.jsonl).
// Returns empty string when the directory cannot be resolved.
func resolveSubagentsDir(sessionID string, directory *string) string {
	var dir string
	if directory != nil {
		dir = *directory
	}
	filePath := ""
	if dir != "" {
		canon, err := canonicalizePath(dir)
		if err == nil {
			if pd, err := findProjectDir(canon); err == nil {
				filePath = filepath.Join(pd, sessionID+".jsonl")
				if _, statErr := os.Stat(filePath); statErr != nil {
					filePath = ""
				}
				if filePath != "" {
					return filepath.Join(pd, sessionID)
				}
			}
		}
	} else {
		// Search all project directories.
		projectsDir, err := getProjectsDir()
		if err == nil {
			entries, _ := os.ReadDir(projectsDir)
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				candidate := filepath.Join(projectsDir, e.Name(), sessionID+".jsonl")
				if _, statErr := os.Stat(candidate); statErr == nil {
					return filepath.Join(projectsDir, e.Name(), sessionID)
				}
			}
		}
	}
	return ""
}

// ListSubagentsOptions contains options for listing subagents.
type ListSubagentsOptions struct {
	// SessionID is the UUID of the parent session.
	SessionID string
	// Directory is the project directory path. When omitted, all project directories are searched.
	Directory *string
}

// ListSubagents returns the agent IDs of subagents that ran under a session.
// Subagent transcripts are stored as agent-<agentID>.jsonl files in the
// sibling directory <projectDir>/<sessionID>/.
func ListSubagents(opts *ListSubagentsOptions) ([]string, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}
	if !validateUUID(opts.SessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", opts.SessionID)
	}

	subDir := resolveSubagentsDir(opts.SessionID, opts.Directory)
	if subDir == "" {
		return []string{}, nil
	}

	entries, err := os.ReadDir(subDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	agentPattern := regexp.MustCompile(`^agent-(.+)\.jsonl$`)
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		m := agentPattern.FindStringSubmatch(entry.Name())
		if m != nil {
			ids = append(ids, m[1])
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// GetSubagentMessagesOptions contains options for reading subagent messages.
type GetSubagentMessagesOptions struct {
	// SessionID is the UUID of the parent session.
	SessionID string
	// AgentID is the agent ID returned by ListSubagents.
	AgentID string
	// Directory is the project directory path. When omitted, all project directories are searched.
	Directory *string
	// Maximum number of messages to return.
	Limit *int
	// Number of messages to skip from the start.
	Offset int
}

// GetSubagentMessages reads messages from a subagent transcript.
func GetSubagentMessages(opts *GetSubagentMessagesOptions) ([]SessionMessage, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}
	if !validateUUID(opts.SessionID) {
		return nil, fmt.Errorf("invalid session_id: %s", opts.SessionID)
	}
	if opts.AgentID == "" {
		return nil, fmt.Errorf("agent_id must be non-empty")
	}

	subDir := resolveSubagentsDir(opts.SessionID, opts.Directory)
	if subDir == "" {
		return nil, fmt.Errorf("session %s not found", opts.SessionID)
	}

	filePath := filepath.Join(subDir, "agent-"+opts.AgentID+".jsonl")
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("subagent %s not found in session %s", opts.AgentID, opts.SessionID)
		}
		return nil, err
	}

	content := string(contentBytes)
	if content == "" {
		return []SessionMessage{}, nil
	}

	entries := parseTranscriptEntries(content)
	chain := buildConversationChain(entries)
	visible := filterVisibleMessages(chain)
	messages := toSessionMessages(visible)

	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(messages) {
		return []SessionMessage{}, nil
	}

	if opts.Limit != nil && *opts.Limit > 0 {
		end := offset + *opts.Limit
		if end > len(messages) {
			end = len(messages)
		}
		return messages[offset:end], nil
	}
	return messages[offset:], nil
}

// ---------------------------------------------------------------------------
// Store-backed listing helpers
// ---------------------------------------------------------------------------

// storeListLoadConcurrency is the maximum number of concurrent SessionStore.Load
// calls when deriving SDKSessionInfo from a store listing (one per session).
const storeListLoadConcurrency = 16

// entriesToJSONL serializes a slice of SessionStoreEntry values to a newline-
// delimited JSON string, matching the on-disk JSONL format.
func entriesToJSONL(entries []SessionStoreEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err == nil {
			sb.Write(b)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// mtimeFromJSONLTail derives a synthetic mtime (Unix milliseconds) from the
// last valid JSON object's timestamp field in a JSONL string.  Falls back to
// the current time if none is found.
func mtimeFromJSONLTail(jsonl string) int64 {
	lines := strings.Split(strings.TrimRight(jsonl, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		// Try "timestamp" (ISO string) then "mtime" (number)
		if ts, ok := obj["timestamp"].(string); ok && ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				return t.UnixMilli()
			}
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				return t.UnixMilli()
			}
		}
		if mt, ok := obj["mtime"].(float64); ok {
			return int64(mt)
		}
	}
	return time.Now().UnixMilli()
}

// loadStoreEntriesAsJSONL loads entries from a SessionStore for the given
// session and serializes them to JSONL.  Returns ("", nil) when the session
// has no entries in the store.
func loadStoreEntriesAsJSONL(ctx context.Context, store SessionStore, sessionID, directory string) (string, error) {
	projectKey := ProjectKeyForDirectory(directory)
	key := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	entries, err := store.Load(ctx, key)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	return entriesToJSONL(entries), nil
}

// deriveInfosViaLoad loads each session's entries from the store and derives
// SDKSessionInfo via the same lite-parse used by the filesystem path.
// Loads run concurrently up to storeListLoadConcurrency.
// Entries with errors degrade to a minimal SDKSessionInfo; entries that parse
// as sidechain or produce no summary are dropped.
func deriveInfosViaLoad(
	ctx context.Context,
	store SessionStore,
	listing []SessionStoreListEntry,
	directory, projectPath string,
) []SDKSessionInfo {
	type result struct {
		idx  int
		info *SDKSessionInfo
	}

	sem := make(chan struct{}, storeListLoadConcurrency)
	results := make([]result, len(listing))
	var wg sync.WaitGroup

	for i, entry := range listing {
		i, entry := i, entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			jsonl, err := loadStoreEntriesAsJSONL(ctx, store, entry.SessionID, directory)
			if err != nil {
				// Degrade to minimal info on error
				results[i] = result{idx: i, info: &SDKSessionInfo{
					SessionID:    entry.SessionID,
					Summary:      "",
					LastModified: entry.Mtime,
				}}
				return
			}
			if jsonl == "" {
				results[i] = result{idx: i, info: nil}
				return
			}
			mtime := entry.Mtime
			lite := jsonlToLite(jsonl, mtime)
			info := parseSessionInfoFromLite(entry.SessionID, lite, projectPath)
			if info == nil {
				results[i] = result{idx: i, info: nil}
				return
			}
			info.LastModified = mtime
			results[i] = result{idx: i, info: info}
		}()
	}
	wg.Wait()

	var out []SDKSessionInfo
	for _, r := range results {
		if r.info != nil {
			out = append(out, *r.info)
		}
	}
	return out
}

// jsonlToLite parses a JSONL string (already in memory) into a liteSessionFile,
// using mtime as the file modification time.
func jsonlToLite(jsonl string, mtime int64) *liteSessionFile {
	if jsonl == "" {
		return nil
	}
	headSize := liteReadBufSize
	if len(jsonl) < headSize {
		headSize = len(jsonl)
	}
	tailStart := len(jsonl) - liteReadBufSize
	if tailStart < 0 {
		tailStart = 0
	}
	return &liteSessionFile{
		head:  jsonl[:headSize],
		tail:  jsonl[tailStart:],
		mtime: mtime,
		size:  int64(len(jsonl)),
	}
}

// filterTranscriptEntries returns only entries with a "type" field that is a
// known transcript event type. Used by store-backed message readers to skip
// metadata-only entries before building the conversation chain.
func filterTranscriptEntries(raw []SessionStoreEntry) []transcriptEntry {
	const maxFieldScan = 512
	validTypes := map[string]bool{
		"user": true, "assistant": true, "system": true, "tool_result": true,
	}
	var out []transcriptEntry
	for _, e := range raw {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		s := string(b)
		if len(s) > maxFieldScan {
			s = s[:maxFieldScan]
		}
		var te transcriptEntry
		if err := json.Unmarshal([]byte(string(b)), &te); err != nil {
			continue
		}
		if !validTypes[te.Type] {
			continue
		}
		out = append(out, te)
	}
	return out
}

// entriesToSessionMessages converts filtered transcript entries to SessionMessage slice.
// Applies offset/limit if non-nil.
func entriesToSessionMessages(entries []transcriptEntry, limit *int, offset int) []SessionMessage {
	chain := buildConversationChain(entries)
	visible := filterVisibleMessages(chain)
	messages := toSessionMessages(visible)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(messages) {
		return []SessionMessage{}
	}
	messages = messages[offset:]
	if limit != nil && *limit > 0 && *limit < len(messages) {
		messages = messages[:*limit]
	}
	return messages
}

// ---------------------------------------------------------------------------
// Store-backed listing options
// ---------------------------------------------------------------------------

// ListSessionsFromStoreOptions contains options for ListSessionsFromStore.
type ListSessionsFromStoreOptions struct {
	// Store is the session store to query. Required.
	Store SessionStore
	// Directory is the project directory used to compute the project_key.
	// Defaults to the current working directory.
	Directory string
	// Limit caps the number of results returned.
	Limit *int
	// Offset skips results from the beginning of the sorted set.
	Offset int
}

// GetSessionInfoFromStoreOptions contains options for GetSessionInfoFromStore.
type GetSessionInfoFromStoreOptions struct {
	// Store is the session store to query. Required.
	Store SessionStore
	// SessionID is the UUID of the session to look up.
	SessionID string
	// Directory is the project directory used to compute the project_key.
	Directory string
}

// GetSessionMessagesFromStoreOptions contains options for GetSessionMessagesFromStore.
type GetSessionMessagesFromStoreOptions struct {
	// Store is the session store to query. Required.
	Store SessionStore
	// SessionID is the UUID of the session to read.
	SessionID string
	// Directory is the project directory used to compute the project_key.
	Directory string
	// Limit caps the number of messages returned.
	Limit *int
	// Offset skips messages from the beginning.
	Offset int
}

// ListSubagentsFromStoreOptions contains options for ListSubagentsFromStore.
type ListSubagentsFromStoreOptions struct {
	// Store is the session store to query. Required.
	Store SessionStore
	// SessionID is the UUID of the parent session.
	SessionID string
	// Directory is the project directory used to compute the project_key.
	Directory string
}

// GetSubagentMessagesFromStoreOptions contains options for GetSubagentMessagesFromStore.
type GetSubagentMessagesFromStoreOptions struct {
	// Store is the session store to query. Required.
	Store SessionStore
	// SessionID is the UUID of the parent session.
	SessionID string
	// AgentID is the ID of the subagent.
	AgentID string
	// Directory is the project directory used to compute the project_key.
	Directory string
	// Limit caps the number of messages returned.
	Limit *int
	// Offset skips messages from the beginning.
	Offset int
}

// ---------------------------------------------------------------------------
// Store-backed listing functions
// ---------------------------------------------------------------------------

// ListSessionsFromStore lists sessions from a SessionStore.
//
// This is the async store-backed counterpart to ListSessions. It loads each
// session's entries to derive a real summary via the same lite-parse used by
// the filesystem path, so disk and store paths produce identical results for
// the same transcript content.
//
// If the store implements ListSessionSummaries it is called first (one batch
// call). Sessions with fresh sidecars are returned immediately; sessions
// missing a sidecar or with a stale one are gap-filled via Load.
//
// Returns an error if the store implements neither ListSessionSummaries nor
// ListSessions.
func ListSessionsFromStore(ctx context.Context, opts *ListSessionsFromStoreOptions) ([]SDKSessionInfo, error) {
	if opts == nil || opts.Store == nil {
		return nil, fmt.Errorf("opts.Store is required")
	}
	store := opts.Store

	dir := opts.Directory
	projectPath, err := canonicalizePath(func() string {
		if dir != "" {
			return dir
		}
		return "."
	}())
	if err != nil {
		projectPath = dir
	}
	projectKey := sanitizePath(projectPath)

	// Check if store supports ListSessionSummaries
	summaries, summaryErr := store.ListSessionSummaries(ctx, projectKey)
	hasSummaries := summaryErr == nil || !errors.Is(summaryErr, ErrNotImplemented)

	// Check if store supports ListSessions
	listing, listErr := store.ListSessions(ctx, projectKey)
	hasList := listErr == nil || !errors.Is(listErr, ErrNotImplemented)

	if !hasSummaries && !hasList {
		return nil, fmt.Errorf(
			"session_store implements neither ListSessionSummaries() nor " +
				"ListSessions() — cannot list sessions. Provide a store with at " +
				"least one of those methods.",
		)
	}

	// Fast path: list_session_summaries available
	if hasSummaries && summaryErr == nil {
		knownMtimes := map[string]int64{}
		if hasList && listErr == nil {
			for _, e := range listing {
				knownMtimes[e.SessionID] = e.Mtime
			}
		}

		type slot struct {
			mtime     int64
			sessionID string // set when info is nil (gap-fill needed)
			info      *SDKSessionInfo
		}

		var slots []slot
		freshIDs := map[string]bool{}
		for _, s := range summaries {
			sid := s.SessionID
			if hasList {
				known, ok := knownMtimes[sid]
				if !ok {
					continue // session no longer in list_sessions
				}
				if s.Mtime < known {
					continue // stale sidecar — route through gap-fill
				}
			}
			info := summaryEntryToSDKInfo(s, projectPath)
			if info == nil {
				freshIDs[sid] = true
				continue
			}
			slots = append(slots, slot{mtime: s.Mtime, info: info})
			freshIDs[sid] = true
		}
		// Add gap-fill slots for sessions in list but missing/stale sidecar
		if hasList && listErr == nil {
			for _, e := range listing {
				if !freshIDs[e.SessionID] {
					slots = append(slots, slot{mtime: e.Mtime, sessionID: e.SessionID})
				}
			}
		}

		// Sort by mtime desc, then paginate BEFORE gap-fill
		sort.Slice(slots, func(i, j int) bool { return slots[i].mtime > slots[j].mtime })
		if opts.Offset > 0 && opts.Offset < len(slots) {
			slots = slots[opts.Offset:]
		} else if opts.Offset >= len(slots) {
			return []SDKSessionInfo{}, nil
		}
		if opts.Limit != nil && *opts.Limit > 0 && *opts.Limit < len(slots) {
			slots = slots[:*opts.Limit]
		}

		// Gap-fill
		var toFill []SessionStoreListEntry
		for _, sl := range slots {
			if sl.info == nil {
				toFill = append(toFill, SessionStoreListEntry{SessionID: sl.sessionID, Mtime: sl.mtime})
			}
		}
		if len(toFill) > 0 {
			filled := deriveInfosViaLoad(ctx, store, toFill, dir, projectPath)
			byID := map[string]SDKSessionInfo{}
			for _, f := range filled {
				byID[f.SessionID] = f
			}
			for i, sl := range slots {
				if sl.info == nil {
					if f, ok := byID[sl.sessionID]; ok {
						slots[i].info = &f
					}
				}
			}
		}

		var out []SDKSessionInfo
		for _, sl := range slots {
			if sl.info != nil {
				out = append(out, *sl.info)
			}
		}
		return out, nil
	}

	// Slow path: no summaries, use list_sessions + per-session load
	if !hasList || listErr != nil {
		return nil, fmt.Errorf("session_store ListSessions failed: %w", listErr)
	}
	results := deriveInfosViaLoad(ctx, store, listing, dir, projectPath)
	return applySortLimitOffset(results, opts.Limit, opts.Offset), nil
}

// summaryEntryToSDKInfo converts a SessionSummaryEntry to an SDKSessionInfo.
// Returns nil if the entry lacks enough information to build a valid info
// (no summary, no title, or marked as sidechain).
func summaryEntryToSDKInfo(s SessionSummaryEntry, projectPath string) *SDKSessionInfo {
	if s.SessionID == "" {
		return nil
	}
	if isSidechain, _ := s.Data["is_sidechain"].(bool); isSidechain {
		return nil
	}

	customTitle, _ := s.Data["custom_title"].(string)
	aiTitle, _ := s.Data["ai_title"].(string)
	firstPrompt, _ := s.Data["first_prompt"].(string)
	summaryHint, _ := s.Data["summary_hint"].(string)
	lastPrompt, _ := s.Data["last_prompt"].(string)

	// Title resolution: custom_title > ai_title
	title := customTitle
	if title == "" {
		title = aiTitle
	}

	// Summary resolution: title > last_prompt > summary_hint > first_prompt
	summary := title
	if summary == "" {
		summary = lastPrompt
	}
	if summary == "" {
		summary = summaryHint
	}
	if summary == "" {
		summary = firstPrompt
	}
	if summary == "" {
		return nil // no usable summary
	}

	info := &SDKSessionInfo{
		SessionID:    s.SessionID,
		Summary:      summary,
		LastModified: s.Mtime,
	}
	if title != "" {
		info.CustomTitle = &title
	}
	if firstPrompt != "" {
		info.FirstPrompt = &firstPrompt
	}
	if gitBranch, ok := s.Data["git_branch"].(string); ok && gitBranch != "" {
		info.GitBranch = &gitBranch
	}
	sessionCWD, _ := s.Data["cwd"].(string)
	if sessionCWD == "" {
		sessionCWD = projectPath
	}
	if sessionCWD != "" {
		info.CWD = &sessionCWD
	}
	if tag, ok := s.Data["tag"].(string); ok && tag != "" {
		info.Tag = &tag
	}
	if createdAt, ok := s.Data["created_at"].(string); ok && createdAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			ms := t.UnixMilli()
			info.CreatedAt = &ms
		} else if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			ms := t.UnixMilli()
			info.CreatedAt = &ms
		}
	}
	return info
}

// GetSessionInfoFromStore reads metadata for a single session from a SessionStore.
//
// Returns nil if the session is not found, the session_id is invalid, the
// session is a sidechain session, or it has no extractable summary.
func GetSessionInfoFromStore(ctx context.Context, opts *GetSessionInfoFromStoreOptions) (*SDKSessionInfo, error) {
	if opts == nil || opts.Store == nil {
		return nil, fmt.Errorf("opts.Store is required")
	}
	if !validateUUID(opts.SessionID) {
		return nil, nil
	}
	jsonl, err := loadStoreEntriesAsJSONL(ctx, opts.Store, opts.SessionID, opts.Directory)
	if err != nil {
		return nil, err
	}
	if jsonl == "" {
		return nil, nil
	}
	mtime := mtimeFromJSONLTail(jsonl)
	lite := jsonlToLite(jsonl, mtime)
	projectPath, pErr := canonicalizePath(func() string {
		if opts.Directory != "" {
			return opts.Directory
		}
		return "."
	}())
	if pErr != nil {
		projectPath = opts.Directory
	}
	return parseSessionInfoFromLite(opts.SessionID, lite, projectPath), nil
}

// GetSessionMessagesFromStore reads a session's conversation messages from a SessionStore.
//
// This is the store-backed counterpart to GetSessionMessages. It feeds
// SessionStore.Load() results directly into the chain builder — no JSONL round-trip.
// Returns an empty slice if the session is not found or the session_id is invalid.
func GetSessionMessagesFromStore(ctx context.Context, opts *GetSessionMessagesFromStoreOptions) ([]SessionMessage, error) {
	if opts == nil || opts.Store == nil {
		return nil, fmt.Errorf("opts.Store is required")
	}
	if !validateUUID(opts.SessionID) {
		return []SessionMessage{}, nil
	}
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(opts.Directory), SessionID: opts.SessionID}
	entries, err := opts.Store.Load(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotImplemented) {
			return nil, fmt.Errorf("session_store does not implement Load()")
		}
		return nil, err
	}
	if len(entries) == 0 {
		return []SessionMessage{}, nil
	}
	return entriesToSessionMessages(filterTranscriptEntries(entries), opts.Limit, opts.Offset), nil
}

// ListSubagentsFromStore lists subagent IDs for a session from a SessionStore.
//
// This is the store-backed counterpart to ListSubagents. The store must
// implement ListSubkeys; if it does not, an error is returned.
// Returns an empty slice if the session_id is invalid or no subagents exist.
func ListSubagentsFromStore(ctx context.Context, opts *ListSubagentsFromStoreOptions) ([]string, error) {
	if opts == nil || opts.Store == nil {
		return nil, fmt.Errorf("opts.Store is required")
	}
	if !validateUUID(opts.SessionID) {
		return []string{}, nil
	}
	key := SessionListSubkeysKey{ProjectKey: ProjectKeyForDirectory(opts.Directory), SessionID: opts.SessionID}
	subkeys, err := opts.Store.ListSubkeys(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotImplemented) {
			return nil, fmt.Errorf(
				"session_store does not implement ListSubkeys() — cannot list " +
					"subagents. Provide a store with a ListSubkeys() method.",
			)
		}
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, subpath := range subkeys {
		if !strings.HasPrefix(subpath, "subagents/") {
			continue
		}
		last := subpath
		if idx := strings.LastIndex(subpath, "/"); idx >= 0 {
			last = subpath[idx+1:]
		}
		if strings.HasPrefix(last, "agent-") {
			agentID := last[len("agent-"):]
			if !seen[agentID] {
				seen[agentID] = true
				ids = append(ids, agentID)
			}
		}
	}
	return ids, nil
}

// GetSubagentMessagesFromStore reads a subagent's conversation messages from a SessionStore.
//
// This is the store-backed counterpart to GetSubagentMessages. Subagents may
// live at subagents/agent-<id> or nested under subagents/workflows/<runId>/agent-<id>.
// Scans subkeys when the store implements ListSubkeys; otherwise tries the direct path.
// Returns an empty slice if not found or the session_id is invalid.
func GetSubagentMessagesFromStore(ctx context.Context, opts *GetSubagentMessagesFromStoreOptions) ([]SessionMessage, error) {
	if opts == nil || opts.Store == nil {
		return nil, fmt.Errorf("opts.Store is required")
	}
	if !validateUUID(opts.SessionID) {
		return []SessionMessage{}, nil
	}
	if opts.AgentID == "" {
		return nil, fmt.Errorf("AgentID must be non-empty")
	}

	projectKey := ProjectKeyForDirectory(opts.Directory)

	// Try to find the exact subpath by scanning subkeys
	var subpath string
	subkeyKey := SessionListSubkeysKey{ProjectKey: projectKey, SessionID: opts.SessionID}
	subkeys, err := opts.Store.ListSubkeys(ctx, subkeyKey)
	if err == nil {
		suffix := "agent-" + opts.AgentID
		for _, sk := range subkeys {
			if strings.HasSuffix(sk, "/"+suffix) || sk == "subagents/"+suffix {
				subpath = sk
				break
			}
		}
	}
	if subpath == "" {
		// Fall back to canonical path
		subpath = "subagents/agent-" + opts.AgentID
	}

	key := SessionKey{ProjectKey: projectKey, SessionID: opts.SessionID, Subpath: subpath}
	entries, err := opts.Store.Load(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotImplemented) {
			return nil, fmt.Errorf("session_store does not implement Load()")
		}
		return nil, err
	}
	if len(entries) == 0 {
		return []SessionMessage{}, nil
	}
	return entriesToSessionMessages(filterTranscriptEntries(entries), opts.Limit, opts.Offset), nil
}
