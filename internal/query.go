package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResultError is the internal representation of a CLI error result that
// terminated the run. It carries the result payload so the public package
// can rebuild a claude.ResultError from it (the internal package cannot
// import the public one). Cause holds the original transport.ProcessError,
// mirroring Python's `pending_error.__cause__ = e`.
type ResultError struct {
	Message  string
	Data     map[string]interface{}
	ExitCode int
	Cause    error
}

// Error formats like transport.ProcessError: the message with the exit code.
// stderr is deliberately not carried over: the transport's value is a
// generic placeholder, and the result text is the real cause.
func (e *ResultError) Error() string {
	msg := e.Message
	if e.ExitCode != 0 {
		msg = fmt.Sprintf("%s (exit code: %d)", msg, e.ExitCode)
	}
	return msg
}

// Unwrap returns the original transport error this one replaced.
func (e *ResultError) Unwrap() error { return e.Cause }

// normalizeResultErrors normalizes the "errors" field of a "result" frame to
// clean strings.
//
// The CLI emits a list of strings; tolerate a bare string (older/buggy
// emitters) and drop non-string or blank entries so the structured
// ResultError fields and the exception text always agree. Mirrors
// claude.normalizeResultErrors (duplicated here because the internal package
// cannot import the public one).
func normalizeResultErrors(raw interface{}) []string {
	var items []interface{}
	switch v := raw.(type) {
	case string:
		items = []interface{}{v}
	case []interface{}:
		items = v
	case []string:
		items = make([]interface{}, len(v))
		for i, s := range v {
			items[i] = s
		}
	default:
		return []string{}
	}
	errs := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				errs = append(errs, trimmed)
			}
		}
	}
	return errs
}

// errorResultText picks the most informative text from a "result" frame with
// is_error.
//
// Terminal errors the CLI raises itself (error_max_turns,
// error_during_execution, ...) carry their prose in errors[]. A run that
// ends on an API failure instead arrives as subtype "success" with
// is_error=true, an empty errors[] and the "API Error: ..." prose in
// result — falling back to the subtype there produced the self-contradictory
// "Claude Code returned an error result: success". Prefer errors[], then
// result, then a non-success subtype, then the HTTP status, mirroring the
// TypeScript SDK's choice of result for the success subtype.
func errorResultText(message map[string]interface{}) string {
	if errs := normalizeResultErrors(message["errors"]); len(errs) > 0 {
		return strings.Join(errs, "; ")
	}
	if result, ok := message["result"].(string); ok {
		if trimmed := strings.TrimSpace(result); trimmed != "" {
			return trimmed
		}
	}
	if subtype, ok := message["subtype"].(string); ok && subtype != "" && subtype != "success" {
		return subtype
	}
	switch status := message["api_error_status"].(type) {
	case float64:
		return fmt.Sprintf("API error (HTTP %v)", status)
	case int:
		return fmt.Sprintf("API error (HTTP %d)", status)
	}
	return "unknown error"
}

// Query handles bidirectional control protocol on top of Transport.
type Query struct {
	transport              transport.Transport
	isStreamingMode        bool
	canUseTool             CanUseToolFunc
	hooks                  map[string][]HookMatcherInternal
	sdkMCPServers          map[string]*mcp.Server
	agents                 map[string]interface{}
	excludeDynamicSections *bool
	initializeTimeout      time.Duration
	// skills is the skills allowlist (nil, "all", or []string).
	// When it is a []string, the names are sent in the initialize request.
	skills interface{}
	// forwardSubagentText asks the CLI (via initialize) to forward subagent
	// text/thinking blocks, not just tool_use/tool_result.
	forwardSubagentText bool
	// mirrorBatcher handles transcript_mirror frames from the CLI.
	mirrorBatcher TranscriptMirrorBatcher

	// SDK MCP bridges, one per server name, created lazily by
	// handleMCPMessage and closed with the Query (bridge sessions own
	// goroutines).
	bridgeMu   sync.Mutex
	mcpBridges map[string]*SDKMCPBridge

	// Control protocol state
	pendingResponses map[string]chan controlResult
	hookCallbacks    map[string]HookCallback
	inflightRequests map[string]context.CancelFunc
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
	// lastErrorResult is set to the result payload when the most recent
	// message is a result with is_error=true. Used to replace the generic
	// "exit code 1" ProcessError with a ResultError carrying what the CLI
	// already reported. Mirrors the TypeScript SDK's lastErrorResultText
	// (Query.ts), but keeps the whole payload rather than just the text.
	lastErrorResult map[string]interface{}
}

