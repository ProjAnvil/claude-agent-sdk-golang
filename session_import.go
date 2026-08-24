package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxImportBatchEntries = 500
	maxImportBatchBytes   = 1 << 20 // 1 MiB
)

// ImportSessionToStoreOptions contains options for ImportSessionToStore.
type ImportSessionToStoreOptions struct {
	// ProjectsDir is the root projects directory used to derive the project key
	// from the session file path (e.g. ~/.claude/projects).
	// When empty, getProjectsDir() is used.
	ProjectsDir string

	// ExcludeSubagents controls whether subagent JSONL files under
	// <sessionDir>/subagents/ are skipped. Defaults to false (subagents are
	// included). Set to true to skip subagent import.
	ExcludeSubagents bool

	// BatchSize controls how many entries are buffered before flushing to the
	// store via Append. When zero or negative, maxImportBatchEntries (500) is used.
	BatchSize int
}

// defaultImportOptions returns an ImportSessionToStoreOptions with sensible
// defaults applied (ExcludeSubagents = false, BatchSize = maxImportBatchEntries).
func defaultImportOptions() *ImportSessionToStoreOptions {
	return &ImportSessionToStoreOptions{
		BatchSize: maxImportBatchEntries,
	}
}

// mergeImportOptions overlays non-zero fields from opts onto defaults.
// If opts is nil, defaults are returned.
func mergeImportOptions(opts *ImportSessionToStoreOptions) *ImportSessionToStoreOptions {
	d := defaultImportOptions()
	if opts == nil {
		return d
	}
	if opts.ProjectsDir != "" {
		d.ProjectsDir = opts.ProjectsDir
	}
	d.ExcludeSubagents = opts.ExcludeSubagents
	if opts.BatchSize > 0 {
		d.BatchSize = opts.BatchSize
	}
	return d
}

// ImportSessionToStore reads a session .jsonl file and appends all of its
// entries to the given SessionStore using streaming batch processing.
//
// After the main transcript is imported, unless ExcludeSubagents is set the
// function scans <sessionDir>/subagents/** for additional .jsonl files and
// imports each one using a subkey that includes a Subpath.  For every
// subagent .jsonl file, a corresponding .meta.json sidecar (if present) is
// also imported as an agent_metadata entry.
//
// The session key is derived from the file's location relative to ProjectsDir:
//
//	project_key  = <directory-name-under-projects-dir>
//	session_id   = <file-name-without-.jsonl>
//
// Returns the total number of entries imported (main transcript + subagents).
func ImportSessionToStore(ctx context.Context, sessionID string, directory *string, store SessionStore, opts *ImportSessionToStoreOptions) (int, error) {
	if !validateUUID(sessionID) {
		return 0, fmt.Errorf("invalid session_id: %s", sessionID)
	}

	o := mergeImportOptions(opts)

	// Locate the session file.
	filePath, projectDir := findSessionFileWithDir(sessionID, directory)
	if filePath == "" {
		var dirMsg string
		if directory != nil && *directory != "" {
			dirMsg = fmt.Sprintf(" in project directory for %s", *directory)
		}
		return 0, fmt.Errorf("session %s not found%s", sessionID, dirMsg)
	}

	// Determine the projects directory so we can derive the project key.
	var projectsDir string
	if o.ProjectsDir != "" {
		projectsDir = o.ProjectsDir
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

	// Import the main transcript with batch processing.
	total, err := appendJSONLFileInBatches(ctx, filePath, *sessionKey, store, o.BatchSize)
	if err != nil {
		return total, fmt.Errorf("failed to import main session file: %w", err)
	}

	if o.ExcludeSubagents {
		return total, nil
	}

	// Derive the session directory: <projectDir>/<sessionID> (strip .jsonl).
	sessionDir := strings.TrimSuffix(filePath, ".jsonl")
	subagentsDir := filepath.Join(sessionDir, "subagents")

	jsonlFiles, err := collectJSONLFiles(subagentsDir)
	if err != nil {
		// If the subagents directory simply doesn't exist, that is not an error.
		if os.IsNotExist(err) {
			return total, nil
		}
		return total, fmt.Errorf("failed to scan subagents directory: %w", err)
	}

	for _, fp := range jsonlFiles {
		// Build the subpath relative to sessionDir.
		rel, err := filepath.Rel(sessionDir, fp)
		if err != nil {
			return total, fmt.Errorf("failed to compute relative path for %s: %w", fp, err)
		}
		// Normalise to forward slashes and strip the .jsonl suffix.
		rel = filepath.ToSlash(rel)
		rel = strings.TrimSuffix(rel, ".jsonl")

		subKey := SessionKey{
			ProjectKey: sessionKey.ProjectKey,
			SessionID:  sessionID,
			Subpath:    rel,
		}

		n, err := appendJSONLFileInBatches(ctx, fp, subKey, store, o.BatchSize)
		if err != nil {
			return total, fmt.Errorf("failed to import subagent file %s: %w", fp, err)
		}
		total += n

		// Import the .meta.json sidecar so materializeResumeSession can
		// recreate it and resumed subagents keep their agentType/worktreePath.
		// A missing, corrupt, or non-object sidecar is treated as absent (the
		// transcript is still imported); other read errors propagate.
		meta, err := readAgentMetadataSidecar(fp)
		if err != nil {
			return total, fmt.Errorf("failed to read meta file %s: %w", agentMetadataSidecarPath(fp), err)
		}
		if meta == nil {
			continue
		}

		// Synthetic discriminator last so a stray "type" key in the CLI-owned
		// sidecar can never shadow it.
		metaEntry := make(SessionStoreEntry, len(meta)+1)
		for k, v := range meta {
			metaEntry[k] = v
		}
		metaEntry["type"] = "agent_metadata"

		if err := store.Append(ctx, subKey, []SessionStoreEntry{metaEntry}); err != nil {
			return total, fmt.Errorf("failed to append meta entry for %s: %w", fp, err)
		}
		total++
	}

	return total, nil
}

// appendJSONLFileInBatches reads a JSONL file line by line and flushes batches
// to the store whenever either batchSize entries or maxImportBatchBytes of raw
// line text have been accumulated.
func appendJSONLFileInBatches(ctx context.Context, filePath string, key SessionKey, store SessionStore, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = maxImportBatchEntries
	}

	f, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	var (
		batch  []SessionStoreEntry
		nbytes int
		total  int
	)

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

		batch = append(batch, entry)
		nbytes += len(line)

		if len(batch) >= batchSize || nbytes >= maxImportBatchBytes {
			if err := store.Append(ctx, key, batch); err != nil {
				return total, fmt.Errorf("failed to append batch to store: %w", err)
			}
			total += len(batch)
			batch = batch[:0]
			nbytes = 0
		}
	}

	if err := scanner.Err(); err != nil {
		return total, fmt.Errorf("failed to read file: %w", err)
	}

	// Flush remaining entries.
	if len(batch) > 0 {
		if err := store.Append(ctx, key, batch); err != nil {
			return total, fmt.Errorf("failed to append final batch to store: %w", err)
		}
		total += len(batch)
	}

	return total, nil
}

// collectJSONLFiles recursively collects all .jsonl files under baseDir,
// returning them sorted by path for deterministic ordering.
func collectJSONLFiles(baseDir string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort for deterministic order (filepath.WalkDir already walks in
	// lexicographic order, but be explicit).
	return files, nil
}
