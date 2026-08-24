package claude

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// makeTransport is a factory function for creating transports.
// It is exposed as a variable to allow mocking in tests.
var makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
	return transport.NewSubprocessTransport(prompt, opts)
}

// wrapTransportError wraps internal transport errors into public SDK errors.
func wrapTransportError(err error) error {
	if err == nil {
		return nil
	}

	// internal.ResultError carries the CLI's error-result payload; rebuild
	// the public ResultError from it. Checked first because its Unwrap chain
	// contains the original transport.ProcessError.
	var resultErr *internal.ResultError
	if errors.As(err, &resultErr) {
		re := NewResultError(resultErr.Message, resultErr.Data, resultErr.ExitCode)
		if resultErr.Cause != nil {
			re.Cause = wrapTransportError(resultErr.Cause)
		}
		return re
	}

	var cliNotFound *transport.CLINotFoundError
	if errors.As(err, &cliNotFound) {
		return NewCLINotFoundError(cliNotFound.CLIPath)
	}

	var cliConnection *transport.CLIConnectionError
	if errors.As(err, &cliConnection) {
		return NewCLIConnectionError(cliConnection.Message, cliConnection.Cause)
	}

	var processErr *transport.ProcessError
	if errors.As(err, &processErr) {
		return NewProcessError(processErr.Message, processErr.ExitCode, processErr.Stderr)
	}

	var jsonErr *transport.JSONDecodeError
	if errors.As(err, &jsonErr) {
		return NewCLIJSONDecodeError(jsonErr.Line, jsonErr.Cause)
	}

	var bufferErr *transport.BufferOverflowError
	if errors.As(err, &bufferErr) {
		return NewBufferOverflowError(bufferErr.BufferSize, bufferErr.Limit)
	}

	return err
}

// deferringTaskTypes holds the task types whose completion runs a follow-up
// turn, and which therefore may still need the control channel after the
// turn's result frame.
//
// This mirrors the set the CLI itself holds a result back for, which is
// narrower than its notion of "delegated agent work". The types left out are
// left out on purpose, and none of them is merely an oversight:
//   - background shells and monitors run indefinitely by design, so deferring
//     the close on one withholds it forever rather than briefly;
//   - teammates are long-lived too — their status stays running for their whole
//     lifetime, so they never settle the ledger;
//   - remote agents can be long-running monitors the CLI likewise refuses to
//     wait on.
//
// Anything added here must be a type that reliably reaches a terminal status,
// or it will hang the query (see taskLifecycleTracker.track).
var deferringTaskTypes = map[string]bool{
	"local_agent":    true,
	"local_workflow": true,
}

// taskLifecycleTracker tracks in-flight tasks from "system" task lifecycle
// frames so a result frame can tell "one turn ended" apart from "the run is
// done" (see #1088).
//
// task_started marks a task in flight; task_notification or a task_updated
// patch with a terminal status clears it. Terminal completion can arrive as
// either frame (not every terminal task emits a notification), so both are
// handled; deleting from a map keeps the pair idempotent.
//
// This is a mitigation, not a complete answer to #1088. An empty set means
// "nothing we know of is running", which is not the same as "the run is
// over": a task that settles *before* the turn's result frame leaves the set
// empty at that result, so stdin closes even though the completion may still
// wake the parent for a continuation turn. No ledger can close that gap,
// because the ledger cannot distinguish a settled task whose continuation is
// pending from no work at all — that needs a run-boundary signal from the CLI
// rather than an inference from task bookkeeping. What this does fix is the
// common ordering, where the task outlives the turn that spawned it.
//
// Only delegated agent work is tracked (deferringTaskTypes). A background
// *shell* — Bash(run_in_background=true) on a dev server or tail -f — is also
// reported through these frames, but it may never reach a terminal status,
// and the CLI in stream-json mode only exits on stdin EOF. Tracking one would
// therefore withhold the close forever rather than briefly. Agent tasks are
// the ones whose completion wakes the parent for the follow-up turn this
// relies on; shells and monitors are bounded by the CLI's own post-close
// cleanup instead.
//
// background_tasks_changed is deliberately *not* consumed, in either
// direction. Its payload is the live *background* set, while a subagent is
// registered in the foreground and only flips to backgrounded later, without
// a second task_started. So the snapshot omits tracked work that is still
// running: narrowing against it would drop an agent that goes on to outlive
// its turn, which is the very close-too-early bug this tracker exists to
// prevent. Widening from it is no better — the snapshot spans every
// background task type and carries nothing marking an observer agent, whose
// start and terminal frames are both suppressed, so it could admit an id no
// later frame ever clears. The lifecycle frames are the only self-consistent
// source here (see #1088).
//
// Not safe for concurrent use; the Query message loop is single-goroutine.
type taskLifecycleTracker struct {
	inflightTasks map[string]bool
}

