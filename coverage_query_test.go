package claude

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// ---------------------------------------------------------------------------
// wrapTransportError
// ---------------------------------------------------------------------------

func TestWrapTransportErrorCoverage(t *testing.T) {
	if got := wrapTransportError(nil); got != nil {
		t.Errorf("nil error: got %v", got)
	}

	t.Run("result error", func(t *testing.T) {
		inner := &internal.ResultError{
			Message:  "run failed",
			Data:     map[string]interface{}{"subtype": "error_during_execution"},
			ExitCode: 1,
		}
		err := wrapTransportError(inner)
		var resultErr *ResultError
		if !errors.As(err, &resultErr) {
			t.Fatalf("expected *ResultError, got %T", err)
		}
		if resultErr.Subtype != "error_during_execution" {
			t.Errorf("unexpected subtype: %q", resultErr.Subtype)
		}
	})

	t.Run("result error with cause", func(t *testing.T) {
		inner := &internal.ResultError{
			Message:  "run failed",
			Data:     map[string]interface{}{},
			ExitCode: 1,
			Cause:    &transport.ProcessError{Message: "exit", ExitCode: 1},
		}
		err := wrapTransportError(inner)
		var resultErr *ResultError
		if !errors.As(err, &resultErr) {
			t.Fatalf("expected *ResultError, got %T", err)
		}
		if resultErr.Cause == nil {
			t.Error("expected Cause to be set")
		}
		var procErr *ProcessError
		if !errors.As(resultErr.Cause, &procErr) {
			t.Errorf("expected Cause to wrap to *ProcessError, got %T", resultErr.Cause)
		}
	})

	t.Run("cli not found", func(t *testing.T) {
		err := wrapTransportError(&transport.CLINotFoundError{CLIPath: "/missing/claude"})
		if !IsCLINotFoundError(err) {
			t.Fatalf("expected CLINotFoundError, got %T", err)
		}
	})

	t.Run("cli connection", func(t *testing.T) {
		err := wrapTransportError(&transport.CLIConnectionError{Message: "died", Cause: errors.New("boom")})
		if !IsCLIConnectionError(err) {
			t.Fatalf("expected CLIConnectionError, got %T", err)
		}
	})

	t.Run("process error", func(t *testing.T) {
		err := wrapTransportError(&transport.ProcessError{Message: "exit", ExitCode: 2, Stderr: "bad"})
		var procErr *ProcessError
		if !errors.As(err, &procErr) {
			t.Fatalf("expected *ProcessError, got %T", err)
		}
		if procErr.ExitCode != 2 || procErr.Stderr != "bad" {
			t.Errorf("unexpected fields: %+v", procErr)
		}
	})

	t.Run("json decode", func(t *testing.T) {
		err := wrapTransportError(&transport.JSONDecodeError{Line: "{bad", Cause: errors.New("syntax")})
		if !IsCLIJSONDecodeError(err) {
			t.Fatalf("expected CLIJSONDecodeError, got %T", err)
		}
	})

	t.Run("buffer overflow", func(t *testing.T) {
		err := wrapTransportError(&transport.BufferOverflowError{BufferSize: 200, Limit: 100})
		if !IsBufferOverflowError(err) {
			t.Fatalf("expected BufferOverflowError, got %T", err)
		}
	})

	t.Run("unknown passthrough", func(t *testing.T) {
		plain := errors.New("some other error")
		if got := wrapTransportError(plain); got != plain {
			t.Errorf("expected passthrough, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// convertToTransportOptions
// ---------------------------------------------------------------------------

func TestConvertToTransportOptionsFull(t *testing.T) {
	excludeDynamic := true
	opts := &ClaudeAgentOptions{
		Tools:                  []string{"Read"},
		AllowedTools:           []string{"Bash"},
		SystemPrompt:           "sys",
		PermissionMode:         PermissionModePlan,
		ContinueConversation:   true,
		Resume:                 "sess",
		MaxTurns:               3,
		MaxBudgetUSD:           1.5,
		DisallowedTools:        []string{"Write"},
		Model:                  "claude-opus",
		FallbackModel:          "claude-sonnet",
		Betas:                  []SdkBeta{SdkBetaContext1M},
		CWD:                    "/work",
		CLIPath:                "/bin/claude",
		Settings:               "{}",
		AddDirs:                []string{"/extra"},
		Env:                    map[string]string{"K": "V"},
		ExtraArgs:              map[string]string{"flag": "on"},
		MaxBufferSize:          1024,
		SessionID:              "session-id-1",
		IncludePartialMessages: true,
		ForkSession:            true,
		MaxThinkingTokens:      2048,
		TaskBudget:             &TaskBudget{Total: 5000},
		SystemPromptFile:       &SystemPromptFile{Type: "file", Path: "/p.txt"},
		ToolsPreset:            &ToolsPreset{Type: "preset", Preset: "claude_code"},
		SystemPromptPreset: &SystemPromptPreset{
			Type:                   "preset",
			Preset:                 "claude_code",
			Append:                 "extra",
			ExcludeDynamicSections: &excludeDynamic,
		},
		MCPServers: map[string]MCPServerConfig{
			"stdio": &MCPStdioServerConfig{Command: "cmd", Args: []string{"a"}, Env: map[string]string{"E": "1"}},
			"sse":   &MCPSSEServerConfig{Type: "sse", URL: "http://sse", Headers: map[string]string{"H": "1"}},
			"http":  &MCPHTTPServerConfig{Type: "http", URL: "http://http", Headers: map[string]string{"H": "2"}},
			"sdk":   &MCPSdkServerConfig{Type: "sdk", Name: "inproc"},
		},
		Agents: map[string]AgentDefinition{
			"reviewer": {Description: "reviews", Prompt: "review code", Tools: []string{"Read"}, Model: "claude-opus"},
		},
		SettingSources: []SettingSource{SettingSourceUser, SettingSourceProject},
		Plugins:        []PluginConfig{{Type: "local", Path: "/plugin"}},
		Thinking:       &ThinkingConfig{Type: "enabled", BudgetTokens: 1000, Display: "summarized"},
		Skills:         "all",
		SessionStore:   NewInMemorySessionStore(),
		Sandbox: &SandboxSettings{
			Enabled:                  true,
			AutoAllowBashIfSandboxed: true,
			ExcludedCommands:         []string{"rm"},
			Network: &SandboxNetworkConfig{
				AllowedDomains: []string{"example.com"},
				HTTPProxyPort:  8080,
			},
			IgnoreViolations: &SandboxIgnoreViolations{File: []string{"/tmp"}},
		},
	}

	to := convertToTransportOptions(opts)

	if to.SessionID != "session-id-1" {
		t.Errorf("SessionID: got %q", to.SessionID)
	}
	if to.TaskBudget == nil || *to.TaskBudget != 5000 {
		t.Errorf("TaskBudget: got %+v", to.TaskBudget)
	}
	if to.SystemPromptFile == nil || to.SystemPromptFile.Path != "/p.txt" {
		t.Errorf("SystemPromptFile: got %+v", to.SystemPromptFile)
	}
	if to.ToolsPreset == nil || to.ToolsPreset.Preset != "claude_code" {
		t.Errorf("ToolsPreset: got %+v", to.ToolsPreset)
	}
	if to.SystemPromptPreset == nil || to.SystemPromptPreset.Append != "extra" {
		t.Errorf("SystemPromptPreset: got %+v", to.SystemPromptPreset)
	}
	if len(to.MCPServers) != 4 {
		t.Errorf("MCPServers: got %d entries", len(to.MCPServers))
	} else {
		stdio, _ := to.MCPServers["stdio"].(map[string]interface{})
		if stdio["type"] != "stdio" || stdio["command"] != "cmd" {
			t.Errorf("stdio server: got %v", stdio)
		}
		sse, _ := to.MCPServers["sse"].(map[string]interface{})
		if sse["type"] != "sse" || sse["url"] != "http://sse" {
			t.Errorf("sse server: got %v", sse)
		}
		httpSrv, _ := to.MCPServers["http"].(map[string]interface{})
		if httpSrv["type"] != "http" || httpSrv["url"] != "http://http" {
			t.Errorf("http server: got %v", httpSrv)
		}
		sdk, _ := to.MCPServers["sdk"].(map[string]interface{})
		if sdk["type"] != "sdk" || sdk["name"] != "inproc" {
			t.Errorf("sdk server: got %v", sdk)
		}
	}
	agent, _ := to.Agents["reviewer"].(map[string]interface{})
	if agent["description"] != "reviews" || agent["model"] != "claude-opus" {
		t.Errorf("Agents: got %v", agent)
	}
	if len(to.SettingSources) != 2 || to.SettingSources[0] != "user" {
		t.Errorf("SettingSources: got %v", to.SettingSources)
	}
	if len(to.Plugins) != 1 || to.Plugins[0].Path != "/plugin" {
		t.Errorf("Plugins: got %+v", to.Plugins)
	}
	if to.Thinking == nil || to.Thinking.BudgetTokens != 1000 || to.Thinking.Display != "summarized" {
		t.Errorf("Thinking: got %+v", to.Thinking)
	}
	if to.Skills != "all" {
		t.Errorf("Skills: got %v", to.Skills)
	}
	if to.SessionStore == nil {
		t.Error("SessionStore should be passed through")
	}
	if to.Sandbox == nil || !to.Sandbox.Enabled {
		t.Fatalf("Sandbox: got %+v", to.Sandbox)
	}
	if to.Sandbox.Network == nil || to.Sandbox.Network.HTTPProxyPort != 8080 {
		t.Errorf("Sandbox.Network: got %+v", to.Sandbox.Network)
	}
	if to.Sandbox.IgnoreViolations == nil || len(to.Sandbox.IgnoreViolations.File) != 1 {
		t.Errorf("Sandbox.IgnoreViolations: got %+v", to.Sandbox.IgnoreViolations)
	}
}

// ---------------------------------------------------------------------------
// excludeDynamicSectionsFromOpts
// ---------------------------------------------------------------------------

func TestExcludeDynamicSectionsFromOpts(t *testing.T) {
	if got := excludeDynamicSectionsFromOpts(&ClaudeAgentOptions{}); got != nil {
		t.Errorf("no preset: got %v", *got)
	}
	if got := excludeDynamicSectionsFromOpts(&ClaudeAgentOptions{
		SystemPromptPreset: &SystemPromptPreset{Type: "preset", Preset: "claude_code"},
	}); got != nil {
		t.Errorf("preset without flag: got %v", *got)
	}
	flag := false
	got := excludeDynamicSectionsFromOpts(&ClaudeAgentOptions{
		SystemPromptPreset: &SystemPromptPreset{Type: "preset", Preset: "claude_code", ExcludeDynamicSections: &flag},
	})
	if got == nil || *got != false {
		t.Errorf("preset with flag: got %v", got)
	}
}

// ---------------------------------------------------------------------------
// createInternalQueryConfig
// ---------------------------------------------------------------------------

// fakePermissionResult is a PermissionResult implementation unknown to the
// conversion switch, used to exercise the default branch.
type fakePermissionResult struct{}

func (*fakePermissionResult) permissionResultMarker() {}

func TestCreateInternalQueryConfigConversions(t *testing.T) {
	hookCalled := false
	var hookErr error
	sdkServer := CreateSdkMcpServer("test", "1.0.0", nil)

	opts := &ClaudeAgentOptions{
		Hooks: map[HookEvent][]HookMatcher{
			HookEventPreToolUse: {
				{
					Matcher: "Bash",
					Hooks: []HookCallback{
						func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
							hookCalled = true
							return HookOutput{Decision: "block", Reason: "no"}, nil
						},
						func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error) {
							return HookOutput{}, fmt.Errorf("hook exploded")
						},
					},
				},
			},
		},
		MCPServers: map[string]MCPServerConfig{"test": sdkServer},
		Agents:     map[string]AgentDefinition{"a": {Description: "d", Prompt: "p"}},
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			switch toolName {
			case "allow":
				return &PermissionResultAllow{Behavior: "allow", UpdatedInput: map[string]interface{}{"ok": true}}, nil
			case "deny":
				return &PermissionResultDeny{Behavior: "deny", Message: "no", Interrupt: true}, nil
			case "unknown":
				return &fakePermissionResult{}, nil
			default:
				return nil, fmt.Errorf("callback error")
			}
		},
	}

	cfg, err := createInternalQueryConfig(opts, nil)
	if err != nil {
		t.Fatalf("createInternalQueryConfig failed: %v", err)
	}

	// Hooks conversion: invoke the wrapped callbacks.
	matchers := cfg.Hooks[string(HookEventPreToolUse)]
	if len(matchers) != 1 || len(matchers[0].Hooks) != 2 {
		t.Fatalf("hooks not converted: %+v", cfg.Hooks)
	}
	out, err := matchers[0].Hooks[0](internal.HookInput{HookEventName: "PreToolUse", ToolName: "Bash"}, "tu-1", internal.HookContext{})
	if err != nil {
		t.Fatalf("hook call failed: %v", err)
	}
	if !hookCalled {
		t.Error("wrapped hook callback was not invoked")
	}
	if out.Decision != "block" || out.Reason != "no" {
		t.Errorf("unexpected hook output: %+v", out)
	}
	if _, err := matchers[0].Hooks[1](internal.HookInput{}, "tu-2", internal.HookContext{}); err == nil {
		t.Error("expected hook error to propagate")
	} else {
		hookErr = err
	}
	if hookErr == nil || hookErr.Error() != "hook exploded" {
		t.Errorf("unexpected hook error: %v", hookErr)
	}

	// SDK MCP server conversion.
	if len(cfg.SdkMCPServers) != 1 || cfg.SdkMCPServers["test"] == nil {
		t.Errorf("sdk servers not converted: %+v", cfg.SdkMCPServers)
	}

	// Agents conversion.
	if len(cfg.Agents) != 1 {
		t.Errorf("agents not converted: %+v", cfg.Agents)
	}

	// canUseTool conversion branches.
	allowRes, err := cfg.CanUseTool("allow", nil, internal.ToolPermissionContext{ToolUseID: "t1"})
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if _, ok := allowRes.(*internal.PermissionResultAllow); !ok {
		t.Errorf("allow: got %T", allowRes)
	}
	denyRes, err := cfg.CanUseTool("deny", nil, internal.ToolPermissionContext{})
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	internalDeny, ok := denyRes.(*internal.PermissionResultDeny)
	if !ok {
		t.Fatalf("deny: got %T", denyRes)
	}
	if !internalDeny.Interrupt || internalDeny.Message != "no" {
		t.Errorf("deny fields: %+v", internalDeny)
	}
	unknownRes, err := cfg.CanUseTool("unknown", nil, internal.ToolPermissionContext{})
	if err != nil || unknownRes != nil {
		t.Errorf("unknown result should map to (nil, nil), got (%v, %v)", unknownRes, err)
	}
	if _, err := cfg.CanUseTool("boom", nil, internal.ToolPermissionContext{}); err == nil {
		t.Error("expected callback error to propagate")
	}
}

// ---------------------------------------------------------------------------
// Query / QuerySync paths
// ---------------------------------------------------------------------------

// drainQueryChannels drains a Query message/error channel pair with a timeout.
func drainQueryChannels(t *testing.T, messages <-chan Message, errs <-chan error) ([]Message, []error) {
	t.Helper()
	var msgs []Message
	var errorsOut []error
	timeout := time.After(5 * time.Second)
	for messages != nil || errs != nil {
		select {
		case msg, ok := <-messages:
			if !ok {
				messages = nil
				continue
			}
			msgs = append(msgs, msg)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				errorsOut = append(errorsOut, err)
			}
		case <-timeout:
			t.Fatal("timeout draining query")
		}
	}
	return msgs, errorsOut
}

func TestQueryCanUseToolConfigError(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
			return &PermissionResultAllow{Behavior: "allow"}, nil
		},
		PermissionPromptToolName: "stdio",
	}
	messages, errs := Query(context.Background(), "hi", opts)
	_, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) == 0 || !strings.Contains(errorsOut[0].Error(), "permission_prompt_tool_name") {
		t.Errorf("expected configure error, got %v", errorsOut)
	}
}

