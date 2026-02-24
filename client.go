package claude

import (
	"context"
	"encoding/json"
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

	// Convert options
	transportOpts := convertToTransportOptions(c.options)

	var promptArg interface{}
	if len(prompt) > 0 {
		promptArg = prompt[0]
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
			publicCtx := ToolPermissionContext{}
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

// GetMCPStatus returns the current MCP server connection status.
func (c *ClaudeSDKClient) GetMCPStatus(ctx context.Context) (map[string]interface{}, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected {
		return nil, NewCLIConnectionError("not connected", nil)
	}

	return c.query.GetMCPStatus(ctx)
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

	return nil
}
