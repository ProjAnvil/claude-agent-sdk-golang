package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
// or it will hang the query (see Query.trackSystemFrame).
var deferringTaskTypes = map[string]bool{
	"local_agent":    true,
	"local_workflow": true,
}

// terminalTaskStatuses is the set of task statuses that mean the task has
// finished and should be cleared from the in-flight task ledger. Duplicated
// from the public TerminalTaskStatuses (types.go) because the internal
// package cannot import the public one (same pattern as normalizeResultErrors).
var terminalTaskStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"stopped":   true,
	"killed":    true,
}

const (
	// runEndCeilingEnv is the CLI's own background-wait ceiling once stdin is
	// closed; the SDK bounds its wait for the CLI's "idle" by the same value
	// (Query.armRunEndCeiling).
	runEndCeilingEnv = "CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS"
	// DefaultRunEndCeilingMS is the CLI's default background-wait ceiling
	// (10 minutes).
	DefaultRunEndCeilingMS = 600_000
	// maxRunEndCeilingMS is the longest ceiling honored (~24.8 days), as in
	// the TypeScript SDK, whose timers cannot run longer; it also keeps a
	// huge value safe to convert to a duration.
	maxRunEndCeilingMS = 1<<31 - 1
	// maxTrackedTasks caps the in-flight task ledger (trackSystemFrame).
	//
	// Divergence from Python, deliberate: Python's _inflight_tasks is an
	// unbounded set (query.py:267), so a CLI emitting unbounded task_started
	// frames of a deferring type (local_agent/local_workflow) with no
	// terminal frame grows memory without bound AND keeps the run from ever
	// ending — both _maybe_end_run and the run-end ceiling explicitly stand
	// down while the ledger is non-empty (query.py:940-957, 1011-1033). The
	// cap is defensive: past it the ledger is unreliable, so the run may end
	// early rather than never (see taskLedgerOverflowed).
	maxTrackedTasks = 10_000
)

