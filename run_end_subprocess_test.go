package claude

// Query() against a stand-in CLI that reports session state (#1190),
// mirroring the Python SDK's tests/test_run_end_subprocess.py.
//
// The stand-in speaks the CLI side of the contract over real stdin/stdout:
// with CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS set it sends
// session_state_changed frames as they are, otherwise with
// CLAUDE_CODE_SDK_READS_SESSION_STATE set it marks them sdk_host_only,
// otherwise it sends none. After its first result a background agent
// "finishes" and wakes a follow-up turn whose hook needs stdin; the stand-in
// only finishes that turn once the hook's answer arrives on stdin, so the
// second result proves stdin was still open.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeCLIScript is the stand-in CLI. It is a POSIX sh script (no bashisms:
// it must run under the shells macOS and the BSDs ship) written to
// t.TempDir() and pointed at via ClaudeAgentOptions.CLIPath.
const fakeCLIScript = `#!/bin/sh
# The transport's Connect probes the CLI version by running "<cli> -v"
# (best effort, every error swallowed): answer fast and exit so the probe
# never slows the spawn down.
case "${1-}" in
-v|--version)
    echo "2.1.999 (Claude Code)"
    exit 0
    ;;
esac

emit_events=false
reads_state=false
case "${CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS-}" in
1|[Tt][Rr][Uu][Ee]|[Yy][Ee][Ss]|[Oo][Nn]) emit_events=true ;;
esac
case "${CLAUDE_CODE_SDK_READS_SESSION_STATE-}" in
1|[Tt][Rr][Uu][Ee]|[Yy][Ee][Ss]|[Oo][Nn]) reads_state=true ;;
esac

emit() {
    printf '%s\n' "$1"
}

state() {
    if $emit_events; then
        emit "{\"type\":\"system\",\"subtype\":\"session_state_changed\",\"state\":\"$1\",\"uuid\":\"u-$1\",\"session_id\":\"s\"}"
    elif $reads_state; then
        emit "{\"type\":\"system\",\"subtype\":\"session_state_changed\",\"state\":\"$1\",\"uuid\":\"u-$1\",\"session_id\":\"s\",\"sdk_host_only\":true}"
    fi
}

assistant() {
    emit "{\"type\":\"assistant\",\"parent_tool_use_id\":null,\"session_id\":\"s\",\"message\":{\"role\":\"assistant\",\"model\":\"m\",\"content\":[{\"type\":\"text\",\"text\":\"$1\"}]}}"
}

result() {
    emit "{\"type\":\"result\",\"subtype\":\"success\",\"duration_ms\":1,\"duration_api_ms\":1,\"is_error\":false,\"num_turns\":1,\"session_id\":\"s\",\"result\":\"$1\"}"
}

if [ "${CLAUDE_CODE_SDK_READS_SESSION_STATE+x}" = "x" ]; then
    sdk_reads_json="\"$CLAUDE_CODE_SDK_READS_SESSION_STATE\""
else
    sdk_reads_json="null"
fi
if [ "${CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS+x}" = "x" ]; then
    emit_events_json="\"$CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS\""
else
    emit_events_json="null"
fi

scenario="${FAKE_CLI_SCENARIO-wake}"

# Report the environment the CLI actually received in the init message, so
# the tests can assert on the SDK's env injection.
emit "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"s\",\"model\":\"m\",\"cwd\":\".\",\"tools\":[],\"mcp_servers\":[],\"permissionMode\":\"default\",\"apiKeySource\":\"none\",\"fake_env\":{\"CLAUDE_CODE_SDK_READS_SESSION_STATE\":$sdk_reads_json,\"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS\":$emit_events_json}}"

hook_id=""
while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
    *'"type":"control_request"'*)
        # The SDK's initialize handshake: answer it and remember the hook
        # callback id it registered.
        request_id=$(printf '%s\n' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
        found_hook=$(printf '%s\n' "$line" | sed -n 's/.*"hookCallbackIds":\["\([^"]*\)".*/\1/p')
        [ -n "$found_hook" ] && hook_id=$found_hook
        emit "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$request_id\",\"response\":{}}}"
        ;;
    *'"type":"user"'*)
        state running
        assistant LAUNCHED
        result LAUNCHED
        if [ "$scenario" = "wake" ]; then
            # The background agent finished just before that result; its
            # completion wakes a turn whose hook needs an answer on stdin.
            assistant writing
            emit "{\"type\":\"control_request\",\"request_id\":\"hook-1\",\"request\":{\"subtype\":\"hook_callback\",\"callback_id\":\"$hook_id\",\"input\":{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Write\",\"tool_input\":{}}},\"tool_use_id\":\"toolu_1\"}"
        fi
        # "stuck": the background agent never finishes, so the state stays
        # "running" until stdin closes.
        ;;
    *'"request_id":"hook-1"'*)
        # The SDK answered the follow-up turn's hook on stdin.
        assistant FINISHED
        result FINISHED
        state idle
        ;;
    esac
done
`