func newTaskLifecycleTracker() *taskLifecycleTracker {
	return &taskLifecycleTracker{inflightTasks: make(map[string]bool)}
}

// track consumes one raw "system" frame, updating the in-flight task set.
// Frames without a task_id, and non-lifecycle subtypes, are ignored.
func (t *taskLifecycleTracker) track(message map[string]interface{}) {
	subtype, _ := message["subtype"].(string)
	taskID, _ := message["task_id"].(string)
	if taskID == "" {
		return
	}
	switch subtype {
	case "task_started":
		if taskType, _ := message["task_type"].(string); deferringTaskTypes[taskType] {
			t.inflightTasks[taskID] = true
		}
	case "task_notification":
		delete(t.inflightTasks, taskID)
	case "task_updated":
		var status string
		if patch, ok := message["patch"].(map[string]interface{}); ok {
			status, _ = patch["status"].(string)
		}
		if TerminalTaskStatuses[status] {
			delete(t.inflightTasks, taskID)
		}
	}
}

// hasInflight reports whether any tracked task is still running.
func (t *taskLifecycleTracker) hasInflight() bool {
	return len(t.inflightTasks) > 0
}

// Query performs a one-shot query to Claude Code.
// It returns two channels: one for messages and one for errors.
// The message channel is closed when the query completes.
func Query(ctx context.Context, prompt interface{}, opts *ClaudeAgentOptions) (<-chan Message, <-chan error) {
	messages := make(chan Message, 100)
	errs := make(chan error, 10)

	if opts == nil {
		opts = DefaultOptions()
	}

	go func() {
		defer close(messages)
		defer close(errs)

		// Validate and configure permission settings: checks CanUseTool is
		// not combined with PermissionPromptToolName, emits the shadowing
		// advisory, and routes permission prompts over the control protocol
		// (mirrors the Python SDK's _configure_can_use_tool).
		configuredOpts, err := configureCanUseTool(opts)
		if err != nil {
			errs <- err
			return
		}
		opts = configuredOpts

		// Convert to transport options
		transportOpts := convertToTransportOptions(opts)

		// Create transport
		t, err := makeTransport(prompt, transportOpts)
		if err != nil {
			errs <- wrapTransportError(err)
			return
		}
		// We don't defer t.Close() here because internal.Query handles it when Close() is called
		// But internal.Query.Close() calls t.Close().
		// And we call query.Close() at the end.

		// Connect
		if err := t.Connect(ctx); err != nil {
			t.Close()
			errs <- wrapTransportError(err)
			return
		}

		// Create internal query
		queryConfig, err := createInternalQueryConfig(opts, t)
		if err != nil {
			t.Close()
			errs <- err
			return
		}

		q := internal.NewQuery(queryConfig)
		q.Start()
		defer q.Close()

		// Initialize
		if _, err := q.Initialize(ctx); err != nil {
			errs <- wrapTransportError(err)
			return
		}

		// For string prompts, write user message to stdin after initialize
		// (matching Python SDK behavior)
		if promptStr, isString := prompt.(string); isString {
			userMessage := map[string]interface{}{
				"type":       "user",
				"session_id": "",
				"message": map[string]interface{}{
					"role":    "user",
					"content": promptStr,
				},
				"parent_tool_use_id": nil,
			}
			data, err := json.Marshal(userMessage)
			if err != nil {
				errs <- err
				return
			}
			if err := q.Write(string(data) + "\n"); err != nil {
				errs <- wrapTransportError(err)
				return
			}
			// NOTE: Do NOT call EndInput() here. Closing stdin prevents
			// the SDK from writing MCP control_response messages back to
			// the CLI when SDK MCP servers are configured. The CLI
			// processes user messages from stream-json stdin immediately;
			// it does not require an EOF to begin. Stdin will be closed
			// when q.Close() is called via defer above.
		}

		// Read messages
		// q.RawMessages() gives us raw JSON.
		// All channel sends use select with ctx.Done() to prevent
		// goroutine leaks when QuerySync returns early on timeout.
		tracker := newTaskLifecycleTracker()
		for {
			select {
			case <-ctx.Done():
				select {
				case errs <- ctx.Err():
				default:
				}
				return
			case rawMsg, ok := <-q.RawMessages():
				if !ok {
					// Stream closed
					goto End
				}
				// Track task lifecycle frames so results can tell "one turn
				// ended" apart from "the run is done" (see #1088).
				if msgType, _ := rawMsg["type"].(string); msgType == "system" {
					tracker.track(rawMsg)
				}
				msg, err := ParseMessage(rawMsg)
				if err != nil || msg == nil {
					if err != nil {
						select {
						case errs <- err:
						case <-ctx.Done():
							return
						}
					}
					continue
				}
				select {
				case messages <- msg:
				case <-ctx.Done():
					return
				}

				// After forwarding a ResultMessage with no tasks in flight,
				// close stdin so the CLI process can exit. This breaks the
				// deadlock where the goroutine waits for rawMessages to
				// close, but rawMessages only closes when the CLI exits, and
				// the CLI only exits when stdin closes (via defer q.Close),
				// which only fires when the goroutine exits.
				//
				// A result frame ends one turn, not necessarily the run:
				// background tasks keep running past it and still need stdin
				// for hook/SDK-MCP control responses (#1088), so a result
				// that arrives while tasks are in flight must not close
				// stdin. Each task completion wakes the parent for a
				// follow-up turn, so a later result frame arrives with no
				// tasks in flight and closes stdin then.
				//
				// EndInput() only closes stdin; the CLI can still flush
				// remaining output before it exits, so we keep reading
				// until the channels close naturally.
				if _, isResult := msg.(*ResultMessage); isResult && !tracker.hasInflight() {
					q.EndInput()
				}
			case err, ok := <-q.Errors():
				if !ok {
					// Error channel closed (usually happens when transport closes)
					goto End
				}
				select {
				case errs <- wrapTransportError(err):
				case <-ctx.Done():
					return
				}
			}
		}

	End:
		// Drain errors the read loop delivered before closing rawMessages:
		// it forwards transport errors first, but the select above may
		// observe the channel close before picking the buffered error up.
		// q.Errors() is never closed (by design), so drain non-blockingly.
		for {
			select {
			case err := <-q.Errors():
				select {
				case errs <- wrapTransportError(err):
				case <-ctx.Done():
					return
				}
			default:
				return
			}
		}
	}()

	return messages, errs
}

