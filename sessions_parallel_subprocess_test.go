package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the complete wire-to-store-to-reader path without API credentials.
const parallelToolCLIScript = `#!/bin/sh
case "${1-}" in
-v|--version)
    echo "2.1.316 (Claude Code)"
    exit 0
    ;;
esac
while IFS= read -r line; do
    case "$line" in
    *'"type":"control_request"'*)
        request_id=$(printf '%s\n' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
        printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{}}}\n' "$request_id"
        ;;
    *'"type":"user"'*)
        cp "$PARALLEL_TRANSCRIPT" "$PARALLEL_DESTINATION" || exit 1
        cat "$PARALLEL_FRAMES" || exit 1
        ;;
    esac
done
`

func TestParallelToolSubprocess(t *testing.T) {
	requireSubprocessCLITest(t)
	_, directory, projectDir := setupSessionTestProject(t)
	sessionID := generateUUID()
	destination := filepath.Join(projectDir, sessionID+".jsonl")
	raw := parallelTranscript([]int{1, 2, 0}, true)
	for _, entry := range raw {
		entry["sessionId"] = sessionID
		entry["session_id"] = sessionID
		if entry["type"] == "assistant" {
			entry["message"].(map[string]any)["model"] = "test-model"
		}
	}
	temp := t.TempDir()
	transcript := filepath.Join(temp, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(transcriptJSONL(t, raw)), 0600); err != nil {
		t.Fatal(err)
	}
	var frames strings.Builder
	mirror, err := json.Marshal(map[string]any{
		"type": "transcript_mirror", "file_path": destination, "entries": raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	frames.Write(mirror)
	frames.WriteByte('\n')
	frames.WriteString(transcriptJSONL(t, raw[1:]))
	result, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": false,
		"duration_ms": 1, "duration_api_ms": 1, "num_turns": 1,
		"session_id": sessionID, "result": "done",
	})
	if err != nil {
		t.Fatal(err)
	}
	frames.Write(result)
	frames.WriteByte('\n')
	framePath := filepath.Join(temp, "frames.jsonl")
	if err := os.WriteFile(framePath, []byte(frames.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cliPath := filepath.Join(temp, "parallel-claude.sh")
	if err := os.WriteFile(cliPath, []byte(parallelToolCLIScript), 0700); err != nil {
		t.Fatal(err)
	}
	store := NewInMemorySessionStore()
	client := NewClient(&ClaudeAgentOptions{
		CLIPath: cliPath, CWD: directory, SessionStore: store,
		Env: map[string]string{
			"PARALLEL_TRANSCRIPT": transcript, "PARALLEL_DESTINATION": destination, "PARALLEL_FRAMES": framePath,
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	messages, err := client.Query(ctx, "read them")
	if err != nil {
		t.Fatal(err)
	}
	var gotResult bool
	liveCalls, liveResults := make(map[string]bool), make(map[string]bool)
	for message := range messages {
		switch msg := message.(type) {
		case *AssistantMessage:
			for _, block := range msg.Content {
				if call, ok := block.(*ToolUseBlock); ok {
					liveCalls[call.ID] = true
				}
			}
		case *UserMessage:
			blocks, ok := msg.Content.([]ContentBlock)
			if !ok {
				t.Fatalf("unexpected user content type: %T", msg.Content)
			}
			for _, block := range blocks {
				if result, ok := block.(*ToolResultBlock); ok {
					liveResults[result.ToolUseID] = true
				}
			}
		case *ResultMessage:
			if msg.IsError || msg.SessionID != sessionID {
				t.Fatalf("run failed: %+v", msg)
			}
			gotResult = true
		}
		if gotResult {
			break
		}
	}
	if !gotResult || len(liveCalls) != 3 || len(liveResults) != 3 {
		t.Fatalf("incomplete live turn: result=%t calls=%v results=%v", gotResult, liveCalls, liveResults)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	local, err := GetSessionMessages(&GetSessionMessagesOptions{SessionID: sessionID, Directory: &directory})
	if err != nil {
		t.Fatal(err)
	}
	// Mirror keys come from the project folder in file_path, not the CWD.
	mirrored, err := GetSessionMessagesFromStore(ctx, &GetSessionMessagesFromStoreOptions{
		Store: store, SessionID: sessionID, Directory: filepath.Base(projectDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range [][]SessionMessage{local, mirrored} {
		assertSessionUUIDs(t, read, []string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer"})
		calls, results := make(map[string]int), make(map[string]int)
		for _, message := range read {
			payload, ok := message.Message.(map[string]any)
			if !ok {
				t.Fatalf("unexpected session payload type: %T", message.Message)
			}
			entry := transcriptEntry{Message: payload}
			for _, id := range contentBlockIDs(entry, "tool_use", "id") {
				calls[id]++
			}
			for _, id := range contentBlockIDs(entry, "tool_result", "tool_use_id") {
				results[id]++
			}
		}
		for id := range liveCalls {
			if calls[id] != 1 || results[id] != 1 {
				t.Fatalf("read-back lost or duplicated %q: calls=%v results=%v", id, calls, results)
			}
		}
	}
}
