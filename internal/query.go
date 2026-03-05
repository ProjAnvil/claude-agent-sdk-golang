package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// Query handles bidirectional control protocol on top of Transport.
type Query struct {
	transport         transport.Transport
	isStreamingMode   bool
	canUseTool        CanUseToolFunc
	hooks             map[string][]HookMatcherInternal
	sdkMCPServers     map[string]*MCPServer
	agents            map[string]interface{}
	initializeTimeout time.Duration

	// Control protocol state
	pendingResponses map[string]chan controlResult
	hookCallbacks    map[string]HookCallback
	nextCallbackID   int
	requestCounter   int

	// Message channels - raw JSON data
	rawMessages chan map[string]interface{}
	errors      chan error

	// State
	mu                   sync.Mutex
	initialized          bool
	closed               bool
	initializationResult map[string]interface{}
	firstResultReceived  bool
	firstResultCh        chan struct{}
}

// CanUseToolFunc is the callback type for tool permission requests.
type CanUseToolFunc func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error)

// ToolPermissionContext provides context for tool permission callbacks.
type ToolPermissionContext struct {
	Signal      interface{}
	Suggestions []PermissionUpdate
}

// PermissionResult is the interface for permission results.
type PermissionResult interface {
	IsAllow() bool
}

// PermissionResultAllow allows the tool execution.
type PermissionResultAllow struct {
	UpdatedInput       map[string]interface{}
	UpdatedPermissions []PermissionUpdate
}

func (r *PermissionResultAllow) IsAllow() bool { return true }

// PermissionResultDeny denies the tool execution.
type PermissionResultDeny struct {
	Message   string
	Interrupt bool
}

func (r *PermissionResultDeny) IsAllow() bool { return false }

// PermissionUpdate represents a permission update.
type PermissionUpdate struct {
	Type string
}

// HookMatcherInternal is the internal representation of a hook matcher.
type HookMatcherInternal struct {
	Matcher string
	Hooks   []HookCallback
	Timeout time.Duration
}

// HookCallback is the function signature for hook handlers.
type HookCallback func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error)

// HookContext provides context for hook callbacks.
type HookContext struct {
	Signal interface{}
}

// HookInput contains data passed to hook callbacks.
type HookInput struct {
	HookEventName         string
	SessionID             string
	TranscriptPath        string
	CWD                   string
	PermissionMode        string
	ToolName              string
	ToolInput             map[string]interface{}
	ToolResponse          interface{}
	ToolUseID             string
	Error                 string
	IsInterrupt           bool
	Prompt                string
	StopHookActive        bool
	AgentID               string
	AgentTranscriptPath   string
	AgentType             string
	Trigger               string
	CustomInstructions    string
	Message               string
	Title                 string
	NotificationType      string
	PermissionSuggestions []interface{}
}

// HookOutput defines the response from a hook callback.
type HookOutput struct {
	Continue           *bool
	Async              bool
	AsyncTimeout       int
	SuppressOutput     bool
	StopReason         string
	Decision           string
	SystemMessage      string
	Reason             string
	HookSpecificOutput map[string]interface{}
}

type controlResult struct {
	response map[string]interface{}
	err      error
}

// QueryConfig configures a Query instance.
type QueryConfig struct {
	Transport         transport.Transport
	IsStreamingMode   bool
	CanUseTool        CanUseToolFunc
	Hooks             map[string][]HookMatcherInternal
	SdkMCPServers     map[string]*MCPServer
	Agents            map[string]interface{}
	InitializeTimeout time.Duration
}

// NewQuery creates a new Query instance.
func NewQuery(cfg QueryConfig) *Query {
	if cfg.InitializeTimeout == 0 {
		cfg.InitializeTimeout = 60 * time.Second
	}

	return &Query{
		transport:         cfg.Transport,
		isStreamingMode:   cfg.IsStreamingMode,
		canUseTool:        cfg.CanUseTool,
		hooks:             cfg.Hooks,
		sdkMCPServers:     cfg.SdkMCPServers,
		agents:            cfg.Agents,
		initializeTimeout: cfg.InitializeTimeout,
		pendingResponses:  make(map[string]chan controlResult),
		hookCallbacks:     make(map[string]HookCallback),
		rawMessages:       make(chan map[string]interface{}, 100),
		errors:            make(chan error, 10),
		firstResultCh:     make(chan struct{}),
	}
}