func TestQueryMakeTransportError(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return nil, &transport.CLIConnectionError{Message: "cannot spawn"}
	}

	messages, errs := Query(context.Background(), "hi", nil)
	_, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) != 1 || !IsCLIConnectionError(errorsOut[0]) {
		t.Errorf("expected wrapped CLIConnectionError, got %v", errorsOut)
	}
}

func TestQueryConnectError(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	mockT.ConnectFunc = func(ctx context.Context) error {
		return &transport.ProcessError{Message: "exit", ExitCode: 3, Stderr: "nope"}
	}
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	messages, errs := Query(context.Background(), "hi", nil)
	_, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) != 1 {
		t.Fatalf("expected 1 error, got %v", errorsOut)
	}
	var procErr *ProcessError
	if !errors.As(errorsOut[0], &procErr) || procErr.ExitCode != 3 {
		t.Errorf("expected *ProcessError exit 3, got %v", errorsOut[0])
	}
}

func TestQueryInitializeError(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	// Respond to the initialize control request with an error response.
	mockT.WriteFunc = func(data string) error {
		if strings.Contains(data, `"initialize"`) {
			reqID := extractJSONStringField(data, "request_id")
			mockT.readCh <- map[string]interface{}{
				"type": "control_response",
				"response": map[string]interface{}{
					"subtype":    "error",
					"request_id": reqID,
					"error":      "init failed",
				},
			}
		}
		return nil
	}
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	messages, errs := Query(context.Background(), "hi", nil)
	_, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) == 0 {
		t.Error("expected initialize error")
	}
}

