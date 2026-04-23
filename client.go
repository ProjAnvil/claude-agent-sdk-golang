package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// ClaudeSDKClient provides bidirectional communication with Claude Code.
type ClaudeSDKClient struct {
	options          *ClaudeAgentOptions
	transport        transport.Transport
	transportFactory func(interface{}, *transport.TransportOptions) (transport.Transport, error)
	query            *internal.Query
	mu               sync.RWMutex
	connected        bool
	materialized     *MaterializedResume
}

// NewClient creates a new ClaudeSDKClient.
func NewClient(opts *ClaudeAgentOptions) *ClaudeSDKClient {
	if opts == nil {
		opts = DefaultOptions()
	}
	return &ClaudeSDKClient{
		options: opts,
		transportFactory: func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
			t, err := transport.NewSubprocessTransport(prompt, opts)
			if err != nil {
				return nil, err
			}
			return t, nil
		},
	}
}

// Connect establishes connection to Claude Code CLI.
// prompt can be a string, a channel of messages, or nil for interactive mode.
func (c *ClaudeSDKClient) Connect(ctx context.Context, prompt ...interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Fail fast on invalid session_store option combinations before spawn.
	if err := validateSessionStoreOptions(c.options); err != nil {
		return err
	}

	// resume/continue + session_store: load the session from the store and
	// materialize it into a temp CLAUDE_CONFIG_DIR the subprocess can read.
	if c.options != nil && c.options.SessionStore != nil &&
		(c.options.Resume != "" || c.options.ContinueConversation) {
		m, err := materializeResumeSession(ctx, c.options)
		if err != nil {
			return err
		}
		if m != nil {
			c.materialized = m
			c.options = applyMaterializedOptions(c.options, m)
		}
	}

	// Convert options
	transportOpts := convertToTransportOptions(c.options)

	var promptArg interface{}
	var stringPrompt string
	if len(prompt) > 0 {
		if s, ok := prompt[0].(string); ok {
			// String prompts are sent via transport.write() after initialize,
			// so the transport only needs an empty channel.
			stringPrompt = s
			promptArg = make(chan map[string]interface{})
		} else {
			promptArg = prompt[0]
		}
	} else {
		// Default to empty channel for interactive mode
		promptArg = make(chan map[string]interface{})
	}

	// Create transport
	t, err := c.transportFactory(promptArg, transportOpts)
	if err != nil {
		return err
	}

	if err := t.Connect(ctx); err != nil {
		return err
	}

	c.transport = t

	// Convert hooks to internal format
	var internalHooks map[string][]internal.HookMatcherInternal
	if c.options.Hooks != nil {
		internalHooks = make(map[string][]internal.HookMatcherInternal)
		for event, matchers := range c.options.Hooks {
			internalHooks[string(event)] = make([]internal.HookMatcherInternal, len(matchers))
			for i, m := range matchers {
				// Convert callbacks
				internalCallbacks := make([]internal.HookCallback, len(m.Hooks))
				for j, cb := range m.Hooks {
					cb := cb // capture
					internalCallbacks[j] = func(input internal.HookInput, toolUseID string, ctx internal.HookContext) (internal.HookOutput, error) {
						// Convert internal types to public types
						publicInput := HookInput{
							HookEventName:         input.HookEventName,
							SessionID:             input.SessionID,
							TranscriptPath:        input.TranscriptPath,
							CWD:                   input.CWD,
							PermissionMode:        input.PermissionMode,
							ToolName:              input.ToolName,
							ToolInput:             input.ToolInput,
							ToolResponse:          input.ToolResponse,
							ToolUseID:             input.ToolUseID,
							Error:                 input.Error,
							IsInterrupt:           input.IsInterrupt,
							Prompt:                input.Prompt,
							StopHookActive:        input.StopHookActive,
							AgentID:               input.AgentID,
							AgentTranscriptPath:   input.AgentTranscriptPath,
							AgentType:             input.AgentType,
							Trigger:               input.Trigger,
							CustomInstructions:    input.CustomInstructions,
							Message:               input.Message,
							Title:                 input.Title,
							NotificationType:      input.NotificationType,
							PermissionSuggestions: input.PermissionSuggestions,
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
	if c.options.MCPServers != nil {
		sdkServers = make(map[string]*internal.MCPServer)
		for name, config := range c.options.MCPServers {
			if sdkConfig, ok := config.(*MCPSdkServerConfig); ok {
				if server, ok := sdkConfig.Instance.(*internal.MCPServer); ok {
					sdkServers[name] = server
				}
			}
		}
	}

	// Convert canUseTool callback
	var canUseTool internal.CanUseToolFunc
	if c.options.CanUseTool != nil {
		canUseTool = func(toolName string, input map[string]interface{}, ctx internal.ToolPermissionContext) (internal.PermissionResult, error) {
			publicCtx := ToolPermissionContext{
				ToolUseID: ctx.ToolUseID,
				AgentID:   ctx.AgentID,
			}
			result, err := c.options.CanUseTool(toolName, input, publicCtx)
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
	if c.options.Agents != nil {
		internalAgents = make(map[string]interface{})
		for k, v := range c.options.Agents {
			internalAgents[k] = v
		}
	}

	// Create query handler
	c.query = internal.NewQuery(internal.QueryConfig{
		Transport:       t,
		IsStreamingMode: true,
		CanUseTool:      canUseTool,
		Hooks:           internalHooks,
		SdkMCPServers:   sdkServers,
		Agents:          internalAgents,
	})

	c.query.Start()

	// Initialize
	if _, err := c.query.Initialize(ctx); err != nil {
		c.transport.Close()
		return err
	}

	// If we have a string prompt, send it as a user message after initialize
	if stringPrompt != "" {
		message := map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"role":    "user",
				"content": stringPrompt,
			},
			"parent_tool_use_id": nil,
			"session_id":         "default",
		}
		data, err := json.Marshal(message)
		if err != nil {
			c.transport.Close()
			return err
		}
		if err := c.query.Write(string(data) + "\n"); err != nil {
			c.transport.Close()
			return err
		}
	}

	c.connected = true
	return nil
}

// QueryOption configures a query or send operation.
type QueryOption func(*queryOptions)

type queryOptions struct {
	sessionID string
}

// WithSessionID sets the session ID for the message.
func WithSessionID(id string) QueryOption {
	return func(o *queryOptions) {
		o.sessionID = id
	}
}

// Send sends a message to Claude without waiting for a response.
func (c *ClaudeSDKClient) Send(ctx context.Context, prompt string, opts ...QueryOption) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	qOpts := &queryOptions{
		sessionID: "default",
	}
	for _, opt := range opts {
		opt(qOpts)
	}

	message := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": prompt,
		},
		"parent_tool_use_id": nil,
		"session_id":         qOpts.sessionID,
	}

	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return c.query.Write(string(data) + "\n")
}

// Query sends a message and returns a channel of responses.
func (c *ClaudeSDKClient) Query(ctx context.Context, prompt string, opts ...QueryOption) (<-chan Message, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return nil, NewCLIConnectionError("not connected", nil)
	}

	qOpts := &queryOptions{
		sessionID: "default",
	}
	for _, opt := range opts {
		opt(qOpts)
	}

	// Send the message
	message := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": prompt,
		},
		"parent_tool_use_id": nil,
		"session_id":         qOpts.sessionID,
	}

	data, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}

	if err := c.query.Write(string(data) + "\n"); err != nil {
		return nil, err
	}

	// Return message channel
	messages := make(chan Message, 100)
	go func() {
		defer close(messages)
		for rawMsg := range c.query.RawMessages() {
			msg, err := ParseMessage(rawMsg)
			if err != nil || msg == nil {
				continue
			}
			messages <- msg
		}
	}()

	return messages, nil
}

