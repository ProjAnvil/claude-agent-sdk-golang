package transport

import (
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// setupTestTransport creates a transport with a pipe for stdout.
// Returns the transport, the stdout writer, and a cleanup function.
func setupTestTransport(t *testing.T) (*SubprocessTransport, *io.PipeWriter, func()) {
	// Create a dummy process that sleeps so Wait() doesn't return immediately
	// but doesn't block forever if we kill it.
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start dummy process: %v", err)
	}

	r, w := io.Pipe()

	transport := &SubprocessTransport{
		process:       cmd,
		stdout:        r,
		stderr:        io.NopCloser(strings.NewReader("")), // dummy stderr
		maxBufferSize: 1024 * 1024,
		messages:      make(chan map[string]interface{}, 100),
		errors:        make(chan error, 10),
		options:       &TransportOptions{},
	}

	// Run readStdout in a goroutine
	go transport.readStdout()

	cleanup := func() {
		w.Close()
		if transport.process.Process != nil {
			transport.process.Process.Kill()
		}
		// Drain channels to prevent deadlock if readStdout is trying to write
	loop:
		for {
			select {
			case <-transport.messages:
			case <-transport.errors:
			default:
				break loop
			}
		}
	}

	return transport, w, cleanup
}

func TestBuffer_MultipleObjectsWithNewline(t *testing.T) {
	transport, w, cleanup := setupTestTransport(t)
	defer cleanup()

	obj1 := map[string]interface{}{"id": "1", "type": "message"}
	obj2 := map[string]interface{}{"id": "2", "type": "result"}

	bytes1, _ := json.Marshal(obj1)
	bytes2, _ := json.Marshal(obj2)

	// Write two objects separated by newline
	w.Write(bytes1)
	w.Write([]byte("\n"))
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

func TestBuffer_EmbeddedNewlines_Escaped(t *testing.T) {
	transport, w, cleanup := setupTestTransport(t)
	defer cleanup()

	// JSON with escaped newline (standard JSON)
	// {"content": "Line 1\nLine 2"}
	// In Go string literal: "Line 1\\nLine 2"
	obj := map[string]interface{}{"content": "Line 1\nLine 2"}
	bytes, _ := json.Marshal(obj) // json.Marshal escapes \n to \\n

	w.Write(bytes)
	w.Write([]byte("\n"))

	select {
	case msg := <-transport.messages:
		content := msg["content"].(string)
		if content != "Line 1\nLine 2" {
			t.Errorf("Expected 'Line 1\\nLine 2', got %q", content)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for msg")
	}
}

func TestBuffer_SplitRead(t *testing.T) {

	transport, w, cleanup := setupTestTransport(t)
	defer cleanup()

	obj := map[string]interface{}{"type": "split"}
	bytes, _ := json.Marshal(obj)

	// Write first half
	w.Write(bytes[:5])
	time.Sleep(100 * time.Millisecond)
	// Write second half + newline
	w.Write(bytes[5:])
	w.Write([]byte("\n"))

	select {
	case msg := <-transport.messages:
		if msg["type"] != "split" {
			t.Errorf("Expected type=split, got %v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for split message")
	}
}

func TestBuffer_Overflow(t *testing.T) {
	// Create transport with small buffer
	cmd := exec.Command("sleep", "10")
	cmd.Start()
	r, w := io.Pipe()

	transport := &SubprocessTransport{
		process:       cmd,
		stdout:        r,
		stderr:        io.NopCloser(strings.NewReader("")),
		maxBufferSize: 20, // Very small buffer
		messages:      make(chan map[string]interface{}, 100),
		errors:        make(chan error, 10),
		options:       &TransportOptions{},
	}
	go transport.readStdout()

	defer func() {
		w.Close()
		cmd.Process.Kill()
	}()

	// Write data longer than 20 bytes in a goroutine to avoid blocking
	go func() {
		longData := `{"type": "very_long_message_that_exceeds_buffer"}`
		w.Write([]byte(longData))
		w.Write([]byte("\n"))
	}()

	select {
	case err := <-transport.errors:
		if _, ok := err.(*BufferOverflowError); !ok {
			t.Errorf("Expected BufferOverflowError, got %T: %v", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for overflow error")
	}
}