func TestQueryStringPromptAndLifecycleFrames(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	var writes []string
	handleInitialization(mockT, &writes)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		// task_started for a deferring task type, cleared by task_notification.
		mockT.readCh <- map[string]interface{}{
			"type":        "system",
			"subtype":     "task_started",
			"task_id":     "task-1",
			"task_type":   "local_agent",
			"description": "background agent",
			"uuid":        "u1",
			"session_id":  "s1",
		}
		mockT.readCh <- map[string]interface{}{
			"type":        "system",
			"subtype":     "task_notification",
			"task_id":     "task-1",
			"status":      "completed",
			"output_file": "/tmp/out",
			"summary":     "done",
			"uuid":        "u2",
			"session_id":  "s1",
		}
		// A second task cleared via a terminal task_updated patch.
		mockT.readCh <- map[string]interface{}{
			"type":       "system",
			"subtype":    "task_updated",
			"task_id":    "task-2",
			"patch":      map[string]interface{}{"status": "killed"},
			"session_id": "s1",
		}
		// A frame with no task_id is ignored by the tracker.
		mockT.readCh <- map[string]interface{}{
			"type":    "system",
			"subtype": "task_updated",
			"patch":   map[string]interface{}{"status": "running"},
		}
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(10),
			"duration_api_ms": float64(5),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "s1",
		}
		mockT.Close()
	}()

	messages, errs := Query(context.Background(), "do work", nil)
	msgs, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) != 0 {
		t.Fatalf("unexpected errors: %v", errorsOut)
	}
	var sawStarted, sawNotification, sawUpdated, sawResult bool
	for _, m := range msgs {
		switch m.(type) {
		case *TaskStartedMessage:
			sawStarted = true
		case *TaskNotificationMessage:
			sawNotification = true
		case *TaskUpdatedMessage:
			sawUpdated = true
		case *ResultMessage:
			sawResult = true
		}
	}
	if !sawStarted || !sawNotification || !sawUpdated || !sawResult {
		t.Errorf("missing messages: started=%v notification=%v updated=%v result=%v",
			sawStarted, sawNotification, sawUpdated, sawResult)
	}

	// The string prompt must have been written as a user message after init.
	var foundUser bool
	for _, w := range writes {
		if strings.Contains(w, `"type":"user"`) && strings.Contains(w, "do work") {
			foundUser = true
		}
	}
	if !foundUser {
		t.Error("string prompt was not written to the transport")
	}
}

