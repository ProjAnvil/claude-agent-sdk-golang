package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ImportSessionToStoreOptions contains options for ImportSessionToStore.
type ImportSessionToStoreOptions struct {
	// ProjectsDir is the root projects directory used to derive the project key
	// from the session file path (e.g. ~/.claude/projects).
	// When empty, getProjectsDir() is used.
	ProjectsDir string
}

// ImportSessionToStore reads a session .jsonl file and appends all of its
// entries to the given SessionStore.
//
// The session key is derived from the file's location relative to ProjectsDir:
//
//	project_key  = <directory-name-under-projects-dir>
//	session_id   = <file-name-without-.jsonl>
//
// Returns the number of entries imported.
func ImportSessionToStore(ctx context.Context, sessionID string, directory *string, store SessionStore, opts *ImportSessionToStoreOptions) (int, error) {
	if !validateUUID(sessionID) {
		return 0, fmt.Errorf("invalid session_id: %s", sessionID)
	}

	var dir string
	if directory != nil {
		dir = *directory
	}

	// Locate the session file.
	filePath, projectDir := findSessionFileWithDir(sessionID, directory)
	if filePath == "" {
		dirMsg := ""
		if dir != "" {
			dirMsg = fmt.Sprintf(" in project directory for %s", dir)
		}
		return 0, fmt.Errorf("session %s not found%s", sessionID, dirMsg)
	}

	// Determine the projects directory so we can derive the project key.
	var projectsDir string
	if opts != nil && opts.ProjectsDir != "" {
		projectsDir = opts.ProjectsDir
	} else {
		var err error
		projectsDir, err = getProjectsDir()
		if err != nil {
			return 0, fmt.Errorf("unable to determine projects directory: %w", err)
		}
	}

	// Derive the session key from the file path.
	sessionKey := FilePathToSessionKey(filePath, projectsDir)
	if sessionKey == nil {
		// Fall back: use the bare projectDir name as project_key.
		sessionKey = &SessionKey{
			ProjectKey: projectDir,
			SessionID:  sessionID,
		}
	}

	// Read and parse the JSONL file.
	f, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open session file: %w", err)
	}
	defer f.Close()

	var entries []SessionStoreEntry
	scanner := bufio.NewScanner(f)
	// Use 4 MB buffer to handle large lines.
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry SessionStoreEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip malformed lines.
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to read session file: %w", err)
	}

	if len(entries) == 0 {
		return 0, nil
	}

	if err := store.Append(ctx, *sessionKey, entries); err != nil {
		return 0, fmt.Errorf("failed to append entries to store: %w", err)
	}

	return len(entries), nil
}
