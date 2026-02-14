package transport

import (
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBuffer_MultipleNewlines(t *testing.T) {
	// Re-implement setup here since we can't easily reuse the one in the other test file
	// if it is not exported, although it IS in the same package so it SHOULD be visible.
	// Let's assume it is visible.

	// Wait, setupTestTransport takes t *testing.T.
	// Let's copy it just in case or try to use it.
	// Since I'm creating a new file in package transport, it should be visible.

	transport, w, cleanup := setupTestTransport(t)
	defer cleanup()

	obj1 := map[string]interface{}{"id": "1", "type": "msg1"}
	obj2 := map[string]interface{}{"id": "2", "type": "res1"}

	bytes1, _ := json.Marshal(obj1)
	bytes2, _ := json.Marshal(obj2)

	// Write with multiple newlines
	w.Write(bytes1)
	w.Write([]byte("\n\n\n"))
	w.Write(bytes2)
	w.Write([]byte("\n"))

	// Check messages
	select {
	case msg := <-transport.messages:
		if msg["id"] != "1" {
			t.Errorf("Expected id=1, got %v", msg["id"])
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for msg 1")
	}

	select {
	case msg := <-transport.messages:
		if msg["id"] != "2" {
			t.Errorf("Expected id=2, got %v", msg["id"])
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for msg 2")
	}
}

func TestBuffer_LargeMinifiedJSON(t *testing.T) {
	transport, w, cleanup := setupTestTransport(t)
	defer cleanup()

	// Create large data
	largeData := make([]map[string]interface{}, 1000)
	for i := 0; i < 1000; i++ {
		largeData[i] = map[string]interface{}{
			"id":    i,
			"value": strings.Repeat("x", 100),
		}
	}

	obj := map[string]interface{}{
		"type":    "tool_result",
		"content": largeData,
	}

	bytes, _ := json.Marshal(obj)

	// Split into chunks
	chunkSize := 64 * 1024
	for i := 0; i < len(bytes); i += chunkSize {
		end := i + chunkSize
		if end > len(bytes) {
			end = len(bytes)
		}
		w.Write(bytes[i:end])
		// Small delay to simulate network/pipe chunks
		time.Sleep(10 * time.Millisecond)
	}
	w.Write([]byte("\n"))

	select {
	case msg := <-transport.messages:
		if msg["type"] != "tool_result" {
			t.Errorf("Expected type=tool_result, got %v", msg["type"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for large message")
	}
}

func TestBuffer_MixedCompleteAndSplit(t *testing.T) {
	transport, w, cleanup := setupTestTransport(t)
	defer cleanup()

	msg1 := map[string]interface{}{"type": "system", "subtype": "start"}
	msg3 := map[string]interface{}{"type": "system", "subtype": "end"}

	largeMsg := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"content": strings.Repeat("y", 5000),
		},
	}

	bytes1, _ := json.Marshal(msg1)
	bytesLarge, _ := json.Marshal(largeMsg)
	bytes3, _ := json.Marshal(msg3)

	// Write msg1 complete
	w.Write(bytes1)
	w.Write([]byte("\n"))

	// Write large msg split
	w.Write(bytesLarge[:1000])
	time.Sleep(50 * time.Millisecond)
	w.Write(bytesLarge[1000:3000])
	time.Sleep(50 * time.Millisecond)
	w.Write(bytesLarge[3000:])
	w.Write([]byte("\n"))

	// Write msg3 complete
	w.Write(bytes3)
	w.Write([]byte("\n"))

	// Verify
	select {
	case msg := <-transport.messages:
		if msg["type"] != "system" || msg["subtype"] != "start" {
			t.Errorf("Expected start, got %v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout msg1")
	}

	select {
	case msg := <-transport.messages:
		if msg["type"] != "assistant" {
			t.Errorf("Expected assistant, got %v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout largeMsg")
	}

	select {
	case msg := <-transport.messages:
		if msg["type"] != "system" || msg["subtype"] != "end" {
			t.Errorf("Expected end, got %v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout msg3")
	}
}

func TestBuffer_SizeOption(t *testing.T) {
	// Need to manually create transport to set MaxBufferSize option via TransportOptions
	// setupTestTransport sets it to 1MB manually.

	customLimit := 512
	opts := &TransportOptions{
		MaxBufferSize: customLimit,
	}

	cmd := exec.Command("sleep", "10")
	cmd.Start()
	r, w := io.Pipe()

	transport := &SubprocessTransport{
		process: cmd,
		stdout:  r,
		stderr:  io.NopCloser(strings.NewReader("")),
		// Important: NewSubprocessTransport copies MaxBufferSize from opts.
		// Here we construct manually, so we simulate what NewSubprocessTransport does.
		maxBufferSize: customLimit,
		messages:      make(chan map[string]interface{}, 100),
		errors:        make(chan error, 10),
		options:       opts,
	}

	go transport.readStdout()

	defer func() {
		w.Close()
		cmd.Process.Kill()
	}()

	// Write data exceeding limit
	go func() {
		longData := `{"data": "` + strings.Repeat("x", customLimit+10) + `"}`
		w.Write([]byte(longData))
		w.Write([]byte("\n"))
	}()

	select {
	case err := <-transport.errors:
		if overflow, ok := err.(*BufferOverflowError); ok {
			if overflow.Limit != customLimit {
				t.Errorf("Expected limit %d, got %d", customLimit, overflow.Limit)
			}
		} else {
			t.Errorf("Expected BufferOverflowError, got %T", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for overflow error")
	}
}
