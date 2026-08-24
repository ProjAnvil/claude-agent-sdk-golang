package transport

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// coverageErrReadCloser returns its data on the first Read and a forced
// error on the next, to drive bufio.Scanner into a generic (non-overflow)
// read error.
type coverageErrReadCloser struct {
	data []byte
	err  error
	done bool
}

func (r *coverageErrReadCloser) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}

func (r *coverageErrReadCloser) Close() error { return nil }

// TestCoverage_ReadStdoutMidParseBufferOverflow verifies that when a partial
// JSON line is followed by continuation lines, the accumulated buffer is
// rejected with a BufferOverflowError once it exceeds maxBufferSize, and the
// parser recovers for subsequent messages.
func TestCoverage_ReadStdoutMidParseBufferOverflow(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start dummy process: %v", err)
	}

	r, w := io.Pipe()
	tr := &SubprocessTransport{
		process:       cmd,
		stdout:        r,
		stderr:        io.NopCloser(strings.NewReader("")),
		maxBufferSize: 100,
		messages:      make(chan map[string]interface{}, 10),
		errors:        make(chan error, 10),
		options:       &TransportOptions{},
	}
	go tr.readStdout()

	defer func() {
		w.Close()
		cmd.Process.Kill()
	}()

	// An incomplete JSON line: accumulates in the parse buffer without
	// yielding a message.
	if _, err := w.Write([]byte(`{"key":"` + strings.Repeat("a", 80) + "\n")); err != nil {
		t.Fatal(err)
	}
	// A continuation line pushes the accumulated buffer past the limit.
	if _, err := w.Write([]byte(strings.Repeat("b", 50) + "\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-tr.errors:
		boErr, ok := err.(*BufferOverflowError)
		if !ok {
			t.Fatalf("Expected *BufferOverflowError, got %T: %v", err, err)
		}
		if boErr.Limit != 100 {
			t.Errorf("Expected limit 100, got %d", boErr.Limit)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for mid-parse buffer overflow error")
	}

	// After the overflow the buffer is reset and parsing resumes.
	if _, err := w.Write([]byte(`{"after":true}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-tr.messages:
		if msg["after"] != true {
			t.Errorf("Expected after=true, got %v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message after buffer reset")
	}
}

// TestCoverage_ReadStdoutScannerGenericError verifies a non-overflow stdout
// read failure surfaces as a JSONDecodeError wrapping the underlying cause,
// after delivering any already-scanned message.
func TestCoverage_ReadStdoutScannerGenericError(t *testing.T) {
	sentinel := errors.New("forced stdout read failure")

	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start dummy process: %v", err)
	}
	defer cmd.Process.Kill()

	tr := &SubprocessTransport{
		process:       cmd,
		stdout:        &coverageErrReadCloser{data: []byte(`{"ok":true}` + "\n"), err: sentinel},
		stderr:        io.NopCloser(strings.NewReader("")),
		maxBufferSize: 1024,
		messages:      make(chan map[string]interface{}, 10),
		errors:        make(chan error, 10),
		options:       &TransportOptions{},
	}
	go tr.readStdout()

	select {
	case msg := <-tr.messages:
		if msg["ok"] != true {
			t.Errorf("Expected ok=true, got %v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message before the read failure")
	}

	select {
	case err := <-tr.errors:
		decErr, ok := err.(*JSONDecodeError)
		if !ok {
			t.Fatalf("Expected *JSONDecodeError, got %T: %v", err, err)
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("JSONDecodeError should wrap the read failure: %v", decErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for read failure error")
	}
}