// QuerySync performs a synchronous query, collecting all messages.
// It blocks until the query completes and returns all messages or an error.
func QuerySync(ctx context.Context, prompt interface{}, opts *ClaudeAgentOptions) ([]Message, error) {
	messages, errs := Query(ctx, prompt, opts)

	var result []Message
	var lastErr error

	for {
		select {
		case msg, ok := <-messages:
			if !ok {
				return result, lastErr
			}
			result = append(result, msg)
		case err := <-errs:
			if err != nil {
				lastErr = err
			}
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
}

// createInternalQueryConfig converts ClaudeAgentOptions to internal.QueryConfig
func createInternalQueryConfig(opts *ClaudeAgentOptions, t transport.Transport) (internal.QueryConfig, error) {
	// Convert hooks to internal format
	var internalHooks map[string][]internal.HookMatcherInternal
	if opts.Hooks != nil {
		internalHooks = make(map[string][]internal.HookMatcherInternal)
		for event, matchers := range opts.Hooks {
			internalHooks[string(event)] = make([]internal.HookMatcherInternal, len(matchers))
			for i, m := range matchers {
				// Convert callbacks
				internalCallbacks := make([]internal.HookCallback, len(m.Hooks))
				for j, cb := range m.Hooks {
					cb := cb // capture
					internalCallbacks[j] = func(input internal.HookInput, toolUseID string, ctx internal.HookContext) (internal.HookOutput, error) {
						// Convert internal types to public types
						publicInput := HookInput{
							HookEventName:  input.HookEventName,
							SessionID:      input.SessionID,
							TranscriptPath: input.TranscriptPath,
							CWD:            input.CWD,
							PermissionMode: input.PermissionMode,
							ToolName:       input.ToolName,
							ToolInput:      input.ToolInput,
							ToolResponse:   input.ToolResponse,
							Prompt:         input.Prompt,
						}
						publicCtx := HookContext{}

						output, err := cb(publicInput, toolUseID, publicCtx)
						if err != nil {
							return internal.HookOutput{}, err
						}

						return internal.HookOutput{
							Continue:           output.Continue,
							SuppressOutput:     output.SuppressOutput,
							StopReason:         output.StopReason,
							Decision:           output.Decision,
							SystemMessage:      output.SystemMessage,
							Reason:             output.Reason,
							HookSpecificOutput: output.HookSpecificOutput,
						}, nil
					}
				}
				internalHooks[string(event)][i] = internal.HookMatcherInternal{
					Matcher: m.Matcher,
					Hooks:   internalCallbacks,
					Timeout: m.Timeout,
				}
			}
		}
	}

	// Convert SDK MCP servers
	var sdkServers map[string]*mcp.Server
	if opts.MCPServers != nil {
		sdkServers = make(map[string]*mcp.Server)
		for name, config := range opts.MCPServers {
			if sdkConfig, ok := config.(*MCPSdkServerConfig); ok {
				if sdkConfig.Instance != nil {
					sdkServers[name] = sdkConfig.Instance
				}
			}
		}
	}

	// Convert canUseTool callback
	var canUseTool internal.CanUseToolFunc
	if opts.CanUseTool != nil {
		canUseTool = func(toolName string, input map[string]interface{}, ctx internal.ToolPermissionContext) (internal.PermissionResult, error) {
			publicCtx := ToolPermissionContext{
				ToolUseID: ctx.ToolUseID,
				AgentID:   ctx.AgentID,
			}
			result, err := opts.CanUseTool(toolName, input, publicCtx)
			if err != nil {
				return nil, err
			}

			switch r := result.(type) {
			case *PermissionResultAllow:
				return &internal.PermissionResultAllow{
					UpdatedInput: r.UpdatedInput,
				}, nil
			case *PermissionResultDeny:
				return &internal.PermissionResultDeny{
					Message:   r.Message,
					Interrupt: r.Interrupt,
				}, nil
			default:
				return nil, nil
			}
		}
	}

	// Convert Agents
	var internalAgents map[string]interface{}
	if opts.Agents != nil {
		internalAgents = make(map[string]interface{})
		for k, v := range opts.Agents {
			internalAgents[k] = v
		}
	}

	return internal.QueryConfig{
		Transport:              t,
		IsStreamingMode:        true,
		CanUseTool:             canUseTool,
		Hooks:                  internalHooks,
		SdkMCPServers:          sdkServers,
		Agents:                 internalAgents,
		ExcludeDynamicSections: excludeDynamicSectionsFromOpts(opts),
		Skills:                 opts.Skills,
		ForwardSubagentText:    opts.ForwardSubagentText,
	}, nil
}

// convertToTransportOptions converts ClaudeAgentOptions to TransportOptions.
func convertToTransportOptions(opts *ClaudeAgentOptions) *transport.TransportOptions {

	transportOpts := &transport.TransportOptions{
		Tools:                    opts.Tools,
		AllowedTools:             opts.AllowedTools,
		SystemPrompt:             opts.SystemPrompt,
		PermissionMode:           string(opts.PermissionMode),
		ContinueConversation:     opts.ContinueConversation,
		Resume:                   opts.Resume,
		MaxTurns:                 opts.MaxTurns,
		MaxBudgetUSD:             opts.MaxBudgetUSD,
		DisallowedTools:          opts.DisallowedTools,
		Model:                    opts.Model,
		FallbackModel:            opts.FallbackModel,
		Betas:                    opts.Betas,
		PermissionPromptToolName: opts.PermissionPromptToolName,
		CWD:                      opts.CWD,
		CLIPath:                  opts.CLIPath,
		Settings:                 opts.Settings,
		AddDirs:                  opts.AddDirs,
		Env:                      opts.Env,
		ExtraArgs:                opts.ExtraArgs,
		MaxBufferSize:            opts.MaxBufferSize,
		StderrCallback:           opts.StderrCallback,
		IncludePartialMessages:   opts.IncludePartialMessages,
		ForkSession:              opts.ForkSession,
		ResumeSessionAt:          opts.ResumeSessionAt,
		ResumeDropsTurn:          opts.ResumeDropsTurn,
		MaxThinkingTokens:        opts.MaxThinkingTokens,
		OutputFormat:             opts.OutputFormat,
		EnableFileCheckpointing:  opts.EnableFileCheckpointing,
		Effort:                   opts.Effort,
		StrictMCPConfig:          opts.StrictMCPConfig,
		IncludeHookEvents:        opts.IncludeHookEvents,
	}

	if opts.SessionID != "" {
		transportOpts.SessionID = opts.SessionID
	}

	if opts.TaskBudget != nil {
		total := opts.TaskBudget.Total
		transportOpts.TaskBudget = &total
	}

	if opts.SystemPromptFile != nil {
		transportOpts.SystemPromptFile = &transport.SystemPromptFile{
			Type: opts.SystemPromptFile.Type,
			Path: opts.SystemPromptFile.Path,
		}
	}

	if opts.ToolsPreset != nil {
		transportOpts.ToolsPreset = &transport.ToolsPreset{
			Type:   opts.ToolsPreset.Type,
			Preset: opts.ToolsPreset.Preset,
		}
	}

	if opts.SystemPromptPreset != nil {
		transportOpts.SystemPromptPreset = &transport.SystemPromptPreset{
			Type:   opts.SystemPromptPreset.Type,
			Preset: opts.SystemPromptPreset.Preset,
			Append: opts.SystemPromptPreset.Append,
		}
	}

	// Convert MCP servers
	if opts.MCPServers != nil {
		transportOpts.MCPServers = make(map[string]interface{})
		for name, config := range opts.MCPServers {
			switch c := config.(type) {
			case *MCPStdioServerConfig:
				transportOpts.MCPServers[name] = map[string]interface{}{
					"type":    "stdio",
					"command": c.Command,
					"args":    c.Args,
					"env":     c.Env,
				}
			case *MCPSSEServerConfig:
				transportOpts.MCPServers[name] = map[string]interface{}{
					"type":    "sse",
					"url":     c.URL,
					"headers": c.Headers,
				}
			case *MCPHTTPServerConfig:
				transportOpts.MCPServers[name] = map[string]interface{}{
					"type":    "http",
					"url":     c.URL,
					"headers": c.Headers,
				}
			case *MCPSdkServerConfig:
				transportOpts.MCPServers[name] = map[string]interface{}{
					"type": "sdk",
					"name": c.Name,
				}
			}
		}
	}

	// Convert agents
	if opts.Agents != nil {
		transportOpts.Agents = make(map[string]interface{})
		for name, agent := range opts.Agents {
			transportOpts.Agents[name] = map[string]interface{}{
				"description": agent.Description,
				"prompt":      agent.Prompt,
				"tools":       agent.Tools,
				"model":       agent.Model,
			}
		}
	}

	// Convert setting sources
	if opts.SettingSources != nil {
		transportOpts.SettingSources = make([]string, len(opts.SettingSources))
		for i, s := range opts.SettingSources {
			transportOpts.SettingSources[i] = string(s)
		}
	}

	// Convert plugins
	if opts.Plugins != nil {
		transportOpts.Plugins = make([]transport.PluginConfig, len(opts.Plugins))
		for i, p := range opts.Plugins {
			transportOpts.Plugins[i] = transport.PluginConfig{
				Type: p.Type,
				Path: p.Path,
			}
		}
	}

	// Convert Thinking
	if opts.Thinking != nil {
		transportOpts.Thinking = &transport.ThinkingConfig{
			Type:         opts.Thinking.Type,
			BudgetTokens: opts.Thinking.BudgetTokens,
			Display:      opts.Thinking.Display,
		}
	}

	// Skills: pass as-is (string or []string).
	if opts.Skills != nil {
		transportOpts.Skills = opts.Skills
	}

	// SessionStore: pass as interface{} to avoid circular import.
	if opts.SessionStore != nil {
		transportOpts.SessionStore = opts.SessionStore
	}

	// Convert Sandbox
	if opts.Sandbox != nil {
		transportOpts.Sandbox = &transport.SandboxSettings{
			Enabled:                   opts.Sandbox.Enabled,
			AutoAllowBashIfSandboxed:  opts.Sandbox.AutoAllowBashIfSandboxed,
			ExcludedCommands:          opts.Sandbox.ExcludedCommands,
			AllowUnsandboxedCommands:  opts.Sandbox.AllowUnsandboxedCommands,
			EnableWeakerNestedSandbox: opts.Sandbox.EnableWeakerNestedSandbox,
		}
		if opts.Sandbox.Network != nil {
			transportOpts.Sandbox.Network = &transport.SandboxNetworkConfig{
				AllowedDomains:          opts.Sandbox.Network.AllowedDomains,
				DeniedDomains:           opts.Sandbox.Network.DeniedDomains,
				AllowManagedDomainsOnly: opts.Sandbox.Network.AllowManagedDomainsOnly,
				AllowUnixSockets:        opts.Sandbox.Network.AllowUnixSockets,
				AllowAllUnixSockets:     opts.Sandbox.Network.AllowAllUnixSockets,
				AllowLocalBinding:       opts.Sandbox.Network.AllowLocalBinding,
				AllowMachLookup:         opts.Sandbox.Network.AllowMachLookup,
				HTTPProxyPort:           opts.Sandbox.Network.HTTPProxyPort,
				SOCKSProxyPort:          opts.Sandbox.Network.SOCKSProxyPort,
			}
		}
		if opts.Sandbox.IgnoreViolations != nil {
			transportOpts.Sandbox.IgnoreViolations = &transport.SandboxIgnoreViolations{
				File:    opts.Sandbox.IgnoreViolations.File,
				Network: opts.Sandbox.IgnoreViolations.Network,
			}
		}
	}

	return transportOpts
}

// excludeDynamicSectionsFromOpts extracts ExcludeDynamicSections from a
// SystemPromptPreset option, mirroring the Python SDK's client.py logic.
func excludeDynamicSectionsFromOpts(opts *ClaudeAgentOptions) *bool {
	if opts.SystemPromptPreset != nil && opts.SystemPromptPreset.ExcludeDynamicSections != nil {
		return opts.SystemPromptPreset.ExcludeDynamicSections
	}
	return nil
}