// ReceiveResponse returns messages until a ResultMessage is received.
func (c *ClaudeSDKClient) ReceiveResponse(ctx context.Context) (<-chan Message, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return nil, NewCLIConnectionError("not connected", nil)
	}

	messages := make(chan Message, 100)
	go func() {
		defer close(messages)
		for rawMsg := range c.query.RawMessages() {
			msg, err := ParseMessage(rawMsg)
			if err != nil || msg == nil {
				continue
			}
			messages <- msg

			// Stop after ResultMessage
			if _, ok := msg.(*ResultMessage); ok {
				return
			}
		}
	}()

	return messages, nil
}

// Interrupt sends an interrupt signal.
func (c *ClaudeSDKClient) Interrupt(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.Interrupt(ctx)
}

// SetPermissionMode changes the permission mode.
func (c *ClaudeSDKClient) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.SetPermissionMode(ctx, string(mode))
}

// SetModel changes the AI model.
func (c *ClaudeSDKClient) SetModel(ctx context.Context, model string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.SetModel(ctx, model)
}

// RewindFiles rewinds tracked files to a specific user message.
func (c *ClaudeSDKClient) RewindFiles(ctx context.Context, userMessageID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.RewindFiles(ctx, userMessageID)
}

// ReconnectMCPServer reconnects to an MCP server.
func (c *ClaudeSDKClient) ReconnectMCPServer(ctx context.Context, serverName string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.ReconnectMCPServer(ctx, serverName)
}