// Start begins reading messages from transport.
func (q *Query) Start() {
	go q.readMessages()
}

// Initialize performs the control protocol initialization handshake.
func (q *Query) Initialize(ctx context.Context) (map[string]interface{}, error) {
	if !q.isStreamingMode {
		return nil, nil
	}

	// Build hooks configuration
	hooksConfig := make(map[string]interface{})
	if q.hooks != nil {
		for event, matchers := range q.hooks {
			if len(matchers) > 0 {
				eventConfig := make([]map[string]interface{}, 0, len(matchers))
				for _, matcher := range matchers {
					callbackIDs := make([]string, len(matcher.Hooks))
					for i, callback := range matcher.Hooks {
						callbackID := fmt.Sprintf("hook_%d", q.nextCallbackID)
						q.nextCallbackID++
						q.hookCallbacks[callbackID] = callback
						callbackIDs[i] = callbackID
					}
					matcherConfig := map[string]interface{}{
						"matcher":         matcher.Matcher,
						"hookCallbackIds": callbackIDs,
					}
					if matcher.Timeout > 0 {
						matcherConfig["timeout"] = matcher.Timeout.Seconds()
					}
					eventConfig = append(eventConfig, matcherConfig)
				}
				hooksConfig[event] = eventConfig
			}
		}
	}

	request := map[string]interface{}{
		"subtype": "initialize",
	}
	if len(hooksConfig) > 0 {
		request["hooks"] = hooksConfig
	}
	if q.agents != nil {
		request["agents"] = q.agents
	}

	ctx, cancel := context.WithTimeout(ctx, q.initializeTimeout)
	defer cancel()

	response, err := q.sendControlRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	q.mu.Lock()
	q.initialized = true
	q.initializationResult = response
	q.mu.Unlock()

	return response, nil
}

// GetServerInfo returns the initialization result containing server info.
func (q *Query) GetServerInfo() map[string]interface{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.initializationResult
}

// GetMCPStatus returns the current MCP server connection status.
func (q *Query) GetMCPStatus(ctx context.Context) (map[string]interface{}, error) {
	return q.sendControlRequest(ctx, map[string]interface{}{
		"subtype": "mcp_status",
	})
}

// Interrupt sends an interrupt control request.
func (q *Query) Interrupt(ctx context.Context) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype": "interrupt",
	})
	return err
}

// SetPermissionMode changes the permission mode.
func (q *Query) SetPermissionMode(ctx context.Context, mode string) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype": "set_permission_mode",
		"mode":    mode,
	})
	return err
}

// SetModel changes the AI model.
func (q *Query) SetModel(ctx context.Context, model string) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype": "set_model",
		"model":   model,
	})
	return err
}

// RewindFiles rewinds tracked files to a specific user message.
func (q *Query) RewindFiles(ctx context.Context, userMessageID string) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype":         "rewind_files",
		"user_message_id": userMessageID,
	})
	return err
}

// ReconnectMCPServer reconnects to an MCP server.
func (q *Query) ReconnectMCPServer(ctx context.Context, serverName string) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype":    "mcp_reconnect",
		"serverName": serverName,
	})
	return err
}

// ToggleMCPServer enables or disables an MCP server.
func (q *Query) ToggleMCPServer(ctx context.Context, serverName string, enabled bool) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype":    "mcp_toggle",
		"serverName": serverName,
		"enabled":    enabled,
	})
	return err
}

// StopTask stops a running task.
func (q *Query) StopTask(ctx context.Context, taskID string) error {
	_, err := q.sendControlRequest(ctx, map[string]interface{}{
		"subtype": "stop_task",
		"task_id": taskID,
	})
	return err
}

// RawMessages returns the channel of raw JSON messages.
func (q *Query) RawMessages() <-chan map[string]interface{} {
	return q.rawMessages
}

// Errors returns the channel for errors.
func (q *Query) Errors() <-chan error {
	return q.errors
}

// Write sends data to the transport.
func (q *Query) Write(data string) error {
	return q.transport.Write(data)
}

// EndInput closes the input stream.
func (q *Query) EndInput() error {
	return q.transport.EndInput()
}

// Close closes the query and transport.
func (q *Query) Close() error {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()

	return q.transport.Close()
}