// requireSubprocessCLITest skips when the stand-in CLI cannot run: on
// Windows (it spawns a shebang script, mirroring the Python test's win32
// skip) and in short mode (it spawns real subprocesses).
func requireSubprocessCLITest(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("spawns a shebang script")
	}
	if testing.Short() {
		t.Skip("skipping stand-in CLI subprocess test in short mode")
	}
}

// unsetRunEndEnv removes the variables that steer the run-end behavior from
// the test process' environment, mirroring the Python tests'
// monkeypatch.delenv: the transport scans the inherited environment when
// deciding whether to inject CLAUDE_CODE_SDK_READS_SESSION_STATE (an opt-out
// riding in the process env is honored), and RunEndCeilingMS falls back to
// the process env, so leftovers from the surrounding shell would change what
// these tests exercise. Everything removed here is restored on cleanup.
func unsetRunEndEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		old, had := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("failed to unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			if had {
				if err := os.Setenv(name, old); err != nil {
					t.Errorf("failed to restore %s: %v", name, err)
				}
			} else if err := os.Unsetenv(name); err != nil {
				t.Errorf("failed to unset %s: %v", name, err)
			}
		})
	}
}

// writeFakeCLI writes the stand-in CLI script to t.TempDir() and returns its
// path (executable, cleaned up with the test).
func writeFakeCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake_claude.sh")
	if err := os.WriteFile(path, []byte(fakeCLIScript), 0o755); err != nil {
		t.Fatalf("failed to write fake CLI script: %v", err)
	}
	return path
}

// hookAskRecorder records the tool names the PreToolUse hook was asked
// about. The hook runs on a control-request goroutine while the test
// goroutine reads the message stream, so access is mutex-guarded.
type hookAskRecorder struct {
	mu    sync.Mutex
	tools []string
}

func (r *hookAskRecorder) record(tool string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append(r.tools, tool)
}

func (r *hookAskRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tools...)
}

