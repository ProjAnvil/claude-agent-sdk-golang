package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// coverageWriteFakeCLI writes an executable shell script to a temp dir and
// returns its path, for use as a fake CLI subprocess.
func coverageWriteFakeCLI(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake CLI requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// coverageFailWriter is an io.WriteCloser whose Write always fails.
type coverageFailWriter struct{}

func (coverageFailWriter) Write(p []byte) (int, error) {
	return 0, errors.New("forced write failure")
}

func (coverageFailWriter) Close() error { return nil }

// coverageBufWriter is an io.WriteCloser capturing writes in a buffer.
type coverageBufWriter struct {
	buf bytes.Buffer
}

func (w *coverageBufWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *coverageBufWriter) Close() error                { return nil }

// TestCoverage_ConnectAlreadyConnected verifies a second Connect is a no-op.
func TestCoverage_ConnectAlreadyConnected(t *testing.T) {
	cli := coverageWriteFakeCLI(t, "#!/bin/sh\nexit 0\n")
	tr := newTestTransport(t, &TransportOptions{CLIPath: cli})

	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Second Connect should be a no-op, got: %v", err)
	}
	tr.Close()
}

// TestCoverage_ConnectWithCWD verifies Connect succeeds with a working
// directory configured (the subprocess runs with cmd.Dir set).
func TestCoverage_ConnectWithCWD(t *testing.T) {
	cli := coverageWriteFakeCLI(t, "#!/bin/sh\nexit 0\n")
	tr := newTestTransport(t, &TransportOptions{CLIPath: cli, CWD: t.TempDir()})

	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect with CWD failed: %v", err)
	}
	tr.Close()
}

// TestCoverage_ConnectStartFailures verifies spawn failures map to the right
// error types: a missing binary yields CLINotFoundError, a non-executable
// file yields CLIConnectionError.
func TestCoverage_ConnectStartFailures(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-claude")
	tr := newTestTransport(t, &TransportOptions{CLIPath: missing})
	err := tr.Connect(context.Background())
	var nf *CLINotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("Expected *CLINotFoundError for a missing binary, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("Not-found error should include the CLI path: %v", err)
	}

	notExec := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(notExec, []byte("not a script"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr = newTestTransport(t, &TransportOptions{CLIPath: notExec})
	err = tr.Connect(context.Background())
	var connErr *CLIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Expected *CLIConnectionError for a non-executable binary, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "failed to start") {
		t.Errorf("Error should mention the start failure: %v", err)
	}
}

// TestCoverage_ConnectChannelPromptStreamsInput connects with a channel
// prompt and verifies messages are streamed to the subprocess stdin as JSON
// lines, and that an unmarshalable message is skipped.
func TestCoverage_ConnectChannelPromptStreamsInput(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "stdin.jsonl")
	cli := coverageWriteFakeCLI(t, "#!/bin/sh\ncat > \"$CAPTURE_FILE\"\n")

	ch := make(chan map[string]interface{}, 4)
	tr, err := NewSubprocessTransport(ch, &TransportOptions{
		CLIPath: cli,
		Env:     map[string]string{"CAPTURE_FILE": capture},
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ch <- map[string]interface{}{"type": "user", "content": "hello"}
	// A value json.Marshal cannot encode must be skipped, not kill the loop.
	ch <- map[string]interface{}{"unmarshalable": make(chan int)}

	// Wait until the good message lands in the subprocess's stdin capture.
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(capture)
		if strings.Contains(string(data), "hello") {
			break
		}
		if time.Now().After(deadline) {
			tr.Close()
			t.Fatal("Timed out waiting for the prompt to reach subprocess stdin")
		}
		time.Sleep(20 * time.Millisecond)
	}

	tr.Close()

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("Failed to read stdin capture: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("Expected exactly one JSON line on stdin (bad message skipped), got %d: %q", len(lines), data)
	}
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &msg); err != nil {
		t.Fatalf("Stdin line is not valid JSON: %v", err)
	}
	if msg["content"] != "hello" {
		t.Errorf("Expected content=hello on subprocess stdin, got %v", msg)
	}
}

// TestCoverage_StreamInputStopsOnWriteError verifies streamInput exits its
// loop when writing to stdin fails, without draining the channel.
func TestCoverage_StreamInputStopsOnWriteError(t *testing.T) {
	tr := &SubprocessTransport{ready: true, stdin: coverageFailWriter{}}

	ch := make(chan map[string]interface{}, 2)
	ch <- map[string]interface{}{"type": "user", "content": "one"}
	ch <- map[string]interface{}{"type": "user", "content": "two"}

	done := make(chan struct{})
	go func() {
		tr.streamInput(ch)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamInput did not stop after a stdin write failure")
	}
	if len(ch) != 1 {
		t.Errorf("streamInput should stop at the first write failure, leaving 1 message; got %d", len(ch))
	}
}

// TestCoverage_WriteStates exercises the Write guard branches and the
// success path.
func TestCoverage_WriteStates(t *testing.T) {
	// Not connected.
	tr := &SubprocessTransport{}
	err := tr.Write("x")
	var connErr *CLIConnectionError
	if !errors.As(err, &connErr) || !strings.Contains(err.Error(), "not ready") {
		t.Errorf("Expected not-ready CLIConnectionError, got %v", err)
	}

	// Ready but stdin already closed.
	tr = &SubprocessTransport{ready: true}
	if err := tr.Write("x"); err == nil || !strings.Contains(err.Error(), "stdin is closed") {
		t.Errorf("Expected stdin-closed error, got %v", err)
	}

	// Process already exited: the exit error is wrapped as the cause.
	exitErr := &ProcessError{Message: "command failed", ExitCode: 2}
	tr = &SubprocessTransport{ready: true, stdin: &coverageBufWriter{}, exitError: exitErr}
	err = tr.Write("x")
	if err == nil || !strings.Contains(err.Error(), "process has exited") {
		t.Errorf("Expected process-exited error, got %v", err)
	}
	var procErr *ProcessError
	if !errors.As(err, &procErr) || procErr.ExitCode != 2 {
		t.Errorf("Expected the ProcessError to be reachable via errors.As, got %v", err)
	}

	// stdin write failure.
	tr = &SubprocessTransport{ready: true, stdin: coverageFailWriter{}}
	if err := tr.Write("x"); err == nil || !strings.Contains(err.Error(), "failed to write to stdin") {
		t.Errorf("Expected stdin write failure, got %v", err)
	}

	// Success path: bytes reach stdin verbatim.
	buf := &coverageBufWriter{}
	tr = &SubprocessTransport{ready: true, stdin: buf}
	if err := tr.Write("hello\n"); err != nil {
		t.Errorf("Write should succeed, got %v", err)
	}
	if buf.buf.String() != "hello\n" {
		t.Errorf("Expected %q on stdin, got %q", "hello\n", buf.buf.String())
	}
}

// TestCoverage_CloseKillsSIGTERMIgnoringProcess verifies Close escalates to
// SIGKILL when the subprocess ignores both stdin EOF and SIGTERM.
func TestCoverage_CloseKillsSIGTERMIgnoringProcess(t *testing.T) {
	cli := coverageWriteFakeCLI(t, "#!/bin/sh\ntrap '' TERM\nwhile true; do sleep 1; done\n")
	tr := newTestTransport(t, &TransportOptions{CLIPath: cli})

	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	state := tr.process.ProcessState
	if state == nil {
		t.Fatal("Process should have been reaped by Close")
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Errorf("Process ignoring SIGTERM should end up SIGKILLed, state: %v", state)
	}
}