func TestQueryContextCancel(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	messages, errs := Query(ctx, "hi", nil)

	// Cancel quickly; the query goroutine should observe ctx.Done and return.
	cancel()

	deadline := time.After(5 * time.Second)
	for messages != nil || errs != nil {
		select {
		case _, ok := <-messages:
			if !ok {
				messages = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		case <-deadline:
			t.Fatal("query did not terminate after context cancel")
		}
	}
}

func TestQueryForwardsTransportError(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "hi"}},
				"model":   "claude-opus-4-1-20250805",
			},
		}
		// A transport-level error forwarded through the query error channel.
		mockT.errCh <- errors.New("stream broke")
		mockT.Close()
	}()

	messages, errs := Query(context.Background(), "hi", nil)
	msgs, errorsOut := drainQueryChannels(t, messages, errs)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
	if len(errorsOut) == 0 || !strings.Contains(errorsOut[0].Error(), "stream broke") {
		t.Errorf("expected stream error, got %v", errorsOut)
	}
}

func TestQuerySyncContextDone(t *testing.T) {
	original := makeTransport
	mockT := newMockTransport()
	handleInitialization(mockT, nil)

	// Signal once the query goroutine has read makeTransport, so the restore
	// below cannot race that read.
	invoked := make(chan struct{})
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		close(invoked)
		return mockT, nil
	}
	defer func() {
		<-invoked
		cleanupMakeTransport(original)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := QuerySync(ctx, "hi", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Second batch: read-loop branches
// ---------------------------------------------------------------------------

func TestQueryResultWithInflightTask(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	endInputCalled := make(chan struct{}, 1)
	mockT.EndInputFunc = func() error {
		select {
		case endInputCalled <- struct{}{}:
		default:
		}
		return nil
	}
	handleInitialization(mockT, nil)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		// A deferring task type that never completes: the result frame that
		// follows must NOT close stdin.
		mockT.readCh <- map[string]interface{}{
			"type":        "system",
			"subtype":     "task_started",
			"task_id":     "task-9",
			"task_type":   "local_agent",
			"description": "long agent",
			"uuid":        "u1",
			"session_id":  "s1",
		}
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(1),
			"duration_api_ms": float64(1),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "s1",
		}
		mockT.Close()
	}()

	messages, errs := Query(context.Background(), "hi", nil)
	msgs, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) != 0 {
		t.Fatalf("unexpected errors: %v", errorsOut)
	}
	var sawResult bool
	for _, m := range msgs {
		if _, ok := m.(*ResultMessage); ok {
			sawResult = true
		}
	}
	if !sawResult {
		t.Error("expected a result message")
	}
	select {
	case <-endInputCalled:
		t.Error("EndInput must not be called while a task is in flight")
	default:
	}
}

