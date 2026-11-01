package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func parallelTranscript(order []int, answer bool) []SessionStoreEntry {
	entries := []SessionStoreEntry{{
		"type": "user", "uuid": "prompt", "parentUuid": nil,
		"message": map[string]any{"role": "user", "content": "read them"},
	}}
	for i := range len(order) {
		parent := "prompt"
		if i > 0 {
			parent = fmt.Sprintf("call-%d", i-1)
		}
		entries = append(entries, SessionStoreEntry{
			"type": "assistant", "uuid": fmt.Sprintf("call-%d", i),
			"parentUuid": parent, "timestamp": fmt.Sprintf("2026-10-20T00:00:0%dZ", i),
			"message": map[string]any{
				"id": "msg_parallel", "role": "assistant",
				"content": []any{map[string]any{
					"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i),
					"name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("%d.txt", i)},
				}},
			},
		})
	}
	for position, i := range order {
		entries = append(entries, SessionStoreEntry{
			"type": "user", "uuid": fmt.Sprintf("result-%d", i),
			"parentUuid":              fmt.Sprintf("call-%d", i),
			"sourceToolAssistantUUID": fmt.Sprintf("call-%d", i),
			"timestamp":               fmt.Sprintf("2026-10-20T00:00:1%dZ", position),
			"message": map[string]any{
				"role": "user", "content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": fmt.Sprintf("toolu_%d", i), "content": "ok",
				}},
			},
		})
	}
	if answer {
		entries = append(entries, SessionStoreEntry{
			"type": "assistant", "uuid": "answer",
			"parentUuid": fmt.Sprintf("result-%d", order[len(order)-1]),
			"message":    map[string]any{"id": "msg_answer", "role": "assistant", "content": "done"},
		})
	}
	return entries
}

func transcriptJSONL(t *testing.T, entries []SessionStoreEntry) string {
	t.Helper()
	var content strings.Builder
	for _, entry := range entries {
		b, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		content.Write(b)
		content.WriteByte('\n')
	}
	return content.String()
}

func assertSessionUUIDs(t *testing.T, messages []SessionMessage, want []string) {
	t.Helper()
	var got []string
	seen := make(map[string]bool)
	for _, message := range messages {
		if seen[message.UUID] {
			t.Fatalf("duplicate message %q", message.UUID)
		}
		seen[message.UUID] = true
		got = append(got, message.UUID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("message UUIDs = %v, want %v", got, want)
	}
}

func TestParallelToolConversation(t *testing.T) {
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {0, 2, 1}} {
		for _, answer := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/answer=%t", order, answer), func(t *testing.T) {
				entries := parseTranscriptEntries(transcriptJSONL(t, parallelTranscript(order, answer)))
				messages := toSessionMessages(filterVisibleMessages(buildConversationChain(entries)))
				want := []string{"prompt", "call-0", "call-1", "call-2"}
				for _, i := range order[:2] {
					want = append(want, fmt.Sprintf("result-%d", i))
				}
				want = append(want, fmt.Sprintf("result-%d", order[2]))
				if answer {
					want = append(want, "answer")
				}
				assertSessionUUIDs(t, messages, want)
				for i, message := range messages[1:7] {
					wantType := "assistant"
					if i >= 3 {
						wantType = "user"
					}
					if string(message.Type) != wantType {
						t.Fatalf("message %d type = %q, want %q", i+1, message.Type, wantType)
					}
				}
			})
		}
	}
}

func TestParallelToolRecoveryEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]SessionStoreEntry) []SessionStoreEntry
		want   []string
	}{
		{"missing parent matched by call ID", func(e []SessionStoreEntry) []SessionStoreEntry {
			e[3]["parentUuid"] = "missing"
			delete(e[3], "sourceToolAssistantUUID")
			return e
		}, []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"}},
		{"source hint with missing parent", func(e []SessionStoreEntry) []SessionStoreEntry {
			e[3]["parentUuid"] = "missing"
			return e
		}, []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"}},
		{"duplicate answered result excluded", func(e []SessionStoreEntry) []SessionStoreEntry {
			duplicate := SessionStoreEntry{
				"type": "user", "uuid": "duplicate", "parentUuid": "elsewhere",
				"sourceToolAssistantUUID": "call-1", "message": e[4]["message"],
			}
			return append(e[:5], duplicate, e[5])
		}, []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"}},
		{"other agent cannot supply stray result", func(e []SessionStoreEntry) []SessionStoreEntry {
			e[3]["parentUuid"] = "missing"
			e[3]["agentId"] = "another-agent"
			return e
		}, []string{"prompt", "call-0", "call-1", "result-1", "answer"}},
		{"sidechain cannot supply stray result", func(e []SessionStoreEntry) []SessionStoreEntry {
			e[3]["parentUuid"] = "missing"
			e[3]["isSidechain"] = true
			return e
		}, []string{"prompt", "call-0", "call-1", "result-1", "answer"}},
		{"abandoned response excluded", func(e []SessionStoreEntry) []SessionStoreEntry {
			old := SessionStoreEntry{
				"type": "assistant", "uuid": "old", "parentUuid": "prompt",
				"message": map[string]any{"id": "msg_old", "content": "old response"},
			}
			return append([]SessionStoreEntry{e[0], old}, e[1:]...)
		}, []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"}},
		{"ambiguous call ID cannot supply stray result", func(e []SessionStoreEntry) []SessionStoreEntry {
			e[3]["parentUuid"] = "missing"
			delete(e[3], "sourceToolAssistantUUID")
			old := SessionStoreEntry{
				"type": "assistant", "uuid": "old", "parentUuid": "prompt",
				"message": map[string]any{"id": "msg_old", "content": []any{
					map[string]any{"type": "tool_use", "id": "toolu_0"},
				}},
			}
			return append([]SessionStoreEntry{e[0], old}, e[1:]...)
		}, []string{"prompt", "call-0", "call-1", "result-1", "answer"}},
		{"non-string optional metadata tolerated", func(e []SessionStoreEntry) []SessionStoreEntry {
			e[3]["timestamp"] = 7
			e[3]["sourceToolAssistantUUID"] = map[string]any{"unexpected": true}
			e[1]["agentId"] = map[string]any{"unexpected": true}
			e[3]["agentId"] = map[string]any{"unexpected": true}
			return e
		}, []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"}},
		{"malformed blocks do not abort recovery", func(e []SessionStoreEntry) []SessionStoreEntry {
			message := e[3]["message"].(map[string]any)
			message["content"] = append(message["content"].([]any), nil, "text",
				map[string]any{"type": "tool_result", "tool_use_id": 3})
			return e
		}, []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := tt.mutate(parallelTranscript([]int{0, 1}, true))
			chain := buildConversationChain(parseTranscriptEntries(transcriptJSONL(t, entries)))
			assertSessionUUIDs(t, toSessionMessages(filterVisibleMessages(chain)), tt.want)
		})
	}
}

func TestParallelToolRecoveryOrdering(t *testing.T) {
	for _, tie := range []bool{false, true} {
		t.Run(fmt.Sprintf("timestamp-tie=%t", tie), func(t *testing.T) {
			raw := parallelTranscript([]int{1, 2, 0}, true)
			if tie {
				for _, entry := range raw {
					entry["timestamp"] = nil
				}
			}
			raw[2], raw[3] = raw[3], raw[2]
			want := []string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer"}
			if tie {
				want = []string{"prompt", "call-0", "call-2", "call-1", "result-2", "result-1", "result-0", "answer"}
			}
			for range 30 {
				chain := buildConversationChain(parseTranscriptEntries(transcriptJSONL(t, raw)))
				assertSessionUUIDs(t, toSessionMessages(chain), want)
			}
		})
	}
}

func TestParallelToolSessionReaders(t *testing.T) {
	_, directory, projectDir := setupSessionTestProject(t)
	sessionID := generateUUID()
	raw := parallelTranscript([]int{1, 2, 0}, true)
	for _, entry := range raw {
		entry["sessionId"] = sessionID
	}
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(transcriptJSONL(t, raw)), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}
	if err := store.Append(t.Context(), key, raw); err != nil {
		t.Fatal(err)
	}
	readers := map[string]func(*int, int) ([]SessionMessage, error){
		"filesystem": func(limit *int, offset int) ([]SessionMessage, error) {
			return GetSessionMessages(&GetSessionMessagesOptions{
				SessionID: sessionID, Directory: &directory, Limit: limit, Offset: offset,
			})
		},
		"store": func(limit *int, offset int) ([]SessionMessage, error) {
			return GetSessionMessagesFromStore(t.Context(), &GetSessionMessagesFromStoreOptions{
				Store: store, SessionID: sessionID, Directory: directory, Limit: limit, Offset: offset,
			})
		},
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			messages, err := read(nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			assertSessionUUIDs(t, messages, []string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer"})
			limit := 3
			messages, err = read(&limit, 2)
			if err != nil {
				t.Fatal(err)
			}
			assertSessionUUIDs(t, messages, []string{"call-1", "call-2", "result-1"})
		})
	}
}

func TestParallelToolSubagentReaders(t *testing.T) {
	_, directory, projectDir := setupSessionTestProject(t)
	sessionID := generateUUID()
	agentID := "parallel-agent"
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"),
		[]byte(`{"type":"user","uuid":"parent-prompt","message":{"content":"start a subagent"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(projectDir, sessionID)
	if err := os.MkdirAll(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := parallelTranscript([]int{1, 2, 0}, true)
	if err := os.WriteFile(filepath.Join(subdir, "agent-"+agentID+".jsonl"), []byte(transcriptJSONL(t, raw)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "agent-"+agentID+".meta.json"),
		[]byte(`{"toolUseId":"parent-call","parentAgentId":"parent-agent"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID, Subpath: "subagents/agent-" + agentID}
	if err := store.Append(t.Context(), key, raw); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(t.Context(), key, []SessionStoreEntry{{
		"type": "agent_metadata", "toolUseId": "parent-call", "parentAgentId": "parent-agent",
	}}); err != nil {
		t.Fatal(err)
	}
	local, err := GetSubagentMessages(&GetSubagentMessagesOptions{
		SessionID: sessionID, AgentID: agentID, Directory: &directory,
	})
	if err != nil {
		t.Fatal(err)
	}
	mirrored, err := GetSubagentMessagesFromStore(t.Context(), &GetSubagentMessagesFromStoreOptions{
		Store: store, SessionID: sessionID, AgentID: agentID, Directory: directory,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, messages := range [][]SessionMessage{local, mirrored} {
		assertSessionUUIDs(t, messages, []string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer"})
		for _, message := range messages {
			if message.ParentToolUseID == nil || *message.ParentToolUseID != "parent-call" ||
				message.ParentAgentID == nil || *message.ParentAgentID != "parent-agent" {
				t.Fatalf("recovered message lost parent metadata: %+v", message)
			}
		}
	}
}

func TestParallelToolSequentialChainUnchanged(t *testing.T) {
	raw := parallelTranscript([]int{0, 1}, true)
	raw[2]["parentUuid"] = "result-0"
	raw = []SessionStoreEntry{raw[0], raw[1], raw[3], raw[2], raw[4], raw[5]}
	chain := buildConversationChain(parseTranscriptEntries(transcriptJSONL(t, raw)))
	assertSessionUUIDs(t, toSessionMessages(chain), []string{"prompt", "call-0", "result-0", "call-1", "result-1", "answer"})
}

func TestParallelToolStraysPreferFirstFileResult(t *testing.T) {
	raw := parallelTranscript([]int{0, 1}, true)
	raw[3]["parentUuid"] = "missing"
	duplicate := SessionStoreEntry{
		"type": "user", "uuid": "duplicate", "parentUuid": "missing",
		"sourceToolAssistantUUID": "call-0", "message": raw[3]["message"],
		"timestamp": "2000-01-01T00:00:00Z",
	}
	raw = append(raw[:5], duplicate, raw[5])
	chain := buildConversationChain(parseTranscriptEntries(transcriptJSONL(t, raw)))
	assertSessionUUIDs(t, toSessionMessages(chain), []string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"})
}

func TestParallelToolNoResponseIDUnchanged(t *testing.T) {
	raw := parallelTranscript([]int{1, 0}, true)
	for _, entry := range raw {
		delete(entry["message"].(map[string]any), "id")
	}
	chain := buildConversationChain(parseTranscriptEntries(transcriptJSONL(t, raw)))
	assertSessionUUIDs(t, toSessionMessages(chain), []string{"prompt", "call-0", "result-0", "answer"})
}

func FuzzParallelToolConversation(f *testing.F) {
	f.Add(`{"type":"user","uuid":"root","message":{"content":"hi"}}
{"type":"assistant","uuid":"call","parentUuid":"root","message":{"id":"msg","content":[{"type":"tool_use","id":"tool"}]}}
{"type":"user","uuid":"result","parentUuid":"call","message":{"content":[{"type":"tool_result","tool_use_id":"tool"}]}}`)
	f.Add(`{"type":"assistant","uuid":"a","parentUuid":"a","message":{"id":"m","content":[null,7,{"type":[]}]}}`)
	f.Fuzz(func(t *testing.T, content string) {
		entries := parseTranscriptEntries(content)
		chain := buildConversationChain(entries)
		known := make(map[string]bool, len(entries))
		for _, entry := range entries {
			known[entry.UUID] = true
		}
		seen := make(map[string]bool, len(chain))
		for _, entry := range chain {
			if !known[entry.UUID] || seen[entry.UUID] {
				t.Fatalf("chain contains an unknown or duplicate entry: %q", entry.UUID)
			}
			seen[entry.UUID] = true
		}
	})
}

func BenchmarkParallelToolConversation(b *testing.B) {
	raw := parallelTranscript([]int{2, 1, 0}, true)
	var content strings.Builder
	for _, entry := range raw {
		data, err := json.Marshal(entry)
		if err != nil {
			b.Fatal(err)
		}
		content.Write(data)
		content.WriteByte('\n')
	}
	entries := parseTranscriptEntries(content.String())
	b.ReportAllocs()
	for b.Loop() {
		if chain := buildConversationChain(entries); len(chain) != 8 {
			b.Fatalf("incomplete parallel turn: %d entries", len(chain))
		}
	}
}