// RunEndCeilingMS reads CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS as the CLI will
// see it. optionsEnv (ClaudeAgentOptions.Env) overrides the inherited
// environment, as it does for the CLI subprocess. 0 means no limit; anything
// that is not a plain non-negative integer falls back to the CLI's default of
// 10 minutes (the CLI itself also reads spellings such as 1e6). Mirrors
// Python's run_end_ceiling_ms (_internal/query.py).
func RunEndCeilingMS(optionsEnv map[string]string) int {
	raw, ok := optionsEnv[runEndCeilingEnv]
	if !ok {
		raw = os.Getenv(runEndCeilingEnv)
	}
	if raw == "" {
		return DefaultRunEndCeilingMS
	}
	// Python's int() tolerates surrounding whitespace; strconv does not.
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultRunEndCeilingMS
	}
	if value < 0 {
		return DefaultRunEndCeilingMS
	}
	return value
}

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
	systemPromptSnapshot   *bool
	initializeTimeout      time.Duration
	// skills is the skills allowlist (nil, "all", or []string).
	// When it is a []string, the names are sent in the initialize request.
	skills interface{}
	// forwardSubagentText asks the CLI (via initialize) to forward subagent
	// text/thinking blocks, not just tool_use/tool_result.
	forwardSubagentText bool
	// verbatimPrompts marks every outgoing user message client_composed so
	// the CLI delivers it as written (see transport.StampUserMessage).
	verbatimPrompts bool
	// runEndCeilingMS bounds the wait for the CLI's "idle" past a result
	// with no new turn; <= 0 means no ceiling (see RunEndCeilingMS).
	runEndCeilingMS int
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
	mu          sync.Mutex
	initialized bool
	closed      bool
	// closedCh is closed by Close (exactly once; the closed flag under mu
	// gates it) so a read loop parked on a full rawMessages channel is
	// released instead of leaking — Python's close() cancels and reaps the
	// reader (query.py:1194-1196). Assigned in NewQuery before any goroutine
	// starts; only closed thereafter, so receiving needs no lock.
	closedCh             chan struct{}
	initializationResult map[string]interface{}
	firstResultReceived  bool
	firstResultCh        chan struct{}
	// lastErrorResult is set to the result payload when the most recent
	// message is a result with is_error=true. Used to replace the generic
	// "exit code 1" ProcessError with a ResultError carrying what the CLI
	// already reported. Mirrors the TypeScript SDK's lastErrorResultText
	// (Query.ts), but keeps the whole payload rather than just the text.
	lastErrorResult map[string]interface{}

	// Run-end state machine (#1190). All fields below are guarded by mu.
	//
	// resultReceived is set by the first result frame of the current run;
	// StreamInput clears it per prompt (each prompt owes a run of its own).
	resultReceived bool
	// sessionState is the CLI's latest session_state_changed state, or nil
	// while it sends none (a CLI too old to honor
	// CLAUDE_CODE_SDK_READS_SESSION_STATE). A CLI that reports state stays
	// "running" while a background agent is live or its completion is still
	// to be handled, and reports "idle" once no further turn is owed.
	sessionState *string
	// turnInProgress tracks whether a main-thread turn is under way (its
	// assistant/stream_event frames have started and its result has not
	// arrived): the ceiling counts only the wait between turns, so it is not
	// armed meanwhile.
	turnInProgress bool
	// runFinal is set once stdin is closed or the reader is gone: the run
	// then stays ended, since nothing can wait on a reopened one.
	runFinal bool
	// runEndedCh is closed when the run is over, so the stdin-closing
	// waiter can wake; runEnded mirrors the channel's closed state under mu.
	// Work the CLI takes up after the run ended swaps in a fresh channel
	// (reopenRun) — Go's replacement for anyio's resettable Event.
	runEndedCh chan struct{}
	runEnded   bool
	// ceilingTimer ends the run if no new turn starts within the ceiling
	// after a result; ceilingGeneration tells a timer that fired after it
	// was cleared or re-armed to stand down.
	ceilingTimer      *time.Timer
	ceilingGeneration int
	// inflightTasks holds task IDs of started-but-not-finished tasks. A
	// result frame only ends one turn, not the run: background tasks keep
	// running past it and still need stdin for hook/SDK-MCP control
	// responses (see #1088), so a result that arrives while this set is
	// non-empty must not close stdin.
	inflightTasks map[string]bool
	// taskLedgerOverflowed is set once inflightTasks hits maxTrackedTasks
	// and stopped tracking new tasks (trackSystemFrame). The ledger is then
	// an under-count, so maybeEndRun and endRunAtCeiling no longer stand
	// down on a non-empty one: the run may end while uncounted tasks are in
	// flight, the alternative the cap exists to prevent (a hostile CLI
	// withholding the run-end forever). Terminal frames still remove tracked
	// entries normally. Guarded by mu.
	taskLedgerOverflowed bool
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
	SystemPromptSnapshot   *bool
	InitializeTimeout      time.Duration
	// Skills is the skills allowlist passed to the initialize request.
	// Accepted: nil, "all", or []string{names...}.
	Skills interface{}
	// ForwardSubagentText asks the CLI (via initialize) to forward subagent
	// text/thinking blocks, not just tool_use/tool_result.
	ForwardSubagentText bool
	// VerbatimPrompts marks every outgoing user message client_composed so
	// the CLI delivers it as written (see transport.StampUserMessage).
	VerbatimPrompts bool
	// RunEndCeilingMS bounds the wait for the CLI's "idle" past a result
	// with no new turn (0 here means "unset" and becomes
	// DefaultRunEndCeilingMS in NewQuery; a negative value disables the
	// ceiling entirely, mirroring Python's explicit run_end_ceiling_ms=0).
	RunEndCeilingMS int
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
	// 0 means "not configured" (the zero value of QueryConfig); the public
	// wiring computes the real value via RunEndCeilingMS and translates the
	// env's "0 = no ceiling" to a negative so it survives this default.
	// Any value <= 0 disables the ceiling in armRunEndCeiling, as in Python.
	if cfg.RunEndCeilingMS == 0 {
		cfg.RunEndCeilingMS = DefaultRunEndCeilingMS
	}

	return &Query{
		transport:              cfg.Transport,
		isStreamingMode:        cfg.IsStreamingMode,
		canUseTool:             cfg.CanUseTool,
		hooks:                  cfg.Hooks,
		sdkMCPServers:          cfg.SdkMCPServers,
		agents:                 cfg.Agents,
		excludeDynamicSections: cfg.ExcludeDynamicSections,
		systemPromptSnapshot:   cfg.SystemPromptSnapshot,
		initializeTimeout:      cfg.InitializeTimeout,
		skills:                 cfg.Skills,
		forwardSubagentText:    cfg.ForwardSubagentText,
		verbatimPrompts:        cfg.VerbatimPrompts,
		runEndCeilingMS:        cfg.RunEndCeilingMS,
		mirrorBatcher:          cfg.MirrorBatcher,
		pendingResponses:       make(map[string]chan controlResult),
		hookCallbacks:          make(map[string]HookCallback),
		inflightRequests:       make(map[string]context.CancelFunc),
		rawMessages:            make(chan map[string]interface{}, 100),
		errors:                 make(chan error, 10),
		firstResultCh:          make(chan struct{}),
		inflightTasks:          make(map[string]bool),
		runEndedCh:             make(chan struct{}),
		closedCh:               make(chan struct{}),
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
		// Registration is guarded by q.mu: the read loop is already running
		// (Start precedes Initialize) and handleHookCallback looks callbacks
		// up under the same lock — Python gets the same guarantee from
		// asyncio's single thread (its dict population is synchronous before
		// the initialize request is written, query.py:319-337). q.mu stays a
		// leaf lock here: nothing in the loop takes it again.
		q.mu.Lock()
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
		q.mu.Unlock()
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
	if q.systemPromptSnapshot != nil {
		request["systemPromptSnapshot"] = *q.systemPromptSnapshot
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
	wasClosed := q.closed
	q.closed = true
	// Release a read loop parked on a full rawMessages channel (see the
	// send in readMessages): without this, an abandoned query leaks its
	// reader goroutine. Gated by the closed flag so the channel closes
	// exactly once even though Close itself is idempotent.
	if !wasClosed {
		close(q.closedCh)
	}
	// Stand down the run-end ceiling (Python's close cancels the child task
	// that sleeps on it) so no timer outlives the query.
	q.clearRunEndCeiling()
	// Ending the run also wakes any WaitForResultAndEndInput / StreamInput
	// waiter (Python's close cancels its child tasks): without this, an
	// abandoned query whose reader is stuck mid-forward would leave the
	// waiter parked on runEndedCh forever. EndInput on a closed transport
	// is a harmless no-op.
	q.runFinal = true
	q.endRun()
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

// ---------------------------------------------------------------------------
// Run-end state machine (#1190)
//
// With hooks, SDK MCP servers or a can_use_tool callback configured, stdin
// must outlive a result frame: follow-up turns (a finished background task
// waking the session, an answered permission request) still need it for their
// control responses. The run ends at the CLI's "idle" session state after a
// result (or the first result from a CLI that reports no state), bounded
// between turns by the run-end ceiling. Every method below requires q.mu to
// be held, except the exported entry points that acquire it themselves.
// ---------------------------------------------------------------------------

// trackSystemFrame consumes one raw "system" frame, updating the in-flight
// task set so result frames can tell "one turn ended" apart from "the run is
// done" (see #1088).
//
// task_started marks a task in flight; task_notification or a task_updated
// patch with a terminal status clears it. Terminal completion can arrive as
// either frame (not every terminal task emits a notification), so both are
// handled; deleting from a map keeps the pair idempotent.
//
// This is a mitigation, not a complete answer to #1088: a task that settles
// *before* the turn's result frame leaves the set empty at that result, so
// the ledger alone would close stdin even though the completion may still
// wake the parent for a continuation turn — that gap is what the
// session-state run boundary closes.
//
// Only delegated agent work is tracked (deferringTaskTypes). A background
// *shell* — Bash(run_in_background=true) on a dev server or tail -f — is also
// reported through these frames, but it may never reach a terminal status,
// and the CLI in stream-json mode only exits on stdin EOF, so tracking one
// would withhold the close forever rather than briefly.
//
// background_tasks_changed is deliberately *not* consumed, in either
// direction: its payload is the live *background* set, while a subagent is
// registered in the foreground and only flips to backgrounded later, without
// a second task_started. The lifecycle frames are the only self-consistent
// source here (see #1088).
//
// Caller must hold q.mu.
func (q *Query) trackSystemFrame(message map[string]interface{}) {
	subtype, _ := message["subtype"].(string)
	taskID, _ := message["task_id"].(string)
	if taskID == "" {
		return
	}
	switch subtype {
	case "task_started":
		if taskType, _ := message["task_type"].(string); deferringTaskTypes[taskType] {
			// Cap the ledger (maxTrackedTasks): a task that would exceed it
			// is NOT tracked, and the overflow flag is set so run-end
			// decisions stop deferring to the (now unreliable) ledger.
			// Re-announcing an already-tracked task never counts as
			// overflow. Python shares the unbounded-ledger weakness; the
			// cap is a deliberate defensive divergence (see maxTrackedTasks).
			if _, tracked := q.inflightTasks[taskID]; !tracked && len(q.inflightTasks) >= maxTrackedTasks {
				if !q.taskLedgerOverflowed {
					q.taskLedgerOverflowed = true
					slog.Warn("task ledger overflow; hostile CLI or >10000 concurrent tasks — run-end may end early",
						"cap", maxTrackedTasks)
				}
				return
			}
			q.inflightTasks[taskID] = true
		}
	case "task_notification":
		delete(q.inflightTasks, taskID)
	case "task_updated":
		var status string
		if patch, ok := message["patch"].(map[string]interface{}); ok {
			status, _ = patch["status"].(string)
		}
		if terminalTaskStatuses[status] {
			delete(q.inflightTasks, taskID)
		}
	}
}

// onSessionState tracks the CLI's session_state_changed state (#1190).
// Caller must hold q.mu.
func (q *Query) onSessionState(state interface{}) {
	if s, ok := state.(string); ok {
		q.sessionState = &s
	} else {
		// A frame with no (usable) state: back to "the CLI sent none".
		q.sessionState = nil
	}
	if q.sessionState != nil && *q.sessionState == "idle" {
		if q.resultReceived {
			q.maybeEndRun()
		}
		// "idle" before any result never ends the run.
		return
	}
	// Work the CLI took up after the run ended (a finished background task
	// woke it) reopens the run until the next "idle".
	q.reopenRun()
	if q.sessionState != nil && *q.sessionState == "requires_action" {
		// The host is answering a request; stdin must outlast it.
		q.clearRunEndCeiling()
	} else {
		q.rearmRunEndCeilingBetweenTurns()
	}
}

// maybeEndRun ends the run unless a tracked background task is still in
// flight. One turn ended, but background tasks that are still running may
// need hook/SDK-MCP control responses over stdin; closing it now silently
// disables hooks and fails SDK-MCP calls with "Stream closed" (#1088). Each
// task completion wakes the parent for a follow-up turn, so a later result
// (or "idle") ends the run then. A CLI that reports session state never
// reports "idle" with an agent still live, so this matters for CLIs that
// report "idle" at every turn end or not at all.
//
// An overflowed ledger (taskLedgerOverflowed) does not stand down: it is an
// under-count, so the run ends despite a non-empty one rather than being
// withheld forever (see maxTrackedTasks).
//
// Caller must hold q.mu.
func (q *Query) maybeEndRun() {
	if len(q.inflightTasks) > 0 && !q.taskLedgerOverflowed {
		slog.Debug("Turn ended with tracked task(s) still in flight; keeping stdin open",
			"count", len(q.inflightTasks))
		return
	}
	q.endRun()
}

// endRun marks the run over, waking the stdin-closing waiter. Idempotent.
// Caller must hold q.mu.
func (q *Query) endRun() {
	q.clearRunEndCeiling()
	if !q.runEnded {
		q.runEnded = true
		close(q.runEndedCh)
	}
}

// reopenRun reopens an ended run for work that started after it ended. A
// waiter the ended run already woke still closes stdin; this makes a later
// wait (StreamInput's, once its prompts are all written) wait for the new
// work too. Once stdin is closed, or the reader is gone, the run stays ended.
//
// Caller must hold q.mu.
func (q *Query) reopenRun() {
	if q.runEnded && !q.runFinal {
		q.runEnded = false
		q.runEndedCh = make(chan struct{})
	}
}

// armRunEndCeiling ends the run anyway once the ceiling passes with no new
// turn. The CLI's own background-wait ceiling only counts once stdin is
// closed, so without this work that never finishes would hold "running", and
// stdin, open forever. It counts only the wait between turns: restarted at
// each result and whenever the CLI reports "running" again, cleared by
// main-thread turn activity and by "requires_action", and never armed while a
// turn is under way.
//
// Caller must hold q.mu.
func (q *Query) armRunEndCeiling() {
	q.clearRunEndCeiling()
	if q.runEndCeilingMS <= 0 ||
		q.runEnded ||
		q.runFinal ||
		// A frame read while close() is under way (the result branch awaits
		// a mirror flush) must not leave a sleeper behind.
		q.closed ||
		q.turnInProgress ||
		!q.hasBidirectionalNeeds() {
		return
	}
	generation := q.ceilingGeneration
	q.ceilingTimer = time.AfterFunc(
		time.Duration(min(q.runEndCeilingMS, maxRunEndCeilingMS))*time.Millisecond,
		func() { q.endRunAtCeiling(generation) },
	)
}

// rearmRunEndCeilingBetweenTurns restarts the ceiling if the run is between
// turns, past a result, with the CLI still reporting work ("running").
// Caller must hold q.mu.
func (q *Query) rearmRunEndCeilingBetweenTurns() {
	if q.resultReceived &&
		!(q.sessionState == nil || *q.sessionState == "idle" || *q.sessionState == "requires_action") {
		q.armRunEndCeiling()
	}
}

// endRunAtCeiling is the ceiling timer body. It stands down when the ceiling
// was cleared or re-armed while this timer was already firing (generation
// mismatch), and never cuts off a tracked background agent that may still
// need stdin (#1088) — the ceiling restarts once it settles, from the read
// loop's rearm when the ledger drains. An overflowed ledger
// (taskLedgerOverflowed) is an under-count, so the ceiling ends the run
// anyway rather than standing down forever (see maxTrackedTasks).
func (q *Query) endRunAtCeiling(generation int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if generation != q.ceilingGeneration {
		// Cleared or re-armed while this timer was already firing.
		return
	}
	q.ceilingTimer = nil
	if len(q.inflightTasks) > 0 && !q.taskLedgerOverflowed {
		slog.Debug("No 'idle' within the run-end ceiling, but tracked task(s) still in flight; keeping stdin open",
			"ceiling_ms", q.runEndCeilingMS, "count", len(q.inflightTasks))
		return
	}
	slog.Debug("No 'idle' within the run-end ceiling after the last result; ending the run",
		"ceiling_ms", q.runEndCeilingMS)
	q.endRun()
}

// clearRunEndCeiling cancels any armed ceiling and invalidates timers from
// earlier generations. Caller must hold q.mu.
func (q *Query) clearRunEndCeiling() {
	q.ceilingGeneration++
	if q.ceilingTimer != nil {
		q.ceilingTimer.Stop()
		q.ceilingTimer = nil
	}
}

// hasBidirectionalNeeds reports whether the CLI may still send control
// requests that need a reply. SDK MCP servers, hooks, and the can_use_tool
// permission callback are all served over the control protocol: the CLI
// writes a control_request to stdout and blocks until the SDK writes the
// matching control_response to stdin. Closing stdin while any of these are
// configured makes every later request fail CLI-side with "Stream closed".
// Mirrors the TypeScript SDK's hasBidirectionalNeeds.
func (q *Query) hasBidirectionalNeeds() bool {
	return len(q.sdkMCPServers) > 0 || len(q.hooks) > 0 || q.canUseTool != nil
}

// RunEnded returns a snapshot of the current run-ended channel; it is closed
// when the current run ends (a fresh channel replaces it if the run reopens).
func (q *Query) RunEnded() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.runEndedCh
}

// WaitForResultAndEndInput waits for the end of the run (if needed) then
// closes stdin.
//
// If SDK MCP servers, hooks, or a can_use_tool callback require
// bidirectional communication, keeps stdin open until the run ends: the
// CLI's "idle" session state after a result, or, from a CLI that reports no
// session state, the first result with no tracked tasks in flight. A result
// frame ends one turn, not necessarily the run: background tasks keep running
// past it, or have just finished and still wake the parent for a follow-up
// turn, and those turns need stdin for control responses (#1088, #1190).
//
// The wait is bounded between turns: if the CLI still reports "running"
// CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS (10 minutes by default, 0 for no
// limit) after a result with no new turn, the run ends anyway
// (armRunEndCeiling). A turn under way, a request the SDK is still answering
// and a tracked background agent still in flight (#1088) stop that clock.
// The wait is guaranteed to end: when the run ends as above, or when the
// reader exits (readMessages marks the run final).
//
// Known limitation: from a CLI that reports no session state, a result for an
// earlier prompt of a channel prompt stream still ends the run even when a
// later prompt is already queued CLI-side, so control requests from that
// later turn can find stdin closed. Single-message and string prompts, the
// common one-shot shapes, are fully covered.
func (q *Query) WaitForResultAndEndInput() {
	if q.hasBidirectionalNeeds() {
		slog.Debug("Waiting for the run to end before closing stdin",
			"sdk_mcp_servers", len(q.sdkMCPServers),
			"has_hooks", len(q.hooks) > 0,
			"has_can_use_tool", q.canUseTool != nil)
		q.mu.Lock()
		ch := q.runEndedCh
		q.mu.Unlock()
		<-ch
	}
	q.mu.Lock()
	q.runFinal = true
	q.clearRunEndCeiling()
	q.mu.Unlock()
	_ = q.transport.EndInput()
}

// StreamInput streams prompt messages from ch to the transport, stamping each
// one for verbatim_prompts, and waits for the end of the run before closing
// stdin when bidirectional needs exist (see WaitForResultAndEndInput). Each
// prompt written owes a run of its own, so the wait is for the last prompt's
// run, not an earlier one's. Once the stream ends, stdin stays closed.
//
// It replaces the transport's own channel drain for prompts routed through
// the SDK (Query and ClaudeSDKClient hand the transport a pre-closed
// placeholder channel instead), so the verbatim stamping and the run-end
// aware stdin close compose in one place.
func (q *Query) StreamInput(ch chan map[string]interface{}) {
	written := 0
	for msg := range ch {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			break
		}
		// This prompt owes a run of its own, result included: an earlier one
		// having ended does not end it.
		q.reopenRun()
		q.resultReceived = false
		q.clearRunEndCeiling()
		q.mu.Unlock()

		data, err := json.Marshal(transport.StampUserMessage(msg, q.verbatimPrompts))
		if err != nil {
			// Skip invalid messages, like the transport's own streamInput.
			continue
		}
		if err := q.transport.Write(string(data) + "\n"); err != nil {
			// Stop if writing fails; fall through and close stdin like a
			// normal end of input — the CLI would otherwise wait forever.
			break
		}
		written++
	}

	if written == 0 {
		// Nothing was sent, so no result will arrive to release the hold;
		// close immediately (mirrors the TypeScript SDK's messageCount
		// guard).
		q.mu.Lock()
		q.runFinal = true
		q.mu.Unlock()
		_ = q.transport.EndInput()
		return
	}
	q.WaitForResultAndEndInput()
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

		// Track task lifecycle frames so results can tell "one turn ended"
		// apart from "the run is done" (see #1088), and drive the run-end
		// state machine (see #1190).
		dropFrame := false
		if msgType == "system" {
			q.mu.Lock()
			subtype, _ := data["subtype"].(string)
			hadInflight := len(q.inflightTasks) > 0
			q.trackSystemFrame(data)
			if hadInflight && len(q.inflightTasks) == 0 {
				// The ceiling left the last tracked agent alone; the wait
				// between turns starts over now that it settled.
				q.rearmRunEndCeilingBetweenTurns()
			}
			if subtype == "session_state_changed" {
				q.onSessionState(data["state"])
				// Frames the CLI sent only because the transport asked for
				// them (CLAUDE_CODE_SDK_READS_SESSION_STATE); the caller did
				// not opt in. Unmarked frames — the caller opted in via
				// CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS — still flow to
				// callers; both kinds drive the run end.
				if hostOnly, _ := data["sdk_host_only"].(bool); hostOnly {
					dropFrame = true
				}
			}
			q.mu.Unlock()
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
			q.mu.Lock()
			// Track error result payloads for actionable ProcessError
			// replacement.
			if isError, _ := data["is_error"].(bool); isError {
				q.lastErrorResult = data
			} else {
				q.lastErrorResult = nil
			}
			q.resultReceived = true
			q.turnInProgress = false
			// A result ends a turn, not necessarily the run: a background
			// agent that finished just before it still wakes the session for
			// another turn, whose hook, permission and SDK MCP requests need
			// stdin. A CLI that reports session state stays "running" while
			// such a turn is owed, so wait for "idle" (some hosts send it
			// just before the result). Without state events the result is
			// all there is to go on.
			if q.sessionState == nil || *q.sessionState == "idle" || !q.hasBidirectionalNeeds() {
				q.maybeEndRun()
			} else if *q.sessionState != "requires_action" {
				// While the SDK is still answering a request the ceiling
				// waits for the "running" that follows.
				q.armRunEndCeiling()
			}
			q.mu.Unlock()
		} else if !(msgType == "system" && data["subtype"] == "session_state_changed") {
			// Anything other than the post-turn session_state_changed
			// marker means the conversation moved on; a ProcessError
			// now is a fresh crash, not the expected exit from a prior
			// error result. Mirrors the TypeScript SDK's reset logic.
			q.mu.Lock()
			q.lastErrorResult = nil
			// A main-thread turn is under way, so the ceiling stops (it
			// counts only the wait between turns, as the CLI's does) and
			// the run reopens even if the ceiling ended it while no state
			// changed.
			if (msgType == "assistant" || msgType == "stream_event") && data["parent_tool_use_id"] == nil {
				q.turnInProgress = true
				q.reopenRun()
				q.clearRunEndCeiling()
			}
			q.mu.Unlock()
		}

		if dropFrame {
			continue
		}

		// Send raw data to be parsed by caller
		q.mu.Lock()
		closed := q.closed
		q.mu.Unlock()
		if !closed {
			// The escape hatch matters, not the buffer: with the consumer
			// gone this send would park the read goroutine forever (one
			// leaked goroutine per abandoned query). Close() closes
			// closedCh to release it — Python's close() cancels and reaps
			// the reader instead (query.py:1194-1196). On a live query the
			// send behaves exactly like a plain blocking send: no message
			// is dropped while the channel is being consumed.
			select {
			case q.rawMessages <- data:
			case <-q.closedCh:
			}
		}
	}

	// The reader is gone: no future frame can end the run, and nothing can
	// wait on a reopened one — mark the run final and wake any waiter so it
	// never stalls on early exit (Python _read_messages' finally block).
	q.mu.Lock()
	q.runFinal = true
	q.endRun()
	q.mu.Unlock()

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
		q.mu.Lock()
		lastErrorResult := q.lastErrorResult
		q.mu.Unlock()
		if lastErrorResult != nil {
			if pe, ok := err.(*transport.ProcessError); ok {
				pendingErr = &ResultError{
					Message: fmt.Sprintf("Claude Code returned an error result: %s",
						errorResultText(lastErrorResult)),
					Data:     lastErrorResult,
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

	// Membership check and removal under one lock section (Python:
	// `if request_id in self.pending_control_responses`, query.py:401). An
	// unknown response is silently skipped, and removing the entry on first
	// delivery makes every later duplicate unknown too.
	q.mu.Lock()
	ch, exists := q.pendingResponses[requestID]
	if exists {
		delete(q.pendingResponses, requestID)
	}
	q.mu.Unlock()

	if !exists {
		return
	}

	var result controlResult
	subtype, _ := response["subtype"].(string)
	if subtype == "error" {
		errMsg, _ := response["error"].(string)
		result = controlResult{err: fmt.Errorf("%s", errMsg)}
	} else {
		respData, _ := response["response"].(map[string]interface{})
		result = controlResult{response: respData}
	}
	// Non-blocking on purpose: the channel is buffered(1) and the entry was
	// just removed, so this only loses to the waiter's own timeout/return
	// path — a hostile duplicate or a stale channel must never park the
	// read loop on a full buffer (Python resolves the future once; the
	// membership guard makes duplicates no-ops).
	select {
	case ch <- result:
	default:
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

	// Validate the concrete result before use: a nil (or typed-nil, or
	// unsupported) return must become an error, never a panic — the handler
	// goroutine has no recover, so a nil interface here (or a nil pointer
	// dereferenced on a field below) would kill the process. Python raises
	// TypeError in the same spot (query.py:635-638) and its handler's
	// catch-all turns it into an error control_response (query.py:694-704);
	// handleControlRequest does the same here via sendControlError. No deny
	// is synthesized — how the tool call proceeds stays the CLI's decision.
	switch r := result.(type) {
	case *PermissionResultAllow:
		if r == nil {
			return nil, fmt.Errorf("CanUseTool returned a nil or unsupported permission result")
		}
		response := map[string]interface{}{
			"behavior": "allow",
		}
		if r.UpdatedInput != nil {
			response["updatedInput"] = r.UpdatedInput
		} else {
			response["updatedInput"] = input
		}
		if r.UpdatedPermissions != nil {
			response["updatedPermissions"] = r.UpdatedPermissions
		}
		return response, nil
	case *PermissionResultDeny:
		if r == nil {
			return nil, fmt.Errorf("CanUseTool returned a nil or unsupported permission result")
		}
		response := map[string]interface{}{
			"behavior": "deny",
			"message":  r.Message,
		}
		if r.Interrupt {
			response["interrupt"] = true
		}
		return response, nil
	default:
		return nil, fmt.Errorf("CanUseTool returned a nil or unsupported permission result")
	}
}

// handleHookCallback handles hook callback requests.
func (q *Query) handleHookCallback(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	callbackID, _ := request["callback_id"].(string)
	// Snapshot the callback under q.mu (Initialize registers under the same
	// lock; Python relies on asyncio's single thread for the same
	// guarantee). The callback is invoked below, outside the lock — user
	// code may call back into Query methods that take q.mu. An unknown ID is
	// an explicit error returned to the CLI as an error control_response,
	// never a crash and never silence (Python: "No hook callback found for
	// ID: ..." from query.py:643-646, caught by the handler's except and
	// written back at query.py:694-704).
	q.mu.Lock()
	callback, exists := q.hookCallbacks[callbackID]
	q.mu.Unlock()
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
