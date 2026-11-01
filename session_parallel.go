package claude

import (
	"cmp"
	"maps"
	"reflect"
	"slices"
)

func assistantResponseID(entry transcriptEntry) string {
	if entry.Type != "assistant" {
		return ""
	}
	id, _ := entry.Message["id"].(string)
	return id
}

func transcriptContentBlocks(entry transcriptEntry) []any {
	content, _ := entry.Message["content"].([]any)
	return content
}

func contentBlockIDs(entry transcriptEntry, blockType, idField string) []string {
	var ids []string
	for _, raw := range transcriptContentBlocks(entry) {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != blockType {
			continue
		}
		if id, ok := block[idField].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func isToolResultEntry(entry transcriptEntry) bool {
	if entry.Type != "user" || entry.ParentUUID == nil || *entry.ParentUUID == "" {
		return false
	}
	for _, raw := range transcriptContentBlocks(entry) {
		if block, ok := raw.(map[string]any); ok && block["type"] == "tool_result" {
			return true
		}
	}
	return false
}

func sameTranscriptWriter(a, b transcriptEntry) bool {
	return a.IsSidechain == b.IsSidechain && reflect.DeepEqual(a.AgentID, b.AgentID)
}

func transcriptTimestamp(entry transcriptEntry) string {
	timestamp, _ := entry.Timestamp.(string)
	return timestamp
}

// The parent walk keeps only one branch of a parallel turn. Recover entries
// sharing an on-chain API response ID at that response's last on-chain entry,
// putting all sibling calls before their results without reviving retried turns.
func recoverParallelToolEntries(entries []transcriptEntry, byUUID map[string]transcriptEntry, chain []transcriptEntry, seen map[string]bool) []transcriptEntry {
	anchors := make(map[string]string)
	var responseOrder []string
	for _, entry := range chain {
		if id := assistantResponseID(entry); id != "" {
			if _, ok := anchors[id]; !ok {
				responseOrder = append(responseOrder, id)
			}
			anchors[id] = entry.UUID
		}
	}
	if len(anchors) == 0 {
		return chain
	}

	// Python dicts keep the first insertion position and the last UUID value.
	// Preserve that ordering explicitly instead of ranging over a Go map.
	positions := make(map[string]int)
	var ordered []transcriptEntry
	for _, entry := range entries {
		if _, ok := positions[entry.UUID]; !ok {
			positions[entry.UUID] = len(ordered)
			ordered = append(ordered, byUUID[entry.UUID])
		}
	}
	siblings := make(map[string][]transcriptEntry)
	callOwner := make(map[string]transcriptEntry)
	var results []transcriptEntry
	for _, entry := range ordered {
		if id := assistantResponseID(entry); id != "" {
			siblings[id] = append(siblings[id], entry)
			for _, callID := range contentBlockIDs(entry, "tool_use", "id") {
				owner, exists := callOwner[callID]
				if !exists || assistantResponseID(owner) == id {
					callOwner[callID] = entry
				} else {
					// A call ID claimed by different responses is ambiguous.
					callOwner[callID] = transcriptEntry{}
				}
			}
		} else if isToolResultEntry(entry) {
			results = append(results, entry)
		}
	}

	resultsByAssistant := make(map[string][]transcriptEntry)
	for _, result := range results {
		parent := *result.ParentUUID
		resultsByAssistant[parent] = append(resultsByAssistant[parent], result)
		if source, ok := result.SourceToolAssistantUUID.(string); ok {
			if entry, exists := byUUID[source]; exists && sameTranscriptWriter(result, entry) {
				resultsByAssistant[source] = append(resultsByAssistant[source], result)
			}
		}
		for _, callID := range contentBlockIDs(result, "tool_result", "tool_use_id") {
			if owner := callOwner[callID]; owner.UUID != "" && sameTranscriptWriter(result, owner) {
				resultsByAssistant[owner.UUID] = append(resultsByAssistant[owner.UUID], result)
			}
		}
	}

	var answeredOnChain map[string]bool
	inserts := make(map[string][]transcriptEntry)
	for _, id := range responseOrder {
		group := siblings[id]
		members := make(map[string]bool, len(group))
		var orphanedSiblings, orphanedResults, strays []transcriptEntry
		for _, member := range group {
			members[member.UUID] = true
			if !seen[member.UUID] {
				orphanedSiblings = append(orphanedSiblings, member)
			}
		}
		collected := make(map[string]bool)
		for _, member := range group {
			for _, result := range resultsByAssistant[member.UUID] {
				if seen[result.UUID] || collected[result.UUID] {
					continue
				}
				collected[result.UUID] = true
				if members[*result.ParentUUID] {
					orphanedResults = append(orphanedResults, result)
				} else {
					strays = append(strays, result)
				}
			}
		}
		if len(strays) > 0 {
			if answeredOnChain == nil {
				answeredOnChain = make(map[string]bool)
				for _, entry := range chain {
					for _, callID := range contentBlockIDs(entry, "tool_result", "tool_use_id") {
						answeredOnChain[callID] = true
					}
				}
			}
			answered := maps.Clone(answeredOnChain)
			for _, result := range orphanedResults {
				for _, callID := range contentBlockIDs(result, "tool_result", "tool_use_id") {
					answered[callID] = true
				}
			}
			slices.SortFunc(strays, func(a, b transcriptEntry) int {
				return cmp.Compare(positions[a.UUID], positions[b.UUID])
			})
			for _, result := range strays {
				callIDs := contentBlockIDs(result, "tool_result", "tool_use_id")
				answersOpenCall := false
				for _, callID := range callIDs {
					owner := callOwner[callID]
					if !answered[callID] && owner.UUID != "" && members[owner.UUID] {
						answersOpenCall = true
						break
					}
				}
				if answersOpenCall {
					for _, callID := range callIDs {
						answered[callID] = true
					}
					orphanedResults = append(orphanedResults, result)
				}
			}
		}
		if len(orphanedSiblings) == 0 && len(orphanedResults) == 0 {
			continue
		}
		byTimestamp := func(a, b transcriptEntry) int {
			return cmp.Compare(transcriptTimestamp(a), transcriptTimestamp(b))
		}
		slices.SortStableFunc(orphanedSiblings, byTimestamp)
		slices.SortStableFunc(orphanedResults, byTimestamp)
		recovered := append(orphanedSiblings, orphanedResults...)
		for _, entry := range recovered {
			seen[entry.UUID] = true
		}
		inserts[anchors[id]] = recovered
	}
	if len(inserts) == 0 {
		return chain
	}
	var recoveredChain []transcriptEntry
	for _, entry := range chain {
		recoveredChain = append(recoveredChain, entry)
		recoveredChain = append(recoveredChain, inserts[entry.UUID]...)
	}
	return recoveredChain
}
