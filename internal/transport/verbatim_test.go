package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
)

// captureSlogWarnings replaces the default slog logger with one writing to a
// buffer at LevelWarn and restores it on cleanup, so warning tests can assert
// what was (or was not) emitted.
func captureSlogWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// unsetEnvForTest removes key from the process env for the duration of the
// test and restores the previous value afterwards.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// --- StampUserMessage -------------------------------------------------------

func TestStampUserMessage(t *testing.T) {
	t.Run("off returns the same map unmarked", func(t *testing.T) {
		msg := map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"role": "user", "content": "hi"},
		}
		got := StampUserMessage(msg, false)
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(msg).Pointer() {
			t.Error("expected the identical map when verbatim is off, got a different map")
		}
		if _, marked := got["client_composed"]; marked {
			t.Errorf("expected no client_composed when verbatim is off, got %v", got["client_composed"])
		}
	})

	t.Run("on returns a copy marked client_composed", func(t *testing.T) {
		msg := map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"role": "user", "content": "hi"},
		}
		got := StampUserMessage(msg, true)
		if reflect.ValueOf(got).Pointer() == reflect.ValueOf(msg).Pointer() {
			t.Fatal("expected a copy when verbatim is on, got the same map")
		}
		if v, ok := got["client_composed"].(bool); !ok || !v {
			t.Errorf("expected client_composed=true, got %v", got["client_composed"])
		}
		if got["type"] != "user" {
			t.Errorf("expected the copy to carry the original fields, got %v", got)
		}
	})

	t.Run("on overwrites a caller-set false", func(t *testing.T) {
		msg := map[string]interface{}{
			"type":            "user",
			"client_composed": false,
			"message":         map[string]interface{}{"role": "user", "content": "hi"},
		}
		got := StampUserMessage(msg, true)
		if v, ok := got["client_composed"].(bool); !ok || !v {
			t.Errorf("expected client_composed=true to overwrite the caller's false, got %v", got["client_composed"])
		}
		// The caller's map is not mutated.
		if v, ok := msg["client_composed"].(bool); !ok || v {
			t.Errorf("expected the caller's map to keep client_composed=false, got %v", msg["client_composed"])
		}
	})

	t.Run("on does not mutate the original map", func(t *testing.T) {
		msg := map[string]interface{}{"type": "user"}
		got := StampUserMessage(msg, true)
		if _, marked := msg["client_composed"]; marked {
			t.Errorf("expected the caller's map to stay unmarked, got client_composed=%v", msg["client_composed"])
		}
		if v, ok := got["client_composed"].(bool); !ok || !v {
			t.Errorf("expected the returned copy to carry client_composed=true, got %v", got["client_composed"])
		}
	})
}

// --- streamInput stamping ---------------------------------------------------

// fakeStdin records everything the transport writes to the CLI's stdin.
type fakeStdin struct{ bytes.Buffer }

func (f *fakeStdin) Close() error { return nil }

// runStreamInput drives SubprocessTransport.streamInput — the code path the
// real CLI transport uses for channel prompts — with a fake stdin, and
// returns the decoded frames it received.
func runStreamInput(t *testing.T, verbatim bool, messages []map[string]interface{}) []map[string]interface{} {
	t.Helper()
	stdin := &fakeStdin{}
	tr := &SubprocessTransport{
		options: &TransportOptions{VerbatimPrompts: verbatim},
		ready:   true,
		stdin:   stdin,
	}
	ch := make(chan map[string]interface{}, len(messages))
	for _, m := range messages {
		ch <- m
	}
	close(ch)
	tr.streamInput(ch)

	var frames []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(stdin.String()), "\n") {
		if line == "" {
			continue
		}
		var frame map[string]interface{}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("streamInput wrote invalid JSON %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func TestStreamInputStampsChannelPrompts(t *testing.T) {
	userMessage := func(text string, clientComposed interface{}) map[string]interface{} {
		msg := map[string]interface{}{
			"type":    "user",
			"message": map[string]interface{}{"role": "user", "content": text},
		}
		if clientComposed != nil {
			msg["client_composed"] = clientComposed
		}
		return msg
	}

	t.Run("off writes messages unchanged", func(t *testing.T) {
		frames := runStreamInput(t, false, []map[string]interface{}{
			userMessage("one", nil),
			userMessage("two", true),
		})
		if len(frames) != 2 {
			t.Fatalf("expected 2 frames, got %d", len(frames))
		}
		if _, marked := frames[0]["client_composed"]; marked {
			t.Errorf("expected frame without client_composed, got %v", frames[0])
		}
		if v, _ := frames[1]["client_composed"].(bool); !v {
			t.Errorf("expected caller's client_composed=true preserved, got %v", frames[1]["client_composed"])
		}
	})

	t.Run("on marks every message and overwrites caller values", func(t *testing.T) {
		first := userMessage("one", nil)
		second := userMessage("two", true)
		third := userMessage("three", false)
		frames := runStreamInput(t, true, []map[string]interface{}{first, second, third})
		if len(frames) != 3 {
			t.Fatalf("expected 3 frames, got %d", len(frames))
		}
		for i, frame := range frames {
			if v, ok := frame["client_composed"].(bool); !ok || !v {
				t.Errorf("frame %d: expected client_composed=true, got %v", i, frame["client_composed"])
			}
		}
		// The caller's maps are not mutated.
		if _, marked := first["client_composed"]; marked {
			t.Errorf("expected caller's first map unmutated, got client_composed=%v", first["client_composed"])
		}
		if v, _ := third["client_composed"].(bool); v {
			t.Errorf("expected caller's third map to keep client_composed=false, got %v", third["client_composed"])
		}
	})
}

// --- CLAUDE_CODE_SDK_READS_SESSION_STATE (Task 4) ---------------------------

// envMap parses a child env slice into a map from key to the list of values
// carried for that exact key spelling (duplicates preserved in order).
func envMap(t *testing.T, env []string) map[string][]string {
	t.Helper()
	m := make(map[string][]string, len(env))
	for _, entry := range env {
		key, value := entry, ""
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key, value = entry[:i], entry[i+1:]
		}
		m[key] = append(m[key], value)
	}
	return m
}

