package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
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

	commandNameRE   = regexp.MustCompile(`<command-name>(.*?)</command-name>`)
	sanitizeRE      = regexp.MustCompile(`[^a-zA-Z0-9]`)
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
		return listSessionsForProject(*opts.Directory, opts.Limit, opts.IncludeWorktrees)
	}
	return listAllSessions(opts.Limit)
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

		head, tail, mtime, size := lite.head, lite.tail, lite.mtime, lite.size

		// Check first line for sidechain sessions
		firstNewline := strings.Index(head, "\n")
		firstLine := head
		if firstNewline >= 0 {
			firstLine = head[:firstNewline]
		}
		if strings.Contains(firstLine, `"isSidechain":true`) || strings.Contains(firstLine, `"isSidechain": true`) {
			continue
		}

		// Extract metadata
		customTitle := extractLastJSONStringField(tail, "customTitle")
		firstPrompt := extractFirstPromptFromHead(head)
		summary := customTitle
		if summary == "" {
			summary = extractLastJSONStringField(tail, "summary")
		}
		if summary == "" {
			summary = firstPrompt
		}

		// Skip metadata-only sessions
		if summary == "" {
			continue
		}

		gitBranch := extractLastJSONStringField(tail, "gitBranch")
		if gitBranch == "" {
			gitBranch = extractJSONStringField(head, "gitBranch")
		}

		sessionCWD := extractJSONStringField(head, "cwd")
		if sessionCWD == "" && projectPath != "" {
			sessionCWD = projectPath
		}

		sessionInfo := SDKSessionInfo{
			SessionID:    sessionID,
			Summary:      summary,
			LastModified: mtime,
			FileSize:     size,
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

		results = append(results, sessionInfo)
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

// applySortAndLimit sorts sessions by last_modified descending and applies optional limit.
func applySortAndLimit(sessions []SDKSessionInfo, limit *int) []SDKSessionInfo {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastModified > sessions[j].LastModified
	})
	if limit != nil && *limit > 0 && *limit < len(sessions) {
		return sessions[:*limit]
	}
	return sessions
}

// listSessionsForProject lists sessions for a specific project directory (and its worktrees).
func listSessionsForProject(directory string, limit *int, includeWorktrees bool) ([]SDKSessionInfo, error) {
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
		return applySortAndLimit(sessions, limit), nil
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
		return applySortAndLimit(allSessions, limit), nil
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
	return applySortAndLimit(deduped, limit), nil
}

// listAllSessions lists sessions across all project directories.
func listAllSessions(limit *int) ([]SDKSessionInfo, error) {
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
	return applySortAndLimit(deduped, limit), nil
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

// transcriptEntry represents a parsed JSONL transcript entry.
type transcriptEntry struct {
	Type         string                 `json:"type"`
	UUID         string                 `json:"uuid"`
	ParentUUID   *string                `json:"parentUuid,omitempty"`
	SessionID    string                 `json:"sessionId,omitempty"`
	Message      map[string]interface{} `json:"message,omitempty"`
	IsSidechain  bool                   `json:"isSidechain,omitempty"`
	IsMeta       bool                   `json:"isMeta,omitempty"`
	IsCompactSummary bool               `json:"isCompactSummary,omitempty"`
	TeamName     string                 `json:"teamName,omitempty"`
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
