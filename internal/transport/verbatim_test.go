package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

// fakeCLIVersionOutput stubs runCLIVersionCommand to return out and restores
// the real probe on cleanup, so version tests never need a real CLI.
func fakeCLIVersionOutput(t *testing.T, out string, err error) {
	t.Helper()
	orig := runCLIVersionCommand
	runCLIVersionCommand = func(path string) (string, error) {
		return out, err
	}
	t.Cleanup(func() { runCLIVersionCommand = orig })
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

// --- CLI version probe (Task 3b) --------------------------------------------

func TestCheckCLIVersionWarnings(t *testing.T) {
	tests := []struct {
		name            string
		verbatimPrompts bool
		versionOutput   string
		probeErr        error
		wantWarnings    int
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "predates client_composed with option on warns verbatim",
			verbatimPrompts: true,
			versionOutput:   "2.1.247 (Claude Code)\n",
			wantWarnings:    1,
			wantContains:    []string{"verbatim_prompts", "2.1.248"},
			wantNotContains: []string{"unsupported"},
		},
		{
			name:            "above minimum but below verbatim minimum with option on",
			verbatimPrompts: true,
			versionOutput:   "2.0.5",
			wantWarnings:    1,
			wantContains:    []string{"verbatim_prompts", "2.1.248"},
			wantNotContains: []string{"unsupported"},
		},
		{
			name:            "exactly the verbatim minimum does not warn",
			verbatimPrompts: true,
			versionOutput:   "2.1.248",
			wantWarnings:    0,
		},
		{
			name:            "above the verbatim minimum does not warn",
			verbatimPrompts: true,
			versionOutput:   "2.1.294 (Claude Code)",
			wantWarnings:    0,
		},
		{
			name:            "option off does not warn",
			verbatimPrompts: false,
			versionOutput:   "2.1.100 (Claude Code)",
			wantWarnings:    0,
		},
		{
			name:            "below the supported minimum warns unsupported",
			verbatimPrompts: false,
			versionOutput:   "1.9.0",
			wantWarnings:    1,
			wantContains:    []string{"unsupported", "2.0.0"},
			wantNotContains: []string{"verbatim_prompts"},
		},
		{
			name:            "below both minima with option on warns twice",
			verbatimPrompts: true,
			versionOutput:   "1.9.0",
			wantWarnings:    2,
			wantContains:    []string{"unsupported", "verbatim_prompts"},
		},
		{
			name:            "probe error is silent",
			verbatimPrompts: true,
			probeErr:        errors.New("cli not runnable"),
			wantWarnings:    0,
		},
		{
			name:            "garbage output is silent",
			verbatimPrompts: true,
			versionOutput:   "not a version at all",
			wantWarnings:    0,
		},
		{
			// Python anchors re.match on the stripped output: a version
			// anywhere but the start is not the CLI's version, so the probe
			// treats the output as unknown — no warnings, no crash.
			name:            "version not at the start is unknown and silent",
			verbatimPrompts: true,
			versionOutput:   "build 2026.10.13\n2.1.100",
			wantWarnings:    0,
		},
		{
			name:            "leading whitespace is trimmed before the anchored match",
			verbatimPrompts: true,
			versionOutput:   "  \n2.1.247 (Claude Code)",
			wantWarnings:    1,
			wantContains:    []string{"verbatim_prompts"},
			wantNotContains: []string{"unsupported"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: tt.verbatimPrompts})
			fakeCLIVersionOutput(t, tt.versionOutput, tt.probeErr)
			buf := captureSlogWarnings(t)

			tr.checkCLIVersion()

			out := buf.String()
			if got := strings.Count(out, "level=WARN"); got != tt.wantWarnings {
				t.Errorf("expected %d warning(s), got %d: %s", tt.wantWarnings, got, out)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("expected warning to mention %q, got: %s", want, out)
				}
			}
			for _, banned := range tt.wantNotContains {
				if strings.Contains(out, banned) {
					t.Errorf("expected warning not to mention %q, got: %s", banned, out)
				}
			}
		})
	}
}

// TestCheckCLIVersionProbePath verifies the probe runs on the transport's
// resolved CLI path.
func TestCheckCLIVersionProbePath(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{})

	var probedPath string
	orig := runCLIVersionCommand
	runCLIVersionCommand = func(path string) (string, error) {
		probedPath = path
		return "2.1.300", nil
	}
	defer func() { runCLIVersionCommand = orig }()

	tr.checkCLIVersion()
	if probedPath != tr.cliPath {
		t.Errorf("expected probe to run on %q, got %q", tr.cliPath, probedPath)
	}
}