func TestQueryParseErrorForwarded(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		// Malformed assistant frame (message is not an object): a parse error
		// is reported but the stream continues.
		mockT.readCh <- map[string]interface{}{
			"type":    "assistant",
			"message": "not-a-dict",
		}
		mockT.readCh <- map[string]interface{}{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(1),
			"duration_api_ms": float64(1),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "s1",
		}
		mockT.Close()
	}()

	messages, errs := Query(context.Background(), "hi", nil)
	msgs, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) == 0 {
		t.Error("expected a parse error")
	}
	if !IsMessageParseError(errorsOut[0]) {
		t.Errorf("expected MessageParseError, got %T: %v", errorsOut[0], errorsOut[0])
	}
	if len(msgs) != 1 {
		t.Errorf("stream should continue past the bad frame, got %d messages", len(msgs))
	}
}

func TestQueryUserMessageWriteError(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	initialized := false
	handleInitialization(mockT, nil)
	baseWrite := mockT.WriteFunc
	mockT.WriteFunc = func(data string) error {
		if initialized {
			return fmt.Errorf("stdin broken")
		}
		if strings.Contains(data, `"initialize"`) {
			initialized = true
		}
		return baseWrite(data)
	}
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	messages, errs := Query(context.Background(), "hi", nil)
	_, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) == 0 || !strings.Contains(errorsOut[0].Error(), "stdin broken") {
		t.Errorf("expected write error, got %v", errorsOut)
	}
}