// readMessages reads from transport and routes messages.
func (q *Query) readMessages() {
	defer close(q.rawMessages)

	for data := range q.transport.ReadMessages() {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			break
		}
		q.mu.Unlock()

		msgType, _ := data["type"].(string)

		// Route control messages
		switch msgType {
		case "control_response":
			q.handleControlResponse(data)
			continue
		case "control_request":
			go q.handleControlRequest(data)
			continue
		case "control_cancel_request":
			continue
		}

		// Track results for proper stream closure
		if msgType == "result" {
			q.mu.Lock()
			if !q.firstResultReceived {
				q.firstResultReceived = true
				close(q.firstResultCh)
			}
			q.mu.Unlock()
		}

		// Send raw data to be parsed by caller
		q.mu.Lock()
		if !q.closed {
			q.rawMessages <- data
		}
		q.mu.Unlock()
	}

	// Forward transport errors
	for err := range q.transport.Errors() {
		q.errors <- err
	}
}

// handleControlResponse handles incoming control responses.
func (q *Query) handleControlResponse(data map[string]interface{}) {
	response, ok := data["response"].(map[string]interface{})
	if !ok {
		return
	}

	requestID, ok := response["request_id"].(string)
	if !ok {
		return
	}

	q.mu.Lock()
	ch, exists := q.pendingResponses[requestID]
	q.mu.Unlock()

	if !exists {
		return
	}

	subtype, _ := response["subtype"].(string)
	if subtype == "error" {
		errMsg, _ := response["error"].(string)
		ch <- controlResult{err: fmt.Errorf("%s", errMsg)}
	} else {
		respData, _ := response["response"].(map[string]interface{})
		ch <- controlResult{response: respData}
	}
}

// handleControlRequest handles incoming control requests from CLI.
func (q *Query) handleControlRequest(data map[string]interface{}) {
	requestID, _ := data["request_id"].(string)
	request, ok := data["request"].(map[string]interface{})
	if !ok {
		q.sendControlError(requestID, "invalid request format")
		return
	}

	subtype, _ := request["subtype"].(string)

	var responseData map[string]interface{}
	var err error

	switch subtype {
	case "can_use_tool":
		responseData, err = q.handleCanUseTool(request)
	case "hook_callback":
		responseData, err = q.handleHookCallback(request)
	case "mcp_message":
		responseData, err = q.handleMCPMessage(request)
	default:
		err = fmt.Errorf("unsupported control request subtype: %s", subtype)
	}

	if err != nil {
		q.sendControlError(requestID, err.Error())
		return
	}

	q.sendControlSuccess(requestID, responseData)
}

// handleCanUseTool handles tool permission requests.
func (q *Query) handleCanUseTool(request map[string]interface{}) (map[string]interface{}, error) {
	if q.canUseTool == nil {
		return nil, fmt.Errorf("canUseTool callback is not provided")
	}

	toolName, _ := request["tool_name"].(string)
	input, _ := request["input"].(map[string]interface{})
	suggestions, _ := request["permission_suggestions"].([]interface{})

	var permSuggestions []PermissionUpdate
	for _, s := range suggestions {
		if sMap, ok := s.(map[string]interface{}); ok {
			update := PermissionUpdate{}
			if t, ok := sMap["type"].(string); ok {
				update.Type = t
			}
			permSuggestions = append(permSuggestions, update)
		}
	}

	ctx := ToolPermissionContext{
		Suggestions: permSuggestions,
	}

	result, err := q.canUseTool(toolName, input, ctx)
	if err != nil {
		return nil, err
	}

	if result.IsAllow() {
		allow := result.(*PermissionResultAllow)
		response := map[string]interface{}{
			"behavior": "allow",
		}
		if allow.UpdatedInput != nil {
			response["updatedInput"] = allow.UpdatedInput
		} else {
			response["updatedInput"] = input
		}
		if allow.UpdatedPermissions != nil {
			response["updatedPermissions"] = allow.UpdatedPermissions
		}
		return response, nil
	}

	deny := result.(*PermissionResultDeny)
	response := map[string]interface{}{
		"behavior": "deny",
		"message":  deny.Message,
	}
	if deny.Interrupt {
		response["interrupt"] = true
	}
	return response, nil
}