// TestCheckCLIVersionSkipEnvEscapeHatch mirrors Python's gate: a non-empty
// CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK disables the version probe entirely —
// the probe command never runs and nothing is logged. An empty value or an
// unset variable still probes (os.environ.get is falsy in both cases).
func TestCheckCLIVersionSkipEnvEscapeHatch(t *testing.T) {
	probeCalls := 0
	orig := runCLIVersionCommand
	runCLIVersionCommand = func(path string) (string, error) {
		probeCalls++
		return "1.9.0 (Claude Code)", nil // old enough to warn on both minima
	}
	t.Cleanup(func() { runCLIVersionCommand = orig })

	run := func(t *testing.T) (int, string) {
		t.Helper()
		probeCalls = 0
		buf := captureSlogWarnings(t)
		tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: true})
		tr.checkCLIVersion()
		return probeCalls, buf.String()
	}

	t.Run("set non-empty skips the probe entirely", func(t *testing.T) {
		t.Setenv(skipVersionCheckEnv, "1")
		calls, out := run(t)
		if calls != 0 {
			t.Errorf("expected no probe run with %s set, got %d call(s)", skipVersionCheckEnv, calls)
		}
		if out != "" {
			t.Errorf("expected no warnings with the probe skipped, got: %s", out)
		}
	})
	t.Run("set to an empty string still probes", func(t *testing.T) {
		t.Setenv(skipVersionCheckEnv, "")
		calls, out := run(t)
		if calls != 1 {
			t.Errorf("expected the probe to run with %s empty, got %d call(s)", skipVersionCheckEnv, calls)
		}
		if !strings.Contains(out, "unsupported") {
			t.Errorf("expected the unsupported-version warning with %s empty, got: %s", skipVersionCheckEnv, out)
		}
	})
	t.Run("unset still probes", func(t *testing.T) {
		unsetEnvForTest(t, skipVersionCheckEnv)
		calls, _ := run(t)
		if calls != 1 {
			t.Errorf("expected the probe to run with %s unset, got %d call(s)", skipVersionCheckEnv, calls)
		}
	})
}

// writeProbeScript writes an executable POSIX sh script standing in for the
// CLI at path `-v` probe time and returns its path.
func writeProbeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-cli")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing probe script: %v", err)
	}
	return path
}

// TestRunCLIVersionCommandBoundsOutput verifies the probe reads at most 8 KiB
// of stdout, so a binary parked at the CLI path cannot inflate SDK memory for
// the probe's 2s lifetime. The fake writes 12000 bytes — above the cap but
// below any pipe buffer, so it exits immediately and Wait stays clean.
func TestRunCLIVersionCommandBoundsOutput(t *testing.T) {
	script := writeProbeScript(t, `printf '2.1.248\n'; head -c 12000 /dev/zero | tr '\0' A`)
	out, err := runCLIVersionCommand(script)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if len(out) != 8*1024 {
		t.Errorf("expected stdout capped at %d bytes, got %d", 8*1024, len(out))
	}
	if !strings.HasPrefix(out, "2.1.248") {
		t.Errorf("expected the first bytes of stdout to survive the cap, got %q", out[:min(32, len(out))])
	}
}

// TestRunCLIVersionCommandStripsCLAUDECODE verifies the probe child gets the
// same env contract as the real child (buildCommand): CLAUDECODE never
// reaches it, so a probe spawned from inside Claude Code does not look nested.
func TestRunCLIVersionCommandStripsCLAUDECODE(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	script := writeProbeScript(t, `printf '%s\n' "${CLAUDECODE-unset}"`)
	out, err := runCLIVersionCommand(script)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if got := strings.TrimSpace(out); got != "unset" {
		t.Errorf("expected CLAUDECODE stripped from the probe env, child saw %q", got)
	}
}

// TestConnectVersionProbeNeverFailsConnect verifies that a failing version
// probe is swallowed entirely: connect still succeeds and stays silent. The
// CLI path must be spawnable for Connect, so a harmless POSIX binary stands
// in for claude (consistent with the sleep-based transport tests).
func TestConnectVersionProbeNeverFailsConnect(t *testing.T) {
	sleep := "sleep"
	if _, err := exec.LookPath(sleep); err != nil {
		t.Skipf("no %s binary available: %v", sleep, err)
	}
	fakeCLIVersionOutput(t, "", errors.New("probe failed"))
	buf := captureSlogWarnings(t)

	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:         sleep,
		VerbatimPrompts: true,
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect must not fail when the version probe errors, got: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if out := buf.String(); out != "" {
		t.Errorf("expected no warnings from a failed probe, got: %s", out)
	}
}

// TestConnectVersionProbeWarnsDuringConnect verifies Connect actually runs
// the probe (not just that checkCLIVersion works in isolation).
func TestConnectVersionProbeWarnsDuringConnect(t *testing.T) {
	sleep := "sleep"
	if _, err := exec.LookPath(sleep); err != nil {
		t.Skipf("no %s binary available: %v", sleep, err)
	}
	fakeCLIVersionOutput(t, "2.1.247 (Claude Code)", nil)
	buf := captureSlogWarnings(t)

	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:         sleep,
		VerbatimPrompts: true,
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if !strings.Contains(buf.String(), "verbatim_prompts") {
		t.Errorf("expected the verbatim_prompts warning during Connect, got: %s", buf.String())
	}
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
