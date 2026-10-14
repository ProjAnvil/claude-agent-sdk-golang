package transport

import (
	"errors"
	"io"
	"os/exec"
	"runtime"
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

// allStacks returns a dump of all goroutine stacks.
func allStacks() string {
	dump := make([]byte, 1<<20)
	n := runtime.Stack(dump, true)
	return string(dump[:n])
}

// stackBlocksByID parses a goroutine dump into {goroutine ID: stack block},
// keyed by ID so leak assertions can diff against a baseline snapshot taken
// before the action under test. A global substring scan would flake if any
// earlier test leaked a same-named goroutine; the diff scopes the assertion
// to goroutines that appeared during this test.
func stackBlocksByID(dump string) map[string]string {
	blocks := map[string]string{}
	for _, block := range strings.Split(dump, "\n\ngoroutine ") {
		block = strings.TrimPrefix(block, "goroutine ")
		id := block
		if i := strings.IndexAny(id, " \n"); i >= 0 {
			id = id[:i]
		}
		if id != "" {
			blocks[id] = block
		}
	}
	return blocks
}

// newGoroutineBlocks returns the stack blocks of goroutines absent from
// baseline — the goroutines that appeared since the snapshot was taken.
func newGoroutineBlocks(baseline map[string]string) map[string]string {
	fresh := map[string]string{}
	for id, block := range stackBlocksByID(allStacks()) {
		if _, old := baseline[id]; !old {
			fresh[id] = block
		}
	}
	return fresh
}

// TestCloseReleasesParkedReadStdoutSender verifies the goroutine-leak escape
// hatch: with no consumer draining messages, readStdout parks on the full
// buffer; Close() must release it so the goroutine (and the channel) can
// finish. Before the fix the blocking send leaked the reader forever.
func TestCloseReleasesParkedReadStdoutSender(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawns a unix sleep subprocess")
	}
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start dummy process: %v", err)
	}

	pr, pw := io.Pipe()
	tr := &SubprocessTransport{
		process:       cmd,
		stdout:        pr,
		stderr:        io.NopCloser(strings.NewReader("")),
		maxBufferSize: 1024,
		// Small buffer so the reader parks quickly.
		messages: make(chan map[string]interface{}, 3),
		errors:   make(chan error, 10),
		closedCh: make(chan struct{}),
		options:  &TransportOptions{},
	}
	baseline := stackBlocksByID(allStacks())
	go tr.readStdout()

	line := []byte(`{"type":"assistant","uuid":"u"}` + "\n")
	go func() {
		// 5 lines into a 3-slot buffer: the 4th send parks the reader.
		for i := 0; i < 5; i++ {
			if _, err := pw.Write(line); err != nil {
				return
			}
		}
	}()

	// Wait until the buffer is full AND the reader is parked inside the
	// send (a snapshot that merely shows readStdout may be mid-loop before
	// the closed check, and Close could then win that race). Only
	// goroutines new since the baseline are considered, so a same-named
	// goroutine leaked by an earlier test cannot satisfy the check.
	deadline := time.Now().Add(2 * time.Second)
	parked := false
	for time.Now().Before(deadline) {
		for _, block := range newGoroutineBlocks(baseline) {
			if !strings.Contains(block, "(*SubprocessTransport).readStdout") {
				continue
			}
			// A plain blocking send parks as "[chan send]"; the fixed
			// select-guarded send parks as "[select]".
			if strings.Contains(block, "[chan send") || strings.Contains(block, "[select") {
				parked = true
				break
			}
		}
		if parked {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !parked {
		t.Fatal("readStdout never parked on the full messages channel")
	}

	// Make Close's process wait return promptly, then close: the parked
	// sender must be released and readStdout must run to completion. Its
	// deferred close(t.errors) is the join signal — messages itself is NOT
	// drained, because a receive there would free buffer space and release
	// the parked sender even in the unfixed code.
	_ = cmd.Process.Kill()
	_ = pw.Close()
	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	exited := false
	for time.Now().Before(deadline) {
		select {
		case _, ok := <-tr.errors:
			if !ok {
				exited = true
			}
		default:
		}
		if exited {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !exited {
		t.Fatal("readStdout goroutine did not exit after Close; it is parked on a full messages channel")
	}
}

// TestCloseReleasesParkedErrorsSender is the errors-channel sibling of
// TestCloseReleasesParkedReadStdoutSender: with nobody draining errors, the
// buffer-overflow path fills the 1-slot errors channel and parks the reader
// on the second overflow. Close() must release the parked sender so the
// goroutine (and the channel) can finish. Before the fix the blocking send
// leaked the reader forever.
func TestCloseReleasesParkedErrorsSender(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawns a unix sleep subprocess")
	}
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start dummy process: %v", err)
	}

	pr, pw := io.Pipe()
	tr := &SubprocessTransport{
		process:       cmd,
		stdout:        pr,
		stderr:        io.NopCloser(strings.NewReader("")),
		maxBufferSize: 128,
		// messages stays empty: overflow lines never parse, so the reader
		// can only be parked on the errors send in this scenario.
		messages: make(chan map[string]interface{}, 3),
		// 1-slot errors buffer: the first overflow fills it, the second
		// parks the reader.
		errors:   make(chan error, 1),
		closedCh: make(chan struct{}),
		options:  &TransportOptions{},
	}
	baseline := stackBlocksByID(allStacks())
	go tr.readStdout()

	// Overflow arrives via accumulation, like the mid-parse test above: each
	// line fits under the scanner's token limit (maxBufferSize), an
	// unterminated JSON line stays in the parse buffer (unparseable, so not
	// reset), and its continuation pushes the buffer past maxBufferSize.
	// Neither line ever parses, so no message is ever sent.
	openLine := []byte(`{"pad":"` + strings.Repeat("a", 100) + "\n")
	continueLine := []byte(strings.Repeat("b", 60) + "\n")
	go func() {
		// 3 open/continue pairs into a 1-slot errors buffer: the 2nd
		// overflow parks the reader; the 3rd pair queues in the pipe.
		for i := 0; i < 3; i++ {
			if _, err := pw.Write(openLine); err != nil {
				return
			}
			if _, err := pw.Write(continueLine); err != nil {
				return
			}
		}
	}()

	// Wait until the reader is parked inside the errors send BEFORE
	// closing. Only goroutines new since the baseline are considered, and
	// since no message is ever parsed here, a parked readStdout must be
	// parked on the errors channel.
	deadline := time.Now().Add(2 * time.Second)
	parked := false
	for time.Now().Before(deadline) {
		for _, block := range newGoroutineBlocks(baseline) {
			if !strings.Contains(block, "(*SubprocessTransport).readStdout") {
				continue
			}
			// A plain blocking send parks as "[chan send]"; the fixed
			// select-guarded send parks as "[select]".
			if strings.Contains(block, "[chan send") || strings.Contains(block, "[select") {
				parked = true
				break
			}
		}
		if parked {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !parked {
		t.Fatal("readStdout never parked on the full errors channel")
	}

	// Make Close's process wait return promptly, then close: the parked
	// sender must be released and readStdout must run to completion. The
	// exit is observed via the goroutine dump — errors itself is NOT
	// drained, because a receive there would free buffer space and release
	// the parked sender even in the unfixed code.
	_ = cmd.Process.Kill()
	_ = pw.Close()
	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	exited := false
	for time.Now().Before(deadline) {
		stillParked := false
		for _, block := range newGoroutineBlocks(baseline) {
			if strings.Contains(block, "(*SubprocessTransport).readStdout") {
				stillParked = true
				break
			}
		}
		if !stillParked {
			exited = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !exited {
		t.Fatal("readStdout goroutine did not exit after Close; it is parked on a full errors channel")
	}
}