// handleHookCallback handles hook callback requests.
func (q *Query) handleHookCallback(request map[string]interface{}) (map[string]interface{}, error) {
	callbackID, _ := request["callback_id"].(string)
	callback, exists := q.hookCallbacks[callbackID]
	if !exists {
		return nil, fmt.Errorf("no hook callback found for ID: %s", callbackID)
	}

	inputData, _ := request["input"].(map[string]interface{})
	toolUseID, _ := request["tool_use_id"].(string)

	hookInput := HookInput{}
	if eventName, ok := inputData["hook_event_name"].(string); ok {
		hookInput.HookEventName = eventName
	}
	if sessionID, ok := inputData["session_id"].(string); ok {
		hookInput.SessionID = sessionID
	}
	if transcriptPath, ok := inputData["transcript_path"].(string); ok {
		hookInput.TranscriptPath = transcriptPath
	}
	if cwd, ok := inputData["cwd"].(string); ok {
		hookInput.CWD = cwd
	}
	if toolName, ok := inputData["tool_name"].(string); ok {
		hookInput.ToolName = toolName
	}
	if toolInput, ok := inputData["tool_input"].(map[string]interface{}); ok {
		hookInput.ToolInput = toolInput
	}
	if toolResponse := inputData["tool_response"]; toolResponse != nil {
		hookInput.ToolResponse = toolResponse
	}
	if prompt, ok := inputData["prompt"].(string); ok {
		hookInput.Prompt = prompt
	}
	if permissionMode, ok := inputData["permission_mode"].(string); ok {
		hookInput.PermissionMode = permissionMode
	}
	if toolUseIDValue, ok := inputData["tool_use_id"].(string); ok {
		hookInput.ToolUseID = toolUseIDValue
	}
	if error, ok := inputData["error"].(string); ok {
		hookInput.Error = error
	}
	if isInterrupt, ok := inputData["is_interrupt"].(bool); ok {
		hookInput.IsInterrupt = isInterrupt
	}
	if stopHookActive, ok := inputData["stop_hook_active"].(bool); ok {
		hookInput.StopHookActive = stopHookActive
	}
	if agentID, ok := inputData["agent_id"].(string); ok {
		hookInput.AgentID = agentID
	}
	if agentTranscriptPath, ok := inputData["agent_transcript_path"].(string); ok {
		hookInput.AgentTranscriptPath = agentTranscriptPath
	}
	if agentType, ok := inputData["agent_type"].(string); ok {
		hookInput.AgentType = agentType
	}
	if trigger, ok := inputData["trigger"].(string); ok {
		hookInput.Trigger = trigger
	}
	if customInstructions, ok := inputData["custom_instructions"].(string); ok {
		hookInput.CustomInstructions = customInstructions
	}
	if message, ok := inputData["message"].(string); ok {
		hookInput.Message = message
	}
	if title, ok := inputData["title"].(string); ok {
		hookInput.Title = title
	}
	if notificationType, ok := inputData["notification_type"].(string); ok {
		hookInput.NotificationType = notificationType
	}
	if permissionSuggestions, ok := inputData["permission_suggestions"].([]interface{}); ok {
		hookInput.PermissionSuggestions = permissionSuggestions
	}

	ctx := HookContext{}

	output, err := callback(hookInput, toolUseID, ctx)
	if err != nil {
		return nil, err
	}

	response := make(map[string]interface{})
	if output.Continue != nil {
		response["continue"] = *output.Continue
	}
	if output.Async {
		response["async"] = true
	}
	if output.AsyncTimeout > 0 {
		response["asyncTimeout"] = output.AsyncTimeout
	}
	if output.SuppressOutput {
		response["suppressOutput"] = true
	}
	if output.StopReason != "" {
		response["stopReason"] = output.StopReason
	}
	if output.Decision != "" {
		response["decision"] = output.Decision
	}
	if output.SystemMessage != "" {
		response["systemMessage"] = output.SystemMessage
	}
	if output.Reason != "" {
		response["reason"] = output.Reason
	}
	if output.HookSpecificOutput != nil {
		response["hookSpecificOutput"] = output.HookSpecificOutput
	}

	return response, nil
}

