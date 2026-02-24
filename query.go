package claude

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
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
			// End input to signal no more messages (matching Python SDK)
			if err := q.EndInput(); err != nil {
				errs <- wrapTransportError(err)
				return
			}
		}

		// Read messages
		// q.RawMessages() gives us raw JSON
		for {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case rawMsg, ok := <-q.RawMessages():
				if !ok {
					// Stream closed
					goto End
				}
				msg, err := ParseMessage(rawMsg)
				if err != nil || msg == nil {
					if err != nil {
						errs <- err
					}
					continue
				}
				messages <- msg
			case err, ok := <-q.Errors():
				if !ok {
					// Error channel closed (usually happens when transport closes)
					goto End
				}
				errs <- wrapTransportError(err)
			}
		}

	End:
		// Drain any remaining errors?
		// internal.Query closes both channels when it stops?
		// q.Close() sets closed flag and closes transport.
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
	var sdkServers map[string]*internal.MCPServer
	if opts.MCPServers != nil {
		sdkServers = make(map[string]*internal.MCPServer)
		for name, config := range opts.MCPServers {
			if sdkConfig, ok := config.(*MCPSdkServerConfig); ok {
				if server, ok := sdkConfig.Instance.(*internal.MCPServer); ok {
					sdkServers[name] = server
				}
			}
		}
	}

	// Convert canUseTool callback
	var canUseTool internal.CanUseToolFunc
	if opts.CanUseTool != nil {
		canUseTool = func(toolName string, input map[string]interface{}, ctx internal.ToolPermissionContext) (internal.PermissionResult, error) {
			publicCtx := ToolPermissionContext{}
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
		Transport:       t,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
		Hooks:           internalHooks,
		SdkMCPServers:   sdkServers,
		Agents:          internalAgents,
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
		MaxThinkingTokens:        opts.MaxThinkingTokens,
		OutputFormat:             opts.OutputFormat,
		EnableFileCheckpointing:  opts.EnableFileCheckpointing,
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
		}
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
				AllowUnixSockets:    opts.Sandbox.Network.AllowUnixSockets,
				AllowAllUnixSockets: opts.Sandbox.Network.AllowAllUnixSockets,
				AllowLocalBinding:   opts.Sandbox.Network.AllowLocalBinding,
				HTTPProxyPort:       opts.Sandbox.Network.HTTPProxyPort,
				SOCKSProxyPort:      opts.Sandbox.Network.SOCKSProxyPort,
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
