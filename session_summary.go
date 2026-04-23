package claude

import "time"

// FoldSessionSummary computes an updated SessionSummaryEntry by applying new
// transcript entries on top of an existing (possibly zero-value) summary.
//
// Fields follow two update strategies:
//   - Set-once (first writer wins): created_at, cwd, is_sidechain
//   - Last-wins: custom_title, ai_title, last_prompt, summary_hint, git_branch, mtime
//   - Tag: set on "tag" type entries; cleared when tag value is ""
func FoldSessionSummary(prev *SessionSummaryEntry, key SessionKey, entries []SessionStoreEntry) SessionSummaryEntry {
	out := SessionSummaryEntry{
		SessionID: key.SessionID,
		Data:      make(map[string]interface{}),
	}

	// Copy existing summary state.
	if prev != nil {
		out.Mtime = prev.Mtime
		for k, v := range prev.Data {
			out.Data[k] = v
		}
	}

	for _, entry := range entries {
		// Track last-modified time.
		if ts := extractInt64(entry, "timestamp"); ts > out.Mtime {
			out.Mtime = ts
		}
		if ts := extractTimestampField(entry, "timestamp"); ts > out.Mtime {
			out.Mtime = ts
		}

		entryType, _ := entry["type"].(string)

		// Set-once: cwd
		if _, hasCWD := out.Data["cwd"]; !hasCWD {
			if cwd, _ := entry["cwd"].(string); cwd != "" {
				out.Data["cwd"] = cwd
			}
		}

		// Set-once: is_sidechain
		if _, has := out.Data["is_sidechain"]; !has {
			if v, ok := entry["isSidechain"].(bool); ok && v {
				out.Data["is_sidechain"] = true
			}
		}

		// Set-once: created_at (derived from the entry's timestamp on first entry)
		if _, hasCAt := out.Data["created_at"]; !hasCAt {
			if ts, _ := entry["timestamp"].(string); ts != "" {
				out.Data["created_at"] = ts
			}
		}

		// Last-wins fields
		if v, _ := entry["customTitle"].(string); v != "" {
			out.Data["custom_title"] = v
		}
		if v, _ := entry["aiTitle"].(string); v != "" {
			out.Data["ai_title"] = v
		}
		if v, _ := entry["lastPrompt"].(string); v != "" {
			out.Data["last_prompt"] = v
		}
		if v, _ := entry["summary"].(string); v != "" {
			out.Data["summary_hint"] = v
		}
		if v, _ := entry["gitBranch"].(string); v != "" {
			out.Data["git_branch"] = v
		}

		// first_prompt: set once from the first user-type entry.
		if _, has := out.Data["first_prompt"]; !has {
			if entryType == "user" {
				if prompt := extractFirstPromptText(entry); prompt != "" {
					out.Data["first_prompt"] = prompt
				}
			}
		}

		// tag: set or clear
		if entryType == "tag" {
			tagVal, _ := entry["tag"].(string)
			if tagVal == "" {
				delete(out.Data, "tag")
			} else {
				out.Data["tag"] = tagVal
			}
		}
	}

	return out
}

// extractInt64 tries to extract a numeric field as int64.
func extractInt64(m map[string]interface{}, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

// extractTimestampField parses an RFC 3339 string field and returns Unix millis.
func extractTimestampField(m map[string]interface{}, key string) int64 {
	ts, ok := m[key].(string)
	if !ok || ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}

// extractFirstPromptText attempts to extract a user-facing text prompt from
// a raw transcript entry map.
func extractFirstPromptText(entry SessionStoreEntry) string {
	// Try direct "message" field first.
	if msg, ok := entry["message"].(map[string]interface{}); ok {
		if content, ok := msg["content"].(string); ok && content != "" {
			return content
		}
		// Also try array content.
		if contentArr, ok := msg["content"].([]interface{}); ok {
			for _, item := range contentArr {
				if block, ok := item.(map[string]interface{}); ok {
					if block["type"] == "text" {
						if text, ok := block["text"].(string); ok && text != "" {
							return text
						}
					}
				}
			}
		}
	}
	return ""
}