// handleMCPMessage handles SDK MCP requests.
func (q *Query) handleMCPMessage(request map[string]interface{}) (map[string]interface{}, error) {
	serverName, _ := request["server_name"].(string)
	message, _ := request["message"].(map[string]interface{})

	if serverName == "" || message == nil {
		return nil, fmt.Errorf("missing server_name or message for MCP request")
	}

	server, exists := q.sdkMCPServers[serverName]
	if !exists {
		return map[string]interface{}{
			"mcp_response": map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      message["id"],
				"error": map[string]interface{}{
					"code":    -32601,
					"message": fmt.Sprintf("Server '%s' not found", serverName),
				},
			},
		}, nil
	}

	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]interface{})

	var mcpResponse map[string]interface{}

	switch method {
	case "initialize":
		mcpResponse = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    server.Name,
					"version": server.Version,
				},
			},
		}
	case "tools/list":
		tools := make([]map[string]interface{}, len(server.Tools))
		for i, tool := range server.Tools {
			tools[i] = map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
			}
			if tool.Annotations != nil {
				tools[i]["annotations"] = tool.Annotations
			}
		}
		mcpResponse = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"result": map[string]interface{}{
				"tools": tools,
			},
		}
	case "tools/call":
		toolName, _ := params["name"].(string)
		arguments, _ := params["arguments"].(map[string]interface{})

		var found bool
		for _, tool := range server.Tools {
			if tool.Name == toolName {
				found = true
				result, err := tool.Handler(arguments)
				if err != nil {
					mcpResponse = map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      message["id"],
						"error": map[string]interface{}{
							"code":    -32603,
							"message": err.Error(),
						},
					}
				} else {
					mcpResponse = map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      message["id"],
						"result":  result,
					}
				}
				break
			}
		}
		if !found {
			mcpResponse = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      message["id"],
				"error": map[string]interface{}{
					"code":    -32601,
					"message": fmt.Sprintf("Tool '%s' not found", toolName),
				},
			}
		}
	case "notifications/initialized":
		mcpResponse = map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  map[string]interface{}{},
		}
	default:
		mcpResponse = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"error": map[string]interface{}{
				"code":    -32601,
				"message": fmt.Sprintf("Method '%s' not found", method),
			},
		}
	}

	return map[string]interface{}{
		"mcp_response": mcpResponse,
	}, nil
}

// sendControlRequest sends a control request and waits for response.
func (q *Query) sendControlRequest(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if !q.isStreamingMode {
		return nil, fmt.Errorf("control requests require streaming mode")
	}

	q.mu.Lock()
	q.requestCounter++
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	requestID := fmt.Sprintf("req_%d_%s", q.requestCounter, hex.EncodeToString(randomBytes))

	responseCh := make(chan controlResult, 1)
	q.pendingResponses[requestID] = responseCh
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		delete(q.pendingResponses, requestID)
		q.mu.Unlock()
	}()

	controlRequest := map[string]interface{}{
		"type":       "control_request",
		"request_id": requestID,
		"request":    request,
	}

	data, err := json.Marshal(controlRequest)
	if err != nil {
		return nil, err
	}

	if err := q.transport.Write(string(data) + "\n"); err != nil {
		return nil, err
	}

	select {
	case result := <-responseCh:
		if result.err != nil {
			return nil, result.err
		}
		return result.response, nil
	case <-ctx.Done():
		subtype, _ := request["subtype"].(string)
		return nil, fmt.Errorf("control request timeout: %s", subtype)
	}
}

// sendControlSuccess sends a success control response.
func (q *Query) sendControlSuccess(requestID string, response map[string]interface{}) {
	controlResponse := map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		},
	}

	data, _ := json.Marshal(controlResponse)
	q.transport.Write(string(data) + "\n")
}

// sendControlError sends an error control response.
func (q *Query) sendControlError(requestID string, errMsg string) {
	controlResponse := map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "error",
			"request_id": requestID,
			"error":      errMsg,
		},
	}

	data, _ := json.Marshal(controlResponse)
	q.transport.Write(string(data) + "\n")
}

// MCPServer represents an SDK MCP server.
type MCPServer struct {
	Name    string
	Version string
	Tools   []MCPTool
}

// MCPTool represents an MCP tool.
type MCPTool struct {
	Name        string
	Description string
	InputSchema interface{}
	Annotations interface{}
	Handler     func(args map[string]interface{}) (map[string]interface{}, error)
}

// ToolAnnotations represents hints for tool usage.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}