// CanUseToolFunc is the callback type for tool permission requests.
type CanUseToolFunc func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error)

// ToolPermissionContext provides context for tool permission callbacks.
type ToolPermissionContext struct {
	Signal         interface{}
	Suggestions    []PermissionUpdate
	ToolUseID      string
	AgentID        string
	BlockedPath    string
	DecisionReason string
	Title          string
	DisplayName    string
	Description    string
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
	Type        string
	Rules       []PermissionRuleValue
	Behavior    string
	Mode        string
	Directories []string
	Destination string
}

// PermissionRuleValue represents a permission rule.
type PermissionRuleValue struct {
	ToolName    string
	RuleContent string
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
	PermissionSuggestions []map[string]interface{}
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

// TranscriptMirrorBatcher handles asynchronous forwarding of transcript_mirror
// frames to a session store.
type TranscriptMirrorBatcher interface {
	// Enqueue schedules (filePath, entries) for delivery to the session store.
	Enqueue(filePath string, entries []map[string]interface{})
	// Flush waits for all currently-enqueued items to be delivered.
	Flush(ctx context.Context) error
	// Close flushes and shuts down the batcher.
	Close(ctx context.Context) error
}

// QueryConfig configures a Query instance.
type QueryConfig struct {
	Transport              transport.Transport
	IsStreamingMode        bool
	CanUseTool             CanUseToolFunc
	Hooks                  map[string][]HookMatcherInternal
	SdkMCPServers          map[string]*mcp.Server
	Agents                 map[string]interface{}
	ExcludeDynamicSections *bool
	InitializeTimeout      time.Duration
	// Skills is the skills allowlist passed to the initialize request.
	// Accepted: nil, "all", or []string{names...}.
	Skills interface{}
	// ForwardSubagentText asks the CLI (via initialize) to forward subagent
	// text/thinking blocks, not just tool_use/tool_result.
	ForwardSubagentText bool
	// MirrorBatcher handles transcript_mirror frames from the CLI.
	// When non-nil, transcript_mirror messages are peeled off the stream and
	// forwarded to the batcher rather than being yielded to callers.
	MirrorBatcher TranscriptMirrorBatcher
}

// NewQuery creates a new Query instance.
func NewQuery(cfg QueryConfig) *Query {
	if cfg.InitializeTimeout == 0 {
		cfg.InitializeTimeout = 60 * time.Second
	}

	return &Query{
		transport:              cfg.Transport,
		isStreamingMode:        cfg.IsStreamingMode,
		canUseTool:             cfg.CanUseTool,
		hooks:                  cfg.Hooks,
		sdkMCPServers:          cfg.SdkMCPServers,
		agents:                 cfg.Agents,
		excludeDynamicSections: cfg.ExcludeDynamicSections,
		initializeTimeout:      cfg.InitializeTimeout,
		skills:                 cfg.Skills,
		forwardSubagentText:    cfg.ForwardSubagentText,
		mirrorBatcher:          cfg.MirrorBatcher,
		pendingResponses:       make(map[string]chan controlResult),
		hookCallbacks:          make(map[string]HookCallback),
		inflightRequests:       make(map[string]context.CancelFunc),
		rawMessages:            make(chan map[string]interface{}, 100),
		errors:                 make(chan error, 10),
		firstResultCh:          make(chan struct{}),
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
	if q.excludeDynamicSections != nil {
		request["excludeDynamicSections"] = *q.excludeDynamicSections
	}
	// Skills list is sent only when it's a concrete []string (not nil or "all").
	if skillsList, ok := q.skills.([]string); ok && skillsList != nil {
		request["skills"] = skillsList
	}
	if q.forwardSubagentText {
		request["forwardSubagentText"] = true
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

// GetContextUsage returns a breakdown of current context window usage by category.
func (q *Query) GetContextUsage(ctx context.Context) (map[string]interface{}, error) {
	return q.sendControlRequest(ctx, map[string]interface{}{
		"subtype": "get_context_usage",
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

	// Close (and flush) mirror batcher before closing the transport.
	if q.mirrorBatcher != nil {
		_ = q.mirrorBatcher.Close(context.Background())
	}

	// Stop the SDK MCP bridge sessions: they own goroutines that must end
	// with the Query.
	q.closeMCPBridges()

	return q.transport.Close()
}

// bridgeForServer returns the bridge for a configured SDK MCP server,
// creating it on first use. Bridges live in the Query's registry so Close
// can stop their sessions.
func (q *Query) bridgeForServer(name string, server *mcp.Server) *SDKMCPBridge {
	q.bridgeMu.Lock()
	defer q.bridgeMu.Unlock()
	if q.mcpBridges == nil {
		q.mcpBridges = make(map[string]*SDKMCPBridge)
	}
	if bridge, ok := q.mcpBridges[name]; ok {
		return bridge
	}
	bridge := NewSDKMCPBridge(name, server)
	q.mcpBridges[name] = bridge
	return bridge
}

// closeMCPBridges stops every bridge the Query created.
func (q *Query) closeMCPBridges() {
	q.bridgeMu.Lock()
	bridges := q.mcpBridges
	q.mcpBridges = nil
	q.bridgeMu.Unlock()
	for _, bridge := range bridges {
		bridge.Close()
	}
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
			go q.spawnControlRequestHandler(data)
			continue
		case "control_cancel_request":
			cancelID, _ := data["request_id"].(string)
			if cancelID != "" {
				q.mu.Lock()
				if cancel, ok := q.inflightRequests[cancelID]; ok {
					cancel()
					delete(q.inflightRequests, cancelID)
				}
				q.mu.Unlock()
			}
			continue
		}

		// transcript_mirror frames are consumed here and forwarded to the
		// batcher rather than being surfaced to callers.
		if msgType == "transcript_mirror" {
			if q.mirrorBatcher != nil {
				filePath, _ := data["file_path"].(string)
				var entries []map[string]interface{}
				if raw, ok := data["entries"].([]interface{}); ok {
					for _, e := range raw {
						if m, ok := e.(map[string]interface{}); ok {
							entries = append(entries, m)
						}
					}
				}
				if filePath != "" && len(entries) > 0 {
					q.mirrorBatcher.Enqueue(filePath, entries)
				}
			}
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
			// Flush mirror batcher before yielding result so all prior frames
			// have been persisted when callers observe the result message.
			if q.mirrorBatcher != nil {
				_ = q.mirrorBatcher.Flush(context.Background())
			}
			// Track error result payloads for actionable ProcessError
			// replacement.
			if isError, _ := data["is_error"].(bool); isError {
				q.lastErrorResult = data
			} else {
				q.lastErrorResult = nil
			}
		} else if !(msgType == "system" && data["subtype"] == "session_state_changed") {
			// Anything other than the post-turn session_state_changed
			// marker means the conversation moved on; a ProcessError
			// now is a fresh crash, not the expected exit from a prior
			// error result. Mirrors the TypeScript SDK's reset logic.
			q.lastErrorResult = nil
		}

		// Send raw data to be parsed by caller
		q.mu.Lock()
		closed := q.closed
		q.mu.Unlock()
		if !closed {
			q.rawMessages <- data
		}
	}

	// Forward transport errors (non-blocking to prevent deadlock
	// when the consumer goroutine has stopped reading).
	for err := range q.transport.Errors() {
		// When the CLI emits a result with is_error=true (e.g.
		// error_max_turns, error_during_execution, or an API failure) it
		// then exits non-zero on purpose, for shell-script consumers. The
		// trailing ProcessError carries no information beyond "exit code
		// 1" — replace it with a ResultError carrying what the CLI already
		// reported so the exception is actionable and typed. Mirrors the
		// TypeScript SDK (Query.ts readMessages).
		pendingErr := err
		if q.lastErrorResult != nil {
			if pe, ok := err.(*transport.ProcessError); ok {
				pendingErr = &ResultError{
					Message: fmt.Sprintf("Claude Code returned an error result: %s",
						errorResultText(q.lastErrorResult)),
					Data:     q.lastErrorResult,
					ExitCode: pe.ExitCode,
					Cause:    err,
				}
			}
		}
		// Signal all pending control requests so they fail fast instead of
		// timing out. This includes an initialize still in flight when the
		// CLI reports an error result during startup (e.g. a refused
		// resume), so that path sees the same actionable error.
		q.mu.Lock()
		for requestID, ch := range q.pendingResponses {
			select {
			case ch <- controlResult{err: pendingErr}:
			default:
			}
			delete(q.pendingResponses, requestID)
		}
		q.mu.Unlock()
		select {
		case q.errors <- pendingErr:
		default:
		}
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

// spawnControlRequestHandler spawns a control request handler and tracks it for cancellation.
func (q *Query) spawnControlRequestHandler(data map[string]interface{}) {
	reqID, _ := data["request_id"].(string)
	ctx, cancel := context.WithCancel(context.Background())

	q.mu.Lock()
	q.inflightRequests[reqID] = cancel
	q.mu.Unlock()

	go func() {
		defer func() {
			q.mu.Lock()
			delete(q.inflightRequests, reqID)
			q.mu.Unlock()
			cancel()
		}()
		q.handleControlRequest(ctx, data)
	}()
}

// handleControlRequest handles incoming control requests from CLI.
func (q *Query) handleControlRequest(ctx context.Context, data map[string]interface{}) {
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
		responseData, err = q.handleCanUseTool(ctx, request)
	case "hook_callback":
		responseData, err = q.handleHookCallback(ctx, request)
	case "mcp_message":
		responseData, err = q.handleMCPMessage(request)
	default:
		err = fmt.Errorf("unsupported control request subtype: %s", subtype)
	}

	// If the context was cancelled (via control_cancel_request), don't send
	// a response — the CLI already knows the request was abandoned.
	if ctx.Err() != nil {
		return
	}

	if err != nil {
		q.sendControlError(requestID, err.Error())
		return
	}

	q.sendControlSuccess(requestID, responseData)
}

// permissionUpdateFromMap constructs a PermissionUpdate from the control protocol dict.
func permissionUpdateFromMap(data map[string]interface{}) PermissionUpdate {
	update := PermissionUpdate{}
	if t, ok := data["type"].(string); ok {
		update.Type = t
	}
	if behavior, ok := data["behavior"].(string); ok {
		update.Behavior = behavior
	}
	if mode, ok := data["mode"].(string); ok {
		update.Mode = mode
	}
	if dest, ok := data["destination"].(string); ok {
		update.Destination = dest
	}
	if dirs, ok := data["directories"].([]interface{}); ok {
		for _, d := range dirs {
			if s, ok := d.(string); ok {
				update.Directories = append(update.Directories, s)
			}
		}
	}
	if rules, ok := data["rules"].([]interface{}); ok {
		for _, r := range rules {
			if rMap, ok := r.(map[string]interface{}); ok {
				rule := PermissionRuleValue{}
				if tn, ok := rMap["toolName"].(string); ok {
					rule.ToolName = tn
				}
				if rc, ok := rMap["ruleContent"].(string); ok {
					rule.RuleContent = rc
				}
				update.Rules = append(update.Rules, rule)
			}
		}
	}
	return update
}

// handleCanUseTool handles tool permission requests.
func (q *Query) handleCanUseTool(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if q.canUseTool == nil {
		return nil, fmt.Errorf("canUseTool callback is not provided")
	}

	toolName, _ := request["tool_name"].(string)
	input, _ := request["input"].(map[string]interface{})
	suggestions, _ := request["permission_suggestions"].([]interface{})
	toolUseID, _ := request["tool_use_id"].(string)
	agentID, _ := request["agent_id"].(string)
	blockedPath, _ := request["blocked_path"].(string)
	decisionReason, _ := request["decision_reason"].(string)
	title, _ := request["title"].(string)
	displayName, _ := request["display_name"].(string)
	description, _ := request["description"].(string)

	var permSuggestions []PermissionUpdate
	for _, s := range suggestions {
		if sMap, ok := s.(map[string]interface{}); ok {
			permSuggestions = append(permSuggestions, permissionUpdateFromMap(sMap))
		}
	}

	permCtx := ToolPermissionContext{
		Suggestions:    permSuggestions,
		ToolUseID:      toolUseID,
		AgentID:        agentID,
		BlockedPath:    blockedPath,
		DecisionReason: decisionReason,
		Title:          title,
		DisplayName:    displayName,
		Description:    description,
	}

	result, err := q.canUseTool(toolName, input, permCtx)
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
func (q *Query) handleHookCallback(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
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
		for _, s := range permissionSuggestions {
			if m, ok := s.(map[string]interface{}); ok {
				hookInput.PermissionSuggestions = append(hookInput.PermissionSuggestions, m)
			}
		}
	}

	hookCtx := HookContext{}

	output, err := callback(hookInput, toolUseID, hookCtx)
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

	// The JSON-RPC dispatch itself lives in sdk_mcp_bridge.go, which mirrors
	// the wire semantics of Python's SdkMcpBridge (#1218); bridge sessions
	// own goroutines and are closed with the Query.
	mcpResponse := q.bridgeForServer(serverName, server).Handle(message)
	if mcpResponse == nil {
		// JSON-RPC notifications get no reply, but the control request that
		// carried one still expects an ack.
		mcpResponse = map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  map[string]interface{}{},
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