func TestBuildCommand_SDKReadsSessionStateEnv(t *testing.T) {
	// Clear ambient values first (mirrors Python's monkeypatch.delenv) so a
	// developer machine that exports either variable cannot skew the cases.
	unsetEnvForTest(t, sdkReadsSessionStateEnv)
	unsetEnvForTest(t, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS")

	assertEmitAbsent := func(t *testing.T, env []string) {
		t.Helper()
		for _, entry := range env {
			if strings.HasPrefix(strings.ToUpper(entry), "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=") {
				t.Errorf("SDK must never set CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS, found %q", entry)
			}
		}
	}

	t.Run("default appends 1", func(t *testing.T) {
		tr := newTestTransport(t, &TransportOptions{})
		env := tr.buildCommand(context.Background()).Env
		m := envMap(t, env)
		if got := m[sdkReadsSessionStateEnv]; len(got) != 1 || got[0] != "1" {
			t.Errorf("expected exactly CLAUDE_CODE_SDK_READS_SESSION_STATE=1, got %v", got)
		}
		assertEmitAbsent(t, env)
	})

	t.Run("caller opt-out in options.Env is honored", func(t *testing.T) {
		tr := newTestTransport(t, &TransportOptions{
			Env: map[string]string{sdkReadsSessionStateEnv: "0"},
		})
		env := tr.buildCommand(context.Background()).Env
		m := envMap(t, env)
		if got := m[sdkReadsSessionStateEnv]; len(got) != 1 || got[0] != "0" {
			t.Errorf("expected caller's =0 preserved exactly once, got %v", got)
		}
		assertEmitAbsent(t, env)
	})

	t.Run("caller opt-out in options.Env with other case is honored", func(t *testing.T) {
		tr := newTestTransport(t, &TransportOptions{
			Env: map[string]string{"claude_code_sdk_reads_session_state": "0"},
		})
		env := tr.buildCommand(context.Background()).Env
		m := envMap(t, env)
		if got := m[sdkReadsSessionStateEnv]; len(got) != 0 {
			t.Errorf("expected no uppercase =1 entry when the caller set another case, got %v", got)
		}
		if got := m["claude_code_sdk_reads_session_state"]; len(got) != 1 || got[0] != "0" {
			t.Errorf("expected caller's lowercase entry preserved, got %v", got)
		}
		assertEmitAbsent(t, env)
	})

	t.Run("ambient opt-out in process env is honored", func(t *testing.T) {
		t.Setenv(sdkReadsSessionStateEnv, "0")
		tr := newTestTransport(t, &TransportOptions{})
		env := tr.buildCommand(context.Background()).Env
		m := envMap(t, env)
		if got := m[sdkReadsSessionStateEnv]; len(got) != 1 || got[0] != "0" {
			t.Errorf("expected ambient =0 preserved without an added =1, got %v", got)
		}
		assertEmitAbsent(t, env)
	})

	t.Run("ambient opt-out in process env lowercase is honored", func(t *testing.T) {
		t.Setenv("claude_code_sdk_reads_session_state", "0")
		tr := newTestTransport(t, &TransportOptions{})
		env := tr.buildCommand(context.Background()).Env
		m := envMap(t, env)
		if got := m[sdkReadsSessionStateEnv]; len(got) != 0 {
			t.Errorf("expected no =1 appended for a lowercase ambient spelling, got %v", got)
		}
		if got := m["claude_code_sdk_reads_session_state"]; len(got) != 1 || got[0] != "0" {
			t.Errorf("expected lowercase ambient entry preserved, got %v", got)
		}
		assertEmitAbsent(t, env)
	})
}

// TestCompareCLIVersions covers the component-wise comparison used by the
// probe's warnings.
func TestCompareCLIVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.0.0", "2.0.0", 0},
		{"2.1.247", "2.1.248", -1},
		{"2.1.248", "2.1.248", 0},
		{"2.1.294", "2.1.248", 1},
		{"1.9.0", "2.0.0", -1},
		{"3.0.0", "2.1.248", 1},
		{"2.2.0", "2.10.0", -1}, // numeric, not lexicographic
	}
	for _, tt := range tests {
		if got := compareCLIVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareCLIVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