func TestQueryContextCancelInReadLoop(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	messages, errs := Query(ctx, "hi", nil)

	// Let the query reach its read loop before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	deadline := time.After(5 * time.Second)
	for messages != nil || errs != nil {
		select {
		case _, ok := <-messages:
			if !ok {
				messages = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		case <-deadline:
			t.Fatal("query did not terminate after context cancel")
		}
	}
}

func TestQueryDrainsPendingError(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		// Error immediately followed by stream close: depending on select
		// timing, the error is delivered either through the read loop or the
		// drain at the end — both must surface it.
		mockT.errCh <- errors.New("transport died")
		mockT.Close()
	}()

	messages, errs := Query(context.Background(), "hi", nil)
	_, errorsOut := drainQueryChannels(t, messages, errs)
	if len(errorsOut) == 0 || !strings.Contains(errorsOut[0].Error(), "transport died") {
		t.Errorf("expected transport error, got %v", errorsOut)
	}
}

// TestQueryErrorDrainPath exercises the end-of-stream error drain. Depending
// on select timing the transport error is delivered through the main read
// loop or the drain that follows it; a handful of iterations makes both paths
// fire across the run. Either way the error must surface exactly once.
func TestQueryErrorDrainPath(t *testing.T) {
	original := makeTransport
	defer cleanupMakeTransport(original)

	for i := 0; i < 20; i++ {
		mockT := newMockTransport()
		handleInitialization(mockT, nil)
		makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
			return mockT, nil
		}

		go func() {
			// Let initialize complete before the stream dies.
			time.Sleep(5 * time.Millisecond)
			mockT.errCh <- fmt.Errorf("drain me")
			mockT.Close()
		}()

		messages, errs := Query(context.Background(), "hi", nil)
		_, errorsOut := drainQueryChannels(t, messages, errs)
		if len(errorsOut) == 0 {
			t.Fatalf("iteration %d: expected the transport error", i)
		}
	}
}