// ToggleMCPServer enables or disables an MCP server.
func (c *ClaudeSDKClient) ToggleMCPServer(ctx context.Context, serverName string, enabled bool) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.ToggleMCPServer(ctx, serverName, enabled)
}

// StopTask stops a running task.
func (c *ClaudeSDKClient) StopTask(ctx context.Context, taskID string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return NewCLIConnectionError("not connected", nil)
	}

	return c.query.StopTask(ctx, taskID)
}

// GetMCPStatus returns the current MCP server connection status.
func (c *ClaudeSDKClient) GetMCPStatus(ctx context.Context) (*McpStatusResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return nil, NewCLIConnectionError("not connected", nil)
	}

	rawData, err := c.query.GetMCPStatus(ctx)
	if err != nil {
		return nil, err
	}

	// Parse the response into McpStatusResponse
	response := &McpStatusResponse{}
	if servers, ok := rawData["mcpServers"].([]interface{}); ok {
		response.MCPServers = make([]McpServerStatus, 0, len(servers))
		for _, server := range servers {
			if serverMap, ok := server.(map[string]interface{}); ok {
				serverStatus := McpServerStatus{
					Name:   toString(serverMap["name"]),
					Status: McpServerConnectionStatus(toString(serverMap["status"])),
					Error:  toString(serverMap["error"]),
					Scope:  toString(serverMap["scope"]),
				}

				// Parse serverInfo if present
				if serverInfo, ok := serverMap["serverInfo"].(map[string]interface{}); ok {
					serverStatus.ServerInfo = &McpServerInfo{
						Name:    toString(serverInfo["name"]),
						Version: toString(serverInfo["version"]),
					}
				}

				// Parse tools if present
				if tools, ok := serverMap["tools"].([]interface{}); ok {
					serverStatus.Tools = make([]McpToolInfo, 0, len(tools))
					for _, tool := range tools {
						if toolMap, ok := tool.(map[string]interface{}); ok {
							toolInfo := McpToolInfo{
								Name:        toString(toolMap["name"]),
								Description: toString(toolMap["description"]),
							}
							// Parse annotations if present
							if annotations, ok := toolMap["annotations"].(map[string]interface{}); ok {
								toolInfo.Annotations = &McpToolAnnotations{}
								if readOnly, ok := annotations["readOnly"].(bool); ok {
									toolInfo.Annotations.ReadOnly = readOnly
								}
								if destructive, ok := annotations["destructive"].(bool); ok {
									toolInfo.Annotations.Destructive = destructive
								}
								if openWorld, ok := annotations["openWorld"].(bool); ok {
									toolInfo.Annotations.OpenWorld = openWorld
								}
							}
							serverStatus.Tools = append(serverStatus.Tools, toolInfo)
						}
					}
				}

				// Parse config if present
				if config, ok := serverMap["config"].(map[string]interface{}); ok {
					serverStatus.Config = config
				}

				response.MCPServers = append(response.MCPServers, serverStatus)
			}
		}
	}

	return response, nil
}

// GetContextUsage returns a breakdown of current context window usage by category.
// Returns the same data shown by the `/context` command in the CLI,
// including token counts per category, total usage, and detailed
// breakdowns of MCP tools, memory files, and agents.
func (c *ClaudeSDKClient) GetContextUsage(ctx context.Context) (*ContextUsageResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return nil, NewCLIConnectionError("not connected", nil)
	}

	raw, err := c.query.GetContextUsage(ctx)
	if err != nil {
		return nil, err
	}

	// Marshal raw map back to JSON, then unmarshal into typed struct
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal context usage response: %w", err)
	}

	var resp ContextUsageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse context usage response: %w", err)
	}

	return &resp, nil
}

// toString is a helper to convert interface{} to string.
func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// GetServerInfo returns information about the connected server.
func (c *ClaudeSDKClient) GetServerInfo() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.query == nil {
		return nil
	}

	return c.query.GetServerInfo()
}

// Close disconnects and cleans up resources.
func (c *ClaudeSDKClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false

	if c.query != nil {
		c.query.Close()
	}

	if c.materialized != nil {
		if cleanup := c.materialized.Cleanup; cleanup != nil {
			_ = cleanup()
		}
		c.materialized = nil
	}

	return nil
}