// runFakeCLIQuery drives Query() end-to-end against the stand-in CLI with a
// PreToolUse hook registered, mirroring the Python tests' _run helper: it
// collects every message (failing on any query error) and the tool names the
// hook was asked about. The 20s bound mirrors anyio.fail_after(20).
func runFakeCLIQuery(t *testing.T, env map[string]string) ([]Message, []string) {
	t.Helper()

	asked := &hookAskRecorder{}
	hook := func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
		asked.record(input.ToolName)
		return HookOutput{}, nil
	}

	options := &ClaudeAgentOptions{
		CLIPath: writeFakeCLI(t),
		Hooks: map[HookEvent][]HookMatcher{
			HookEventPreToolUse: {{Hooks: []HookCallback{hook}}},
		},
		Env: env,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	messages, errs := Query(ctx, "go", options)
	var collected []Message
	for {
		select {
		case msg, ok := <-messages:
			if !ok {
				// errs is closed before messages (Query's deferred closes
				// run innermost-first), so drain anything still buffered.
				for err := range errs {
					if err != nil {
						t.Errorf("query error: %v", err)
					}
				}
				return collected, asked.snapshot()
			}
			collected = append(collected, msg)
		case err, ok := <-errs:
			if ok && err != nil {
				t.Fatalf("query error: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for the query to finish: %v", ctx.Err())
			return nil, nil
		}
	}
}

// runEndResults returns the result texts, in order (Python's _results).
func runEndResults(messages []Message) []string {
	var results []string
	for _, m := range messages {
		if r, ok := m.(*ResultMessage); ok {
			results = append(results, r.Result)
		}
	}
	return results
}

// runEndStates returns the session_state_changed states that reached the
// message channel, in order (Python's _states).
func runEndStates(messages []Message) []string {
	var states []string
	for _, m := range messages {
		if s, ok := m.(*SystemMessage); ok && s.Subtype == "session_state_changed" {
			state, _ := s.Data["state"].(string)
			states = append(states, state)
		}
	}
	return states
}

// runEndFakeEnv returns the fake_env the stand-in CLI reported in its init
// message: the environment the CLI subprocess actually received
// (Python's _fake_env).
func runEndFakeEnv(t *testing.T, messages []Message) map[string]interface{} {
	t.Helper()
	for _, m := range messages {
		if s, ok := m.(*SystemMessage); ok && s.Subtype == "init" {
			fakeEnv, ok := s.Data["fake_env"].(map[string]interface{})
			if !ok {
				t.Fatalf("init message has no fake_env object: %v", s.Data["fake_env"])
			}
			return fakeEnv
		}
	}
	t.Fatal("no init system message in the stream")
	return nil
}

// TestFollowUpTurnIsServedAndSDKFramesStayHidden ports
// test_follow_up_turn_is_served_and_sdk_frames_stay_hidden: the follow-up
// turn's hook is answered on stdin (the second result proves stdin was still
// open past the first), the sdk_host_only-marked state frames stay hidden,
// and the child env carried CLAUDE_CODE_SDK_READS_SESSION_STATE=1 with no
// CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS.
func TestFollowUpTurnIsServedAndSDKFramesStayHidden(t *testing.T) {
	requireSubprocessCLITest(t)
	unsetRunEndEnv(t,
		"CLAUDE_CODE_SDK_READS_SESSION_STATE",
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS",
		"CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS",
	)

	messages, asked := runFakeCLIQuery(t, nil)

	fakeEnv := runEndFakeEnv(t, messages)
	if len(fakeEnv) != 2 {
		t.Fatalf("expected fake_env with exactly 2 entries, got %v", fakeEnv)
	}
	if got := fakeEnv["CLAUDE_CODE_SDK_READS_SESSION_STATE"]; got != "1" {
		t.Errorf("child env CLAUDE_CODE_SDK_READS_SESSION_STATE = %v, want \"1\"", got)
	}
	if got, present := fakeEnv["CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"]; !present || got != nil {
		t.Errorf("child env CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS = %v (present=%v), want absent (null)", got, present)
	}

	if got, want := runEndResults(messages), []string{"LAUNCHED", "FINISHED"}; !reflect.DeepEqual(got, want) {
		t.Errorf("results = %v, want %v", got, want)
	}
	if got, want := asked, []string{"Write"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hook asked about %v, want %v", got, want)
	}
	if got := runEndStates(messages); len(got) != 0 {
		t.Errorf("session states reached the caller: %v, want none", got)
	}
}

// TestCallerWhoOptedInSeesTheFrames ports
// test_caller_who_opted_in_sees_the_frames: with the caller opting in via
// Env{"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS": "1"}, the unmarked state
// frames reach the message channel.
func TestCallerWhoOptedInSeesTheFrames(t *testing.T) {
	requireSubprocessCLITest(t)
	unsetRunEndEnv(t,
		"CLAUDE_CODE_SDK_READS_SESSION_STATE",
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS",
		"CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS",
	)

	messages, asked := runFakeCLIQuery(t, map[string]string{
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS": "1",
	})

	if got, want := runEndResults(messages), []string{"LAUNCHED", "FINISHED"}; !reflect.DeepEqual(got, want) {
		t.Errorf("results = %v, want %v", got, want)
	}
	if got, want := asked, []string{"Write"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hook asked about %v, want %v", got, want)
	}
	if got, want := runEndStates(messages), []string{"running", "idle"}; !reflect.DeepEqual(got, want) {
		t.Errorf("session states = %v, want %v", got, want)
	}
}

// TestCeilingClosesStdinWhenIdleNeverComes ports
// test_ceiling_closes_stdin_when_idle_never_comes: with
// CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS small and "running" never followed by
// "idle", stdin closes ~at the ceiling and the query finishes with just the
// first result.
func TestCeilingClosesStdinWhenIdleNeverComes(t *testing.T) {
	requireSubprocessCLITest(t)
	unsetRunEndEnv(t,
		"CLAUDE_CODE_SDK_READS_SESSION_STATE",
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS",
		"CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS",
	)

	started := time.Now()
	messages, _ := runFakeCLIQuery(t, map[string]string{
		"FAKE_CLI_SCENARIO":                    "stuck",
		"CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS": "300",
	})
	elapsed := time.Since(started)

	if got, want := runEndResults(messages), []string{"LAUNCHED"}; !reflect.DeepEqual(got, want) {
		t.Errorf("results = %v, want %v", got, want)
	}
	if elapsed < 300*time.Millisecond {
		t.Errorf("query finished in %v, before the 300ms run-end ceiling could close stdin", elapsed)
	}
}
