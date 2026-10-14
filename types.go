package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrNotImplemented is returned by BaseSessionStore optional methods.
// Store implementations that do not support an optional capability should
// return this error. SDK helper functions (e.g. ListSessionsFromStore) check
// errors.Is(err, ErrNotImplemented) to determine which capabilities the store
// provides.
var ErrNotImplemented = errors.New("method not implemented")

// PermissionMode defines security levels for tool execution.
type PermissionMode string

const (
	// PermissionModeDefault is the default mode where CLI prompts for dangerous tools.
	PermissionModeDefault PermissionMode = "default"
	// PermissionModeAcceptEdits auto-accepts file edits.
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	// PermissionModePlan is for planning mode.
	PermissionModePlan PermissionMode = "plan"
	// PermissionModeBypassPermissions allows all tools (use with caution).
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
	// PermissionModeDontAsk denies any tool not pre-approved by allow rules
	// (i.e. anything unapproved is silently denied rather than prompted).
	PermissionModeDontAsk PermissionMode = "dontAsk"
	// PermissionModeAuto uses a model classifier to decide the appropriate
	// permission level at runtime.
	PermissionModeAuto PermissionMode = "auto"
)

// SettingSource specifies which setting sources to load.
type SettingSource string

const (
	SettingSourceUser    SettingSource = "user"
	SettingSourceProject SettingSource = "project"
	SettingSourceLocal   SettingSource = "local"
)

// SdkBeta defines known beta feature identifiers.
type SdkBeta = string

const (
	SdkBetaContext1M SdkBeta = "context-1m-2025-08-07"
)

// EffortLevel represents the effort level for Claude responses.
// Controls how much effort Claude puts into its response, working with
// adaptive thinking to guide thinking depth.
type EffortLevel = string

const (
	// EffortLevelLow uses minimal effort.
	EffortLevelLow EffortLevel = "low"
	// EffortLevelMedium uses moderate effort.
	EffortLevelMedium EffortLevel = "medium"
	// EffortLevelHigh uses high effort.
	EffortLevelHigh EffortLevel = "high"
	// EffortLevelXHigh uses extra-high effort (Opus 4.7 only; falls back to high on other models).
	EffortLevelXHigh EffortLevel = "xhigh"
	// EffortLevelMax uses maximum effort.
	EffortLevelMax EffortLevel = "max"
)

// HookEvent defines the type of hook event.
type HookEvent string

const (
	HookEventPreToolUse         HookEvent = "PreToolUse"
	HookEventPostToolUse        HookEvent = "PostToolUse"
	HookEventPostToolUseFailure HookEvent = "PostToolUseFailure"
	HookEventUserPromptSubmit   HookEvent = "UserPromptSubmit"
	HookEventStop               HookEvent = "Stop"
	HookEventSubagentStop       HookEvent = "SubagentStop"
	HookEventPreCompact         HookEvent = "PreCompact"
	HookEventNotification       HookEvent = "Notification"
	HookEventSubagentStart      HookEvent = "SubagentStart"
	HookEventPermissionRequest  HookEvent = "PermissionRequest"
)

// Message is the interface for all message types from Claude Code.
type Message interface {
	messageMarker()
}

// ContentBlock is the interface for content blocks within messages.
type ContentBlock interface {
	contentBlockMarker()
}

// MessageOriginKind identifies the known values of a MessageOrigin's "kind"
// discriminator. Newer CLI versions may emit kinds not listed here; treat
// anything unrecognized as "not human".
type MessageOriginKind string

const (
	MessageOriginKindHuman            MessageOriginKind = "human"
	MessageOriginKindChannel          MessageOriginKind = "channel"
	MessageOriginKindPeer             MessageOriginKind = "peer"
	MessageOriginKindTaskNotification MessageOriginKind = "task-notification"
	MessageOriginKindCoordinator      MessageOriginKind = "coordinator"
	MessageOriginKindUnclassified     MessageOriginKind = "unclassified"
	MessageOriginKindObserver         MessageOriginKind = "observer"
	MessageOriginKindAutoContinuation MessageOriginKind = "auto-continuation"
	MessageOriginKindObserverActivity MessageOriginKind = "observer-activity"
)

// TaskNotificationOriginSubkind identifies the values of a MessageOrigin's
// "subkind" key for kind == "task-notification".
type TaskNotificationOriginSubkind string

const (
	// TaskNotificationOriginSubkindScheduledTrigger marks a delivery that is
	// the fired prompt of a scheduled task.
	TaskNotificationOriginSubkindScheduledTrigger TaskNotificationOriginSubkind = "scheduled-trigger"
	// TaskNotificationOriginSubkindPeerSendMessage marks a message sent from
	// another of the user's sessions.
	TaskNotificationOriginSubkindPeerSendMessage TaskNotificationOriginSubkind = "peer-send-message"
)

// MessageOrigin describes the provenance of a user-role message, and — on a
// ResultMessage — of the message that triggered that turn.
//
// In streaming-input mode a single connection interleaves the turns you send
// with turns the session injects on its own (background-task notifications,
// scheduled-task prompts, MCP channel messages, messages relayed from peer
// sessions, ...). The origin tells them apart, e.g. to decide whether a
// ResultMessage answers *your* prompt:
//
//	if result.Origin == nil || result.Origin.Kind() == claude.MessageOriginKindHuman {
//		// a turn this application submitted
//	}
//
// The map is passed through from the CLI verbatim (including keys this SDK
// version doesn't model), so newer origin kinds/fields stay visible. Only
// "kind" is always present; the remaining keys depend on the kind:
//
//   - kind "channel": "server" — name of the MCP server the message arrived on.
//   - kind "peer" / "observer": "from" — sender address (sender-asserted; use
//     for reply routing / display, never as proof of identity);
//     "senderTaskId" — task id of the in-process background subagent that sent
//     this message (absent for cross-session peers).
//   - kind "peer": "name" — sender display name, normalized by the CLI;
//     "fromSession" — the sender's host-openable session id, if provided (a
//     navigation target only); "body" — decoded message body with the peer
//     envelope stripped (render this instead of re-parsing the message text);
//     "verifiedPeerPid" — kernel-verified pid of the connecting process
//     (absent when unverifiable).
//   - kind "task-notification": "subkind" — see TaskNotificationOriginSubkind;
//     absent for ordinary background-task notifications.
//
// A nil origin means the CLI did not attribute the message: prompts sent
// through Query or Client.Query arrive that way unless the host stamps
// {"kind": "human"} on the message itself (only the "human" kind is honored
// from an SDK host).
type MessageOrigin map[string]interface{}

// Kind returns the origin's kind discriminator, or "" if the map carries no
// string "kind" (which the parser never produces — see parseOrigin).
func (o MessageOrigin) Kind() MessageOriginKind {
	kind, _ := o["kind"].(string)
	return MessageOriginKind(kind)
}

// Subkind returns the origin's "subkind" (only meaningful for
// kind == "task-notification"), or "" if absent or not a string.
func (o MessageOrigin) Subkind() TaskNotificationOriginSubkind {
	subkind, _ := o["subkind"].(string)
	return TaskNotificationOriginSubkind(subkind)
}

// UserMessage represents a user message.
type UserMessage struct {
	Content         interface{}            `json:"content"` // string or []ContentBlock
	UUID            string                 `json:"uuid,omitempty"`
	ParentToolUseID string                 `json:"parent_tool_use_id,omitempty"`
	ToolUseResult   map[string]interface{} `json:"tool_use_result,omitempty"`
	// Origin is the provenance of this message — see MessageOrigin. Nil when
	// the CLI did not attribute it. Populated on injected turns (task
	// notifications, channel/peer messages, ...) and on user messages the CLI
	// replays; tool-result messages never carry it.
	Origin MessageOrigin `json:"origin,omitempty"`
}

func (m *UserMessage) messageMarker() {}

// AssistantMessage represents an assistant message with content blocks.
type AssistantMessage struct {
	Content         []ContentBlock         `json:"content"`
	Model           string                 `json:"model"`
	ParentToolUseID string                 `json:"parent_tool_use_id,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Usage           map[string]interface{} `json:"usage,omitempty"`
	MessageID       string                 `json:"message_id,omitempty"`
	StopReason      string                 `json:"stop_reason,omitempty"`
	SessionID       string                 `json:"session_id,omitempty"`
	UUID            string                 `json:"uuid,omitempty"`
}

func (m *AssistantMessage) messageMarker() {}

// SystemMessage represents a system message with metadata.
//
// For Subtype "init", Data is the session metadata frame described by
// SystemInitData — decode it with ParseSystemInitData.
type SystemMessage struct {
	Subtype string                 `json:"subtype"`
	Data    map[string]interface{} `json:"data"`
}

func (m *SystemMessage) messageMarker() {}

// SystemInitMcpServer is an MCP server listed in SystemInitData.
type SystemInitMcpServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Source is where the server definition came from: "sdk", "plugin", or a
	// config scope such as "user" or "project". Empty on CLIs that predate
	// the field.
	Source string `json:"source,omitempty"`
}

// SystemInitPlugin is a loaded plugin listed in SystemInitData.
type SystemInitPlugin struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Version is the version declared in the plugin's manifest, verbatim.
	// Empty when the manifest declares none.
	Version string `json:"version,omitempty"`
}

// SystemInitPluginError is a plugin load failure listed in SystemInitData.
type SystemInitPluginError struct {
	// Plugin is "name@marketplace", or a positional tag such as "inline[0]"
	// or "synced[0]" when a directory entry failed before it had a name.
	Plugin string `json:"plugin"`
	// Type is a failure category from an open set (e.g. "path-not-found",
	// "manifest-validation-error"). Treat unknown values as a generic
	// failure.
	Type string `json:"type"`
	// Message describes the failure.
	Message string `json:"message"`
	// Path is present only when a plugins, --plugin-dir or synced directory
	// entry did not load at all: that entry's path, resolved against the
	// cwd. Use it to match a positional Plugin tag to your entry.
	Path string `json:"path,omitempty"`
}

// FastModeState is the fast mode state reported in SystemInitData.
type FastModeState string

const (
	FastModeStateOff      FastModeState = "off"
	FastModeStateCooldown FastModeState = "cooldown"
	FastModeStateOn       FastModeState = "on"
)

// FastModeDisabledReason names why fast mode cannot serve right now, as
// reported in SystemInitData. Newer CLIs may send values not listed here.
type FastModeDisabledReason string

const (
	FastModeDisabledReasonFree               FastModeDisabledReason = "free"
	FastModeDisabledReasonPreference         FastModeDisabledReason = "preference"
	FastModeDisabledReasonExtraUsageDisabled FastModeDisabledReason = "extra_usage_disabled"
	FastModeDisabledReasonNetworkError       FastModeDisabledReason = "network_error"
	FastModeDisabledReasonUnknown            FastModeDisabledReason = "unknown"
	FastModeDisabledReasonNotFirstParty      FastModeDisabledReason = "not_first_party"
	FastModeDisabledReasonDisabledByEnv      FastModeDisabledReason = "disabled_by_env"
	FastModeDisabledReasonModelNotAllowed    FastModeDisabledReason = "model_not_allowed"
	FastModeDisabledReasonSdkOptInRequired   FastModeDisabledReason = "sdk_opt_in_required"
	FastModeDisabledReasonPending            FastModeDisabledReason = "pending"
)

// SystemInitData is the shape of SystemMessage.Data for a system/init
// message.
//
// The CLI emits this session metadata at the start of each turn: model,
// working directory, tools, MCP servers, slash commands, permission mode,
// and the capabilities list for feature detection. Mirrors the fields the
// TypeScript SDK reference documents for SDKSystemMessage, with the same key
// names as on the wire.
//
// The parser does not validate the payload, so this describes what the CLI
// sends rather than a guarantee. Narrow Data once you have checked the
// subtype:
//
//	if msg, ok := message.(*claude.SystemMessage); ok && msg.Subtype == "init" {
//		init, err := claude.ParseSystemInitData(msg.Data)
//		// init.Model, init.Capabilities, ...
//	}
type SystemInitData struct {
	Type    string `json:"type"`    // always "system"
	Subtype string `json:"subtype"` // always "init"
	UUID    string `json:"uuid"`
	// SessionID is the ID of the session this frame opened.
	SessionID string `json:"session_id"`
	// Agents lists the agent types available to the Agent tool.
	Agents []string `json:"agents,omitempty"`
	// APIKeySource is where the API credential came from:
	// "ANTHROPIC_API_KEY", "apiKeyHelper", "/login managed key", or "none"
	// (no API key in use, e.g. a claude.ai login or a third-party cloud
	// provider).
	APIKeySource string   `json:"apiKeySource"`
	Betas        []string `json:"betas,omitempty"`
	// ClaudeCodeVersion is the CLI version serving this session.
	ClaudeCodeVersion string `json:"claude_code_version"`
	// CWD is the CLI process's working directory.
	CWD string `json:"cwd"`
	// Tools lists the tool names enabled for this session.
	Tools []string `json:"tools"`
	// MCPServers lists the MCP servers configured for this session.
	MCPServers []SystemInitMcpServer `json:"mcp_servers"`
	// Model is the model id serving this session.
	Model string `json:"model"`
	// PermissionMode is the permission mode the session started in.
	PermissionMode PermissionMode `json:"permissionMode"`
	// SlashCommands lists the available slash commands.
	SlashCommands []string `json:"slash_commands"`
	// TerminalSlashCommands lists the entries of SlashCommands whose
	// interface is bound to the local terminal (e.g. exit). Present only
	// when non-empty.
	TerminalSlashCommands []string `json:"terminal_slash_commands,omitempty"`
	// OutputStyle is the active output style name.
	OutputStyle string `json:"output_style"`
	// Skills lists the skill names available to the session.
	Skills []string `json:"skills"`
	// Plugins lists the loaded plugins.
	Plugins []SystemInitPlugin `json:"plugins"`
	// PluginErrors lists plugin load failures. The CLI omits the key when
	// nothing failed, but an empty list does not by itself assert a clean
	// load (older CLIs and some hosted sessions never send it).
	PluginErrors []SystemInitPluginError `json:"plugin_errors,omitempty"`
	// FastModeState reports whether fast mode is serving this session.
	FastModeState FastModeState `json:"fast_mode_state,omitempty"`
	// FastModeDisabledReason names the check that blocks fast mode, when
	// something does.
	FastModeDisabledReason FastModeDisabledReason `json:"fast_mode_disabled_reason,omitempty"`
	// Capabilities lists protocol capabilities this CLI supports (e.g.
	// "interrupt_receipt_v1"), for feature detection instead of comparing
	// ClaudeCodeVersion. Open set: ignore values you don't recognize.
	// Absent on CLIs before 2.1.205.
	Capabilities []string `json:"capabilities,omitempty"`
}

// ParseSystemInitData decodes the Data map of a SystemMessage with Subtype
// "init" into the typed SystemInitData payload. It is the Go equivalent of
// Python's cast to SystemInitData: the parser keeps returning a plain
// SystemMessage for init frames, and this narrows the payload. The CLI's
// payload is not validated, so unknown keys are ignored and absent optional
// fields are left zero — callers that need the raw frame should read
// SystemMessage.Data directly.
func ParseSystemInitData(data map[string]interface{}) (*SystemInitData, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var init SystemInitData
	if err := json.Unmarshal(raw, &init); err != nil {
		return nil, err
	}
	return &init, nil
}

// DeferredToolUse represents a tool use that was deferred by a PreToolUse hook
// returning decision "defer".
//
// When a PreToolUse hook returns decision "defer", the run stops and the result
// message carries the deferred tool call here so the caller can inspect it and
// decide whether to resume.
type DeferredToolUse struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

// ModelUsage is the per-model token usage and cost breakdown.
//
// Keys match the TypeScript SDK's ModelUsage shape (camelCase), since the
// value is passed through verbatim from the CLI's modelUsage field.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`
	// CanonicalModel is the canonical model id used for the pricing lookup
	// (e.g. "claude-opus-4-7"). May differ from the raw model string this
	// entry is keyed by (provider-specific ids, aliases).
	CanonicalModel string `json:"canonicalModel,omitempty"`
	// Provider is the API provider that served this model ("firstParty",
	// "bedrock", "vertex", "foundry", "anthropicAws",
	// "anthropicGoogleCloud", "mantle", "gateway").
	Provider string `json:"provider,omitempty"`
}

// ResultMessage represents a result message with cost and usage information.
type ResultMessage struct {
	Subtype           string                 `json:"subtype"`
	DurationMS        int                    `json:"duration_ms"`
	DurationAPIMS     int                    `json:"duration_api_ms"`
	IsError           bool                   `json:"is_error"`
	NumTurns          int                    `json:"num_turns"`
	SessionID         string                 `json:"session_id"`
	StopReason        string                 `json:"stop_reason,omitempty"`
	TotalCostUSD      float64                `json:"total_cost_usd,omitempty"`
	Usage             map[string]interface{} `json:"usage,omitempty"`
	Result            string                 `json:"result,omitempty"`
	StructuredOutput  interface{}            `json:"structured_output,omitempty"`
	ModelUsage        map[string]ModelUsage  `json:"model_usage,omitempty"`
	PermissionDenials []interface{}          `json:"permission_denials,omitempty"`
	DeferredToolUse   *DeferredToolUse       `json:"deferred_tool_use,omitempty"`
	APIErrorStatus    *int                   `json:"api_error_status,omitempty"`
	Errors            []string               `json:"errors,omitempty"`
	UUID              string                 `json:"uuid,omitempty"`
	// TerminalReason reports why the query loop terminated (e.g. "completed",
	// "max_turns", "aborted_streaming"). A value of "aborted_streaming" or
	// "aborted_tools" indicates the turn was cancelled (via
	// Client.Interrupt or an interrupt control request). Empty when the CLI
	// did not report a terminal reason (older CLI versions, or a result that
	// bypassed the query loop such as a local slash command). Mirrors the
	// TypeScript SDK's SDKResultMessage.terminal_reason.
	TerminalReason string `json:"terminal_reason,omitempty"`
	// Origin is the origin of the user message that triggered this turn — see
	// MessageOrigin. Lets a streaming-input consumer distinguish the result of
	// its own prompt (nil, or {"kind": "human"} if it stamped that) from
	// results of injected turns such as background-task notifications
	// ({"kind": "task-notification"}).
	Origin MessageOrigin `json:"origin,omitempty"`
}

func (m *ResultMessage) messageMarker() {}

// StreamEvent represents a stream event for partial message updates.
type StreamEvent struct {
	UUID            string                 `json:"uuid"`
	SessionID       string                 `json:"session_id"`
	Event           map[string]interface{} `json:"event"`
	ParentToolUseID string                 `json:"parent_tool_use_id,omitempty"`
}

func (m *StreamEvent) messageMarker() {}

// RateLimitStatus represents the current rate limit state.
type RateLimitStatus string

const (
	RateLimitStatusAllowed        RateLimitStatus = "allowed"
	RateLimitStatusAllowedWarning RateLimitStatus = "allowed_warning"
	RateLimitStatusRejected       RateLimitStatus = "rejected"
)

// RateLimitType identifies which rate limit window applies.
type RateLimitType string

const (
	RateLimitTypeFiveHour       RateLimitType = "five_hour"
	RateLimitTypeSevenDay       RateLimitType = "seven_day"
	RateLimitTypeSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitTypeSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitTypeOverage        RateLimitType = "overage"
)

// RateLimitInfo contains rate limit status details.
type RateLimitInfo struct {
	Status                RateLimitStatus        `json:"status"`
	ResetsAt              *int64                 `json:"resets_at,omitempty"`
	RateLimitType         RateLimitType          `json:"rate_limit_type,omitempty"`
	Utilization           *float64               `json:"utilization,omitempty"`
	OverageStatus         RateLimitStatus        `json:"overage_status,omitempty"`
	OverageResetsAt       *int64                 `json:"overage_resets_at,omitempty"`
	OverageDisabledReason string                 `json:"overage_disabled_reason,omitempty"`
	Raw                   map[string]interface{} `json:"raw,omitempty"`
}

// RateLimitEvent represents a rate limit event emitted when rate limit info changes.
type RateLimitEvent struct {
	RateLimitInfo RateLimitInfo `json:"rate_limit_info"`
	UUID          string        `json:"uuid"`
	SessionID     string        `json:"session_id"`
}

func (m *RateLimitEvent) messageMarker() {}

// ConversationResetMessage is emitted when the session's conversation is
// replaced without ending the connection — e.g. after /clear or any other
// flow that discards the transcript mid-session.
//
// In streaming input mode a single connection can carry many user turns, and
// a reset clears the conversation history *and* zeroes the running totals
// reported on subsequent ResultMessage values (e.g. TotalCostUSD). If you
// accumulate those totals across a long-lived session, snapshot them when
// this message arrives.
type ConversationResetMessage struct {
	// NewConversationID is an opaque identifier for the fresh conversation,
	// for UIs to key an empty transcript on (and discard any cached session
	// title). This is *not* the SessionID of subsequent messages — read that
	// from the next message.
	NewConversationID string `json:"new_conversation_id"`
	// UUID is the unique ID of this message.
	UUID string `json:"uuid"`
	// SessionID is the ID of the session that was reset (the outgoing
	// session; messages after the reset carry a new SessionID).
	SessionID string `json:"session_id"`
}

func (m *ConversationResetMessage) messageMarker() {}

// ContextUsageCategory represents a single context usage category.
type ContextUsageCategory struct {
	Name       string `json:"name"`
	Tokens     int    `json:"tokens"`
	Color      string `json:"color"`
	IsDeferred bool   `json:"isDeferred,omitempty"`
}

// ContextUsageResponse represents the response from GetContextUsage.
type ContextUsageResponse struct {
	Categories           []ContextUsageCategory     `json:"categories"`
	TotalTokens          int                        `json:"totalTokens"`
	MaxTokens            int                        `json:"maxTokens"`
	RawMaxTokens         int                        `json:"rawMaxTokens"`
	Percentage           float64                    `json:"percentage"`
	Model                string                     `json:"model"`
	IsAutoCompactEnabled bool                       `json:"isAutoCompactEnabled"`
	MemoryFiles          []map[string]interface{}   `json:"memoryFiles"`
	McpTools             []map[string]interface{}   `json:"mcpTools"`
	Agents               []map[string]interface{}   `json:"agents"`
	GridRows             [][]map[string]interface{} `json:"gridRows"`
	AutoCompactThreshold *int                       `json:"autoCompactThreshold,omitempty"`
	DeferredBuiltinTools []map[string]interface{}   `json:"deferredBuiltinTools,omitempty"`
	SystemTools          []map[string]interface{}   `json:"systemTools,omitempty"`
	SystemPromptSections []map[string]interface{}   `json:"systemPromptSections,omitempty"`
	SlashCommands        map[string]interface{}     `json:"slashCommands,omitempty"`
	Skills               map[string]interface{}     `json:"skills,omitempty"`
	MessageBreakdown     map[string]interface{}     `json:"messageBreakdown,omitempty"`
	APIUsage             map[string]interface{}     `json:"apiUsage,omitempty"`
}

// TaskUsage represents usage statistics reported in task_progress and task_notification messages.
type TaskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMS  int `json:"duration_ms"`
}

// TaskNotificationStatus represents the status of a task notification.
type TaskNotificationStatus string

const (
	TaskNotificationStatusCompleted TaskNotificationStatus = "completed"
	TaskNotificationStatusFailed    TaskNotificationStatus = "failed"
	TaskNotificationStatusStopped   TaskNotificationStatus = "stopped"
)

// TaskStartedMessage represents a task started system message.
type TaskStartedMessage struct {
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	UUID        string `json:"uuid"`
	SessionID   string `json:"session_id"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
	TaskType    string `json:"task_type,omitempty"`
}

func (m *TaskStartedMessage) messageMarker() {}

// TaskProgressMessage represents a task progress system message.
type TaskProgressMessage struct {
	TaskID       string    `json:"task_id"`
	Description  string    `json:"description"`
	Usage        TaskUsage `json:"usage"`
	UUID         string    `json:"uuid"`
	SessionID    string    `json:"session_id"`
	ToolUseID    string    `json:"tool_use_id,omitempty"`
	LastToolName string    `json:"last_tool_name,omitempty"`
}

func (m *TaskProgressMessage) messageMarker() {}

// TaskNotificationMessage represents a task notification system message.
type TaskNotificationMessage struct {
	TaskID     string                 `json:"task_id"`
	Status     TaskNotificationStatus `json:"status"`
	OutputFile string                 `json:"output_file"`
	Summary    string                 `json:"summary"`
	UUID       string                 `json:"uuid"`
	SessionID  string                 `json:"session_id"`
	ToolUseID  string                 `json:"tool_use_id,omitempty"`
	Usage      *TaskUsage             `json:"usage,omitempty"`
}

func (m *TaskNotificationMessage) messageMarker() {}

// TaskUpdatedStatus represents a status value reported inside a task_updated patch.
//
// "pending", "running", "paused" are non-terminal; "completed", "failed",
// "killed" are terminal. Note task_updated reports the raw "killed"; the CLI
// maps that to "stopped" only when it emits a task_notification.
type TaskUpdatedStatus string

const (
	TaskUpdatedStatusPending   TaskUpdatedStatus = "pending"
	TaskUpdatedStatusRunning   TaskUpdatedStatus = "running"
	TaskUpdatedStatusPaused    TaskUpdatedStatus = "paused"
	TaskUpdatedStatusCompleted TaskUpdatedStatus = "completed"
	TaskUpdatedStatusFailed    TaskUpdatedStatus = "failed"
	TaskUpdatedStatusKilled    TaskUpdatedStatus = "killed"
)

// TerminalTaskStatuses is the set of task statuses that mean the task has
// finished and should be cleared from any "active task" tracking.
//
// This set spans both lifecycle vocabularies: task_notification reports
// "stopped" (the CLI's mapped form of a killed task) while task_updated
// reports the raw "killed". Consumers should treat the Status of a
// TaskNotificationMessage and a TaskUpdatedMessage the same way, e.g.
//
//	if claude.TerminalTaskStatuses[string(msg.Status)] { ... }
var TerminalTaskStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"stopped":   true,
	"killed":    true,
}

// TaskUpdatedMessage represents a task_updated system message emitted when a
// background task's state changes.
//
// The CLI emits system/task_updated events as a task moves through its
// lifecycle. Patch carries the changed fields (e.g. status, end_time); when
// Patch["status"] is terminal (see TerminalTaskStatuses) the task has finished.
//
// Lifecycle note: a background task's terminal state can arrive ONLY as a
// TaskUpdatedMessage with no accompanying TaskNotificationMessage. For example
// a task stopped via TaskStop reports status="killed" here, and the matching
// notification is sometimes suppressed. Consumers that track active task IDs
// should therefore clear them on a terminal status (see TerminalTaskStatuses)
// from EITHER a TaskNotificationMessage or a TaskUpdatedMessage.
type TaskUpdatedMessage struct {
	TaskID    string                 `json:"task_id"`
	Patch     map[string]interface{} `json:"patch"`
	Status    TaskUpdatedStatus      `json:"status,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	UUID      string                 `json:"uuid,omitempty"`
}

func (m *TaskUpdatedMessage) messageMarker() {}

// TextBlock represents a text content block.
type TextBlock struct {
	Text string `json:"text"`
}

func (b *TextBlock) contentBlockMarker() {}

// MarshalJSON implements json.Marshaler for TextBlock.
func (b *TextBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type": "text",
		"text": b.Text,
	})
}

// ThinkingBlock represents a thinking content block.
type ThinkingBlock struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

func (b *ThinkingBlock) contentBlockMarker() {}

// MarshalJSON implements json.Marshaler for ThinkingBlock.
func (b *ThinkingBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type":      "thinking",
		"thinking":  b.Thinking,
		"signature": b.Signature,
	})
}

// ToolUseBlock represents a tool use content block.
type ToolUseBlock struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

func (b *ToolUseBlock) contentBlockMarker() {}

// MarshalJSON implements json.Marshaler for ToolUseBlock.
func (b *ToolUseBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type":  "tool_use",
		"id":    b.ID,
		"name":  b.Name,
		"input": b.Input,
	})
}

// ToolResultBlock represents a tool result content block.
type ToolResultBlock struct {
	ToolUseID string      `json:"tool_use_id"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
}

func (b *ToolResultBlock) contentBlockMarker() {}

// MarshalJSON implements json.Marshaler for ToolResultBlock.
func (b *ToolResultBlock) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{
		"type":        "tool_result",
		"tool_use_id": b.ToolUseID,
	}
	if b.Content != nil {
		m["content"] = b.Content
	}
	if b.IsError {
		m["is_error"] = b.IsError
	}
	return json.Marshal(m)
}

// AssistantMessageError represents error types for assistant messages.
type AssistantMessageError string

const (
	AssistantMessageErrorAuthFailed     AssistantMessageError = "authentication_failed"
	AssistantMessageErrorBilling        AssistantMessageError = "billing_error"
	AssistantMessageErrorRateLimit      AssistantMessageError = "rate_limit"
	AssistantMessageErrorInvalidRequest AssistantMessageError = "invalid_request"
	AssistantMessageErrorServer         AssistantMessageError = "server_error"
	AssistantMessageErrorUnknown        AssistantMessageError = "unknown"
)

// ParseContentBlock parses a raw content block map into a typed ContentBlock.
func ParseContentBlock(raw map[string]interface{}) (ContentBlock, error) {
	blockType, ok := raw["type"].(string)
	if !ok {
		return nil, NewMessageParseError("content block missing 'type' field", raw)
	}

	switch blockType {
	case "text":
		text, _ := raw["text"].(string)
		return &TextBlock{Text: text}, nil

	case "thinking":
		thinking, _ := raw["thinking"].(string)
		signature, _ := raw["signature"].(string)
		return &ThinkingBlock{Thinking: thinking, Signature: signature}, nil

	case "tool_use":
		id, _ := raw["id"].(string)
		name, _ := raw["name"].(string)
		input, _ := raw["input"].(map[string]interface{})
		return &ToolUseBlock{ID: id, Name: name, Input: input}, nil

	case "tool_result":
		toolUseID, _ := raw["tool_use_id"].(string)
		content := raw["content"]
		isError, _ := raw["is_error"].(bool)
		return &ToolResultBlock{ToolUseID: toolUseID, Content: content, IsError: isError}, nil

	case "server_tool_use":
		id, _ := raw["id"].(string)
		name, _ := raw["name"].(string)
		input, _ := raw["input"].(map[string]interface{})
		return &ServerToolUseBlock{ID: id, Name: name, Input: input}, nil

	case "advisor_tool_result":
		toolUseID, _ := raw["tool_use_id"].(string)
		content, _ := raw["content"].(map[string]interface{})
		return &ServerToolResultBlock{ToolUseID: toolUseID, Content: content}, nil

	default:
		return nil, NewMessageParseError("unknown content block type: "+blockType, raw)
	}
}

// ParseContentBlocks parses a slice of raw content block maps into typed ContentBlocks.
func ParseContentBlocks(rawBlocks []interface{}) ([]ContentBlock, error) {
	blocks := make([]ContentBlock, 0, len(rawBlocks))
	for _, raw := range rawBlocks {
		rawMap, ok := raw.(map[string]interface{})
		if !ok {
			// A non-dict block is malformed content, not something to drop
			// silently. Raise MessageParseError naming the offending Go type,
			// mirroring Python SDK #1058 (previously Go `continue`d past it,
			// which lost the block without trace).
			return nil, NewMessageParseError(
				fmt.Sprintf("invalid content block (expected dict, got %T)", raw),
				nil,
			)
		}
		block, err := ParseContentBlock(rawMap)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// SystemPromptPreset represents a system prompt preset configuration.
type SystemPromptPreset struct {
	Type   string `json:"type"`   // "preset"
	Preset string `json:"preset"` // "claude_code"
	Append string `json:"append,omitempty"`
	// ExcludeDynamicSections strips per-user dynamic sections (working directory,
	// auto-memory, git status) from the system prompt so it stays static and
	// cacheable across users. The stripped content is re-injected into the first
	// user message so the model still has access to it.
	//
	// Use this when many users share the same preset system prompt and you
	// want the prompt-caching prefix to hit cross-user.
	//
	// Requires a Claude Code CLI version that supports this option; older
	// CLIs silently ignore it.
	ExcludeDynamicSections *bool `json:"excludeDynamicSections,omitempty"`
	// Snapshot controls whether the session keeps the system prompt it
	// recorded on its first request. When true, every later request
	// (including after resume) sends the recorded prompt, so changing Append
	// has no effect until the session is compacted or a new session starts.
	// When false, the prompt is rebuilt on every request — for example while
	// iterating on Append text across calls that resume the same session.
	// When omitted it acts as true, except in bare mode (--bare), where it
	// acts as false.
	//
	// Requires Claude Code CLI 2.1.257 or later. Before 2.1.265, a session
	// with an Append or custom prompt recorded it only when Snapshot was
	// true.
	Snapshot *bool `json:"snapshot,omitempty"`
}

// SystemPromptCustom is a custom system prompt that can also set Snapshot.
// It has the same wire meaning as a plain custom prompt string, but this
// form can carry snapshot semantics (Python's {"type":"custom"} form).
type SystemPromptCustom struct {
	Type   string `json:"type"` // "custom"
	Prompt string `json:"prompt"`
	// Snapshot has the same meaning as SystemPromptPreset.Snapshot,
	// applied to Prompt.
	Snapshot *bool `json:"snapshot,omitempty"`
}

// ToolsPreset represents a tools preset configuration.
type ToolsPreset struct {
	Type   string `json:"type"`   // "preset"
	Preset string `json:"preset"` // "claude_code"
}

// MCPServerConfig is the interface for MCP server configurations.
type MCPServerConfig interface {
	mcpServerConfigMarker()
}

// MCPStdioServerConfig configures an MCP stdio server.
type MCPStdioServerConfig struct {
	Type    string            `json:"type,omitempty"` // "stdio" (optional for backwards compatibility)
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (c *MCPStdioServerConfig) mcpServerConfigMarker() {}

// MCPSSEServerConfig configures an MCP SSE server.
type MCPSSEServerConfig struct {
	Type    string            `json:"type"` // "sse"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (c *MCPSSEServerConfig) mcpServerConfigMarker() {}

// MCPHTTPServerConfig configures an MCP HTTP server.
type MCPHTTPServerConfig struct {
	Type    string            `json:"type"` // "http"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (c *MCPHTTPServerConfig) mcpServerConfigMarker() {}

// MCPSdkServerConfig configures an in-process SDK MCP server.
type MCPSdkServerConfig struct {
	Type string `json:"type"` // "sdk"
	Name string `json:"name"`
	// Instance is the MCP server served to the CLI over the control channel
	// (not serialized). CreateSdkMcpServer populates it with a factory-built
	// server; it may instead be set directly to a hand-built *mcp.Server
	// (github.com/modelcontextprotocol/go-sdk/mcp), in which case everything
	// the server implements — resources, prompts, custom methods — reaches
	// the CLI verbatim.
	Instance *mcp.Server `json:"-"`
}

func (c *MCPSdkServerConfig) mcpServerConfigMarker() {}

// SystemPromptFile configures a system prompt loaded from a file.
type SystemPromptFile struct {
	Type string `json:"type"` // "file"
	Path string `json:"path"`
}

// TaskBudget configures an API-side task budget in tokens.
type TaskBudget struct {
	Total int `json:"total"`
}

// AgentDefinition defines a custom agent configuration.
type AgentDefinition struct {
	Description     string         `json:"description"`
	Prompt          string         `json:"prompt"`
	Tools           []string       `json:"tools,omitempty"`
	DisallowedTools []string       `json:"disallowedTools,omitempty"`
	Model           string         `json:"model,omitempty"`
	Skills          []string       `json:"skills,omitempty"`
	Memory          string         `json:"memory,omitempty"` // "user", "project", "local"
	McpServers      []interface{}  `json:"mcpServers,omitempty"`
	InitialPrompt   string         `json:"initialPrompt,omitempty"`
	MaxTurns        *int           `json:"maxTurns,omitempty"`
	Background      *bool          `json:"background,omitempty"`
	Effort          interface{}    `json:"effort,omitempty"`         // "low", "medium", "high", "xhigh", "max", or int
	PermissionMode  PermissionMode `json:"permissionMode,omitempty"` // e.g. PermissionModeDefault
}

// SandboxSettings configures bash command sandboxing.
type SandboxSettings struct {
	Enabled                  bool                  `json:"enabled,omitempty"`
	AutoAllowBashIfSandboxed bool                  `json:"autoAllowBashIfSandboxed,omitempty"`
	ExcludedCommands         []string              `json:"excludedCommands,omitempty"`
	AllowUnsandboxedCommands bool                  `json:"allowUnsandboxedCommands,omitempty"`
	Network                  *SandboxNetworkConfig `json:"network,omitempty"`
	// IgnoreViolations maps a command pattern to the violation substrings
	// to leave unreported — see SandboxIgnoreViolations.
	IgnoreViolations          SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	EnableWeakerNestedSandbox bool                    `json:"enableWeakerNestedSandbox,omitempty"`
}

// SandboxNetworkConfig configures network settings for sandbox.
type SandboxNetworkConfig struct {
	AllowedDomains          []string `json:"allowedDomains,omitempty"`
	DeniedDomains           []string `json:"deniedDomains,omitempty"`
	AllowManagedDomainsOnly bool     `json:"allowManagedDomainsOnly,omitempty"`
	AllowUnixSockets        []string `json:"allowUnixSockets,omitempty"`
	AllowAllUnixSockets     bool     `json:"allowAllUnixSockets,omitempty"`
	AllowLocalBinding       bool     `json:"allowLocalBinding,omitempty"`
	AllowMachLookup         []string `json:"allowMachLookup,omitempty"`
	HTTPProxyPort           int      `json:"httpProxyPort,omitempty"`
	SOCKSProxyPort          int      `json:"socksProxyPort,omitempty"`
}

// SandboxIgnoreViolations specifies sandbox violations to leave unreported.
//
// It maps a command pattern to a list of strings. A violation is left
// unreported when the command's text contains the pattern and the
// violation's text contains one of the strings. The pattern "*" matches
// every command. Both checks are plain substring matches, not globs.
//
// For example, SandboxIgnoreViolations{"*": {"/etc/hosts"}} leaves
// violations that mention /etc/hosts unreported for every command.
//
// This only affects which violations are reported, not what the sandbox
// blocks. (The CLI's settings schema is Record<string, string[]>: keys are
// command patterns, so the earlier file/network struct shape was read by
// the CLI as the command patterns "file" and "network".)
type SandboxIgnoreViolations map[string][]string

// PluginConfig configures a plugin.
type PluginConfig struct {
	Type string `json:"type"` // "local"
	Path string `json:"path"`
}

// ThinkingConfig represents thinking configuration.
type ThinkingConfig struct {
	Type         string `json:"type"` // "adaptive", "enabled", "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	// Display controls how thinking text is surfaced: "summarized" or "omitted".
	// Only valid for "adaptive" and "enabled" types.
	Display string `json:"display,omitempty"` // "summarized" or "omitted"
}

// CanUseToolFunc is the callback type for tool permission requests.
type CanUseToolFunc func(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error)

// ToolPermissionContext provides context for tool permission callbacks.
type ToolPermissionContext struct {
	Signal         interface{}        // Future: abort signal support
	Suggestions    []PermissionUpdate // Permission suggestions from CLI
	ToolUseID      string             // Unique identifier for this specific tool call
	AgentID        string             // If running within a sub-agent, the sub-agent's ID
	BlockedPath    string             // File path that triggered the permission request
	DecisionReason string             // Explains why this permission request was triggered
	Title          string             // Full permission prompt sentence (e.g. "Claude wants to read foo.txt")
	DisplayName    string             // Short noun phrase for the tool action (e.g. "Read file")
	Description    string             // Human-readable subtitle for the permission UI
}

// PermissionResult is the interface for permission results.
type PermissionResult interface {
	permissionResultMarker()
}

// PermissionResultAllow allows the tool execution.
type PermissionResultAllow struct {
	Behavior           string                 `json:"behavior"` // "allow"
	UpdatedInput       map[string]interface{} `json:"updated_input,omitempty"`
	UpdatedPermissions []PermissionUpdate     `json:"updated_permissions,omitempty"`
}

func (r *PermissionResultAllow) permissionResultMarker() {}

// PermissionResultDeny denies the tool execution.
type PermissionResultDeny struct {
	Behavior  string `json:"behavior"` // "deny"
	Message   string `json:"message,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
}

func (r *PermissionResultDeny) permissionResultMarker() {}

// PermissionUpdate represents a permission update.
type PermissionUpdate struct {
	Type        string                `json:"type"` // "addRules", "replaceRules", "removeRules", "setMode", "addDirectories", "removeDirectories"
	Rules       []PermissionRuleValue `json:"rules,omitempty"`
	Behavior    string                `json:"behavior,omitempty"` // "allow", "deny", "ask"
	Mode        PermissionMode        `json:"mode,omitempty"`
	Directories []string              `json:"directories,omitempty"`
	Destination string                `json:"destination,omitempty"` // "userSettings", "projectSettings", "localSettings", "session"
}

// PermissionRuleValue represents a permission rule.
type PermissionRuleValue struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"`
}

// HookMatcher configures which hooks to invoke.
type HookMatcher struct {
	Matcher string         // Tool name pattern (e.g., "Bash", "Write|MultiEdit|Edit")
	Hooks   []HookCallback // Hook callback functions
	Timeout time.Duration  // Timeout for hooks in this matcher
}

// HookCallback is the function signature for hook handlers.
type HookCallback func(input HookInput, toolUseID string, ctx HookContext) (HookOutput, error)

// HookContext provides context for hook callbacks.
type HookContext struct {
	Signal interface{} // Future: abort signal support
}

// HookEffort is the reasoning effort in effect when a hook fires.
type HookEffort struct {
	// Level is the effort level for the current turn (e.g. "low", "high",
	// "max"), after any downgrade for the selected model.
	Level EffortLevel `json:"level"`
}

// BackgroundTaskSummary is an in-flight background task, as listed on Stop
// and SubagentStop hook inputs.
//
// Description and Command are capped at 1000 characters by the CLI; a
// clipped value ends with an in-string "… [+N chars]" marker, so it is not
// always the original text.
type BackgroundTaskSummary struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
	// Command is the shell command line. Only present for "shell" tasks.
	Command string `json:"command,omitempty"`
	// AgentType is the subagent type name. Only present for "subagent"
	// tasks.
	AgentType string `json:"agent_type,omitempty"`
	// Server is the MCP server name. Only present for "monitor" / MCP tasks.
	Server string `json:"server,omitempty"`
	// Tool is the MCP tool name. Only present for "monitor" / MCP tasks.
	Tool string `json:"tool,omitempty"`
	// Name is the workflow name. Only present for "workflow" tasks.
	Name string `json:"name,omitempty"`
}

// SessionCronSummary is a session-scoped scheduled wakeup, as listed on Stop
// and SubagentStop hook inputs.
type SessionCronSummary struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	// Recurring is false for one-shot wakeups and true for tasks that
	// re-fire on every match.
	Recurring bool `json:"recurring"`
	// Prompt is the prompt submitted when the cron fires. Capped at 1000
	// characters by the CLI; a clipped value ends with an in-string
	// "… [+N chars]" marker.
	Prompt string `json:"prompt"`
}

// HookInput contains data passed to hook callbacks.
type HookInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
	// PromptID is the UUID of the user prompt being processed. Matches the
	// prompt.id attribute on OpenTelemetry events. Empty until the first
	// user input.
	PromptID string `json:"prompt_id,omitempty"`
	// Effort is the effort in effect for the current turn. Present for hooks
	// that fire within a tool-use context (PreToolUse, PostToolUse, Stop,
	// SubagentStop, etc.) on models that support effort.
	Effort       *HookEffort            `json:"effort,omitempty"`
	ToolName     string                 `json:"tool_name,omitempty"`     // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest (includes agent_id/agent_type for subagents)
	ToolInput    map[string]interface{} `json:"tool_input,omitempty"`    // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	ToolResponse interface{}            `json:"tool_response,omitempty"` // PostToolUse
	ToolUseID    string                 `json:"tool_use_id,omitempty"`   // PreToolUse, PostToolUse, PostToolUseFailure
	Error        string                 `json:"error,omitempty"`         // PostToolUseFailure
	IsInterrupt  bool                   `json:"is_interrupt,omitempty"`  // PostToolUseFailure
	// DurationMS is the tool execution time in milliseconds. Excludes
	// permission-prompt and hook time. PostToolUse, PostToolUseFailure.
	DurationMS            int                      `json:"duration_ms,omitempty"`
	Prompt                string                   `json:"prompt,omitempty"`                 // UserPromptSubmit
	StopHookActive        bool                     `json:"stop_hook_active,omitempty"`       // Stop, SubagentStop
	AgentID               string                   `json:"agent_id,omitempty"`               // SubagentStop, SubagentStart, PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	AgentTranscriptPath   string                   `json:"agent_transcript_path,omitempty"`  // SubagentStop
	AgentType             string                   `json:"agent_type,omitempty"`             // SubagentStop, SubagentStart, PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	Trigger               string                   `json:"trigger,omitempty"`                // PreCompact: "manual" or "auto"
	CustomInstructions    string                   `json:"custom_instructions,omitempty"`    // PreCompact
	Message               string                   `json:"message,omitempty"`                // Notification
	Title                 string                   `json:"title,omitempty"`                  // Notification
	NotificationType      string                   `json:"notification_type,omitempty"`      // Notification
	PermissionSuggestions []map[string]interface{} `json:"permission_suggestions,omitempty"` // PermissionRequest
	// LastAssistantMessage is the text content of the (sub)agent's last
	// assistant message before stopping. Stop, SubagentStop.
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	// BackgroundTasks is the in-flight background work in this session
	// (scoped to the parent session for SubagentStop). Empty when nothing is
	// in flight. Stop, SubagentStop.
	BackgroundTasks []BackgroundTaskSummary `json:"background_tasks,omitempty"`
	// SessionCrons is the session-scoped scheduled wakeups that will resume
	// this session later (scoped to the parent session for SubagentStop).
	// Empty when none are scheduled. Stop, SubagentStop.
	SessionCrons []SessionCronSummary `json:"session_crons,omitempty"`
}

// HookOutput defines the response from a hook callback.
type HookOutput struct {
	Continue           *bool                  `json:"continue,omitempty"`
	Async              bool                   `json:"async,omitempty"` // Mapped to async_ in Python
	AsyncTimeout       int                    `json:"asyncTimeout,omitempty"`
	SuppressOutput     bool                   `json:"suppressOutput,omitempty"`
	StopReason         string                 `json:"stopReason,omitempty"`
	Decision           string                 `json:"decision,omitempty"` // "block", "defer"
	SystemMessage      string                 `json:"systemMessage,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	HookSpecificOutput map[string]interface{} `json:"hookSpecificOutput,omitempty"`
}

// HookPermissionDecision is a PreToolUse hook's permission decision.
type HookPermissionDecision string

const (
	// HookPermissionDecisionAllow allows the tool call.
	HookPermissionDecisionAllow HookPermissionDecision = "allow"
	// HookPermissionDecisionDeny denies the tool call.
	HookPermissionDecisionDeny HookPermissionDecision = "deny"
	// HookPermissionDecisionAsk asks the user to decide.
	HookPermissionDecisionAsk HookPermissionDecision = "ask"
	// HookPermissionDecisionDefer defers the tool call (PreToolUse only).
	HookPermissionDecisionDefer HookPermissionDecision = "defer"
)

// ModelSwitchPermissionDecision is a PreModelSwitch hook's decision on the
// pending model switch.
type ModelSwitchPermissionDecision string

const (
	// ModelSwitchPermissionDecisionAllow proceeds with the switch.
	ModelSwitchPermissionDecisionAllow ModelSwitchPermissionDecision = "allow"
	// ModelSwitchPermissionDecisionDeny cancels the switch.
	ModelSwitchPermissionDecisionDeny ModelSwitchPermissionDecision = "deny"
	// ModelSwitchPermissionDecisionAsk is treated as a refusal outside an
	// interactive session.
	ModelSwitchPermissionDecisionAsk ModelSwitchPermissionDecision = "ask"
)

// ElicitationAction answers an MCP elicitation request from a hook.
type ElicitationAction string

const (
	// ElicitationActionAccept submits the form field values.
	ElicitationActionAccept ElicitationAction = "accept"
	// ElicitationActionDecline declines the elicitation request.
	ElicitationActionDecline ElicitationAction = "decline"
	// ElicitationActionCancel cancels the elicitation request.
	ElicitationActionCancel ElicitationAction = "cancel"
)

// HookSpecificOutputMap converts a typed hook-specific output struct (any of
// the *HookSpecificOutput types below) into the map form that
// HookOutput.HookSpecificOutput expects, using the struct's JSON tags.
// Python can put its hookSpecificOutput TypedDicts in HookJSONOutput
// directly; Go's field is a map, so this is the bridge:
//
//	HookSpecificOutput: claude.HookSpecificOutputMap(
//	    claude.StopHookSpecificOutput{AdditionalContext: "one more turn"},
//	)
//
// It panics only if the value cannot be JSON-marshaled — hook-specific
// outputs are plain data, so that is a programming error.
func HookSpecificOutputMap[T any](output T) map[string]interface{} {
	raw, err := json.Marshal(output)
	if err != nil {
		panic(fmt.Sprintf("claude: marshaling hook-specific output: %v", err))
	}
	m := make(map[string]interface{})
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(fmt.Sprintf("claude: unmarshaling hook-specific output: %v", err))
	}
	return m
}

// marshalHookSpecificOutput marshals a hook-specific output struct (passed
// as a method-less alias to avoid recursing through its MarshalJSON) and
// stamps the hookEventName discriminator onto the object — the Go analogue
// of the Python SDK's Literal-typed hookEventName keys, which cannot carry a
// mismatched event name.
func marshalHookSpecificOutput(eventName string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	m["hookEventName"] = eventName
	return json.Marshal(m)
}

// Hook-specific output types. Each is the hookSpecificOutput payload for one
// hook event, discriminated by its HookEventName field. The set mirrors the
// Python SDK's HookSpecificOutput union at v0.2.166.

// PreToolUseHookSpecificOutput is hook-specific output for PreToolUse events.
type PreToolUseHookSpecificOutput struct {
	HookEventName            string                 `json:"hookEventName"` // "PreToolUse"
	PermissionDecision       HookPermissionDecision `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string                 `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]interface{} `json:"updatedInput,omitempty"`
	AdditionalContext        string                 `json:"additionalContext,omitempty"`
}

// PostToolUseHookSpecificOutput is hook-specific output for PostToolUse events.
type PostToolUseHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PostToolUse"
	// AdditionalContext is extra context delivered to the model.
	AdditionalContext string `json:"additionalContext,omitempty"`
	// UpdatedToolOutput replaces the tool output before it is sent to the
	// model. For built-in tools (Bash, Read, Edit, etc.) the value must
	// match the tool's output schema (e.g. {"stdout": ..., "stderr": ...,
	// "interrupted": ...} for Bash); a mismatched shape is rejected and the
	// original output is kept.
	UpdatedToolOutput interface{} `json:"updatedToolOutput,omitempty"`
	// UpdatedMCPToolOutput replaces the output for MCP tools only. Prefer
	// UpdatedToolOutput, which works for all tools.
	UpdatedMCPToolOutput interface{} `json:"updatedMCPToolOutput,omitempty"`
}

// PostToolUseFailureHookSpecificOutput is hook-specific output for
// PostToolUseFailure events.
type PostToolUseFailureHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "PostToolUseFailure"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// UserPromptSubmitHookSpecificOutput is hook-specific output for
// UserPromptSubmit events.
type UserPromptSubmitHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "UserPromptSubmit"
	AdditionalContext string `json:"additionalContext,omitempty"`
	// SessionTitle sets the session title.
	SessionTitle string `json:"sessionTitle,omitempty"`
	// SuppressOriginalPrompt omits the original prompt from the block
	// message when Decision (on HookOutput) is "block".
	SuppressOriginalPrompt *bool `json:"suppressOriginalPrompt,omitempty"`
}

// UserPromptExpansionHookSpecificOutput is hook-specific output for
// UserPromptExpansion events.
type UserPromptExpansionHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "UserPromptExpansion"
	AdditionalContext string `json:"additionalContext,omitempty"`
	// SuppressOriginalPrompt omits the original prompt from the block
	// message when Decision (on HookOutput) is "block".
	SuppressOriginalPrompt *bool `json:"suppressOriginalPrompt,omitempty"`
}

// SessionStartHookSpecificOutput is hook-specific output for SessionStart events.
type SessionStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "SessionStart"
	AdditionalContext string `json:"additionalContext,omitempty"`
	// InitialUserMessage is used as the first user message of the session.
	InitialUserMessage string `json:"initialUserMessage,omitempty"`
	// SessionTitle sets the session title. Ignored when the session starts
	// from clear or compact.
	SessionTitle string `json:"sessionTitle,omitempty"`
	// WatchPaths are absolute paths to watch for FileChanged events during
	// this session.
	WatchPaths []string `json:"watchPaths,omitempty"`
	// ReloadSkills re-scans skill and command directories after
	// SessionStart hooks complete, so skills the hook installed are
	// available in the same session.
	ReloadSkills *bool `json:"reloadSkills,omitempty"`
}

// NotificationHookSpecificOutput is hook-specific output for Notification events.
type NotificationHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "Notification"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// SubagentStartHookSpecificOutput is hook-specific output for SubagentStart
// events.
type SubagentStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "SubagentStart"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// PermissionRequestHookSpecificOutput is hook-specific output for
// PermissionRequest events.
type PermissionRequestHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PermissionRequest"
	// Decision is the programmatic permission decision. Its shape follows
	// the CLI's classifier-verdict schema; required when this output is
	// sent.
	Decision map[string]interface{} `json:"decision"`
}

// PostToolBatchHookSpecificOutput is hook-specific output for PostToolBatch
// events.
type PostToolBatchHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "PostToolBatch"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// StopHookSpecificOutput is hook-specific output for Stop events.
//
// AdditionalContext is non-error feedback delivered to the model; the
// conversation continues so the model can act on it.
type StopHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "Stop"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// SubagentStopHookSpecificOutput is hook-specific output for SubagentStop
// events.
//
// AdditionalContext is non-error feedback delivered to the subagent; the
// subagent continues so it can act on it.
type SubagentStopHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "SubagentStop"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// PermissionDeniedHookSpecificOutput is hook-specific output for
// PermissionDenied events.
type PermissionDeniedHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PermissionDenied"
	// Retry tells the model it may retry the denied tool call. Ignored for
	// denials without a classifier verdict.
	Retry *bool `json:"retry,omitempty"`
}

// PreModelSwitchHookSpecificOutput is hook-specific output for
// PreModelSwitch events.
type PreModelSwitchHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PreModelSwitch"`
	// PermissionDecision: "allow" proceeds, "deny" cancels the switch; "ask"
	// is treated as a refusal outside an interactive session.
	PermissionDecision       ModelSwitchPermissionDecision `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string                        `json:"permissionDecisionReason,omitempty"`
}

// PostModelSwitchHookSpecificOutput is hook-specific output for
// PostModelSwitch events.
type PostModelSwitchHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PostModelSwitch"
	// AdditionalContext reaches the model with the next request the new
	// model serves.
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// ElicitationHookSpecificOutput is hook-specific output for Elicitation
// events.
//
// Return this to accept or decline an MCP elicitation request
// programmatically.
type ElicitationHookSpecificOutput struct {
	HookEventName string            `json:"hookEventName"` // "Elicitation"
	Action        ElicitationAction `json:"action,omitempty"`
	// Content holds the form field values to submit. Only used when Action
	// is "accept".
	Content map[string]interface{} `json:"content,omitempty"`
}

// ElicitationResultHookSpecificOutput is hook-specific output for
// ElicitationResult events.
//
// Return this to override the action or content before the response is sent
// to the MCP server.
type ElicitationResultHookSpecificOutput struct {
	HookEventName string                 `json:"hookEventName"` // "ElicitationResult"
	Action        ElicitationAction      `json:"action,omitempty"`
	Content       map[string]interface{} `json:"content,omitempty"`
}

// CwdChangedHookSpecificOutput is hook-specific output for CwdChanged events.
type CwdChangedHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "CwdChanged"
	// WatchPaths are absolute paths to watch for FileChanged events.
	// Replaces the current dynamic watch list.
	WatchPaths []string `json:"watchPaths,omitempty"`
}

// FileChangedHookSpecificOutput is hook-specific output for FileChanged events.
type FileChangedHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "FileChanged"
	// WatchPaths are absolute paths to watch for FileChanged events.
	// Replaces the current dynamic watch list.
	WatchPaths []string `json:"watchPaths,omitempty"`
}

// WorktreeCreateHookSpecificOutput is hook-specific output for
// WorktreeCreate events.
type WorktreeCreateHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "WorktreeCreate"
	// WorktreePath is the absolute path to the created worktree directory.
	// Required when this output is sent.
	WorktreePath string `json:"worktreePath"`
}

// MessageDisplayHookSpecificOutput is hook-specific output for
// MessageDisplay events.
//
// Display-only: replaces the text on screen without changing the stored
// message or what the model sees.
type MessageDisplayHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "MessageDisplay"
	// DisplayContent is the text displayed in place of the delta. Leave
	// empty to display the original.
	DisplayContent string `json:"displayContent,omitempty"`
}

// Each hook-specific output marshals with its own event name stamped on the
// hookEventName key, mirroring the Literal-typed discriminators of the
// Python SDK's HookSpecificOutput union. A caller-set HookEventName value is
// overwritten, so an output can never carry a mismatched event name.

// MarshalJSON implements json.Marshaler for PreToolUseHookSpecificOutput.
func (o PreToolUseHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PreToolUseHookSpecificOutput
	return marshalHookSpecificOutput("PreToolUse", plain(o))
}

// MarshalJSON implements json.Marshaler for PostToolUseHookSpecificOutput.
func (o PostToolUseHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PostToolUseHookSpecificOutput
	return marshalHookSpecificOutput("PostToolUse", plain(o))
}

// MarshalJSON implements json.Marshaler for PostToolUseFailureHookSpecificOutput.
func (o PostToolUseFailureHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PostToolUseFailureHookSpecificOutput
	return marshalHookSpecificOutput("PostToolUseFailure", plain(o))
}

// MarshalJSON implements json.Marshaler for UserPromptSubmitHookSpecificOutput.
func (o UserPromptSubmitHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain UserPromptSubmitHookSpecificOutput
	return marshalHookSpecificOutput("UserPromptSubmit", plain(o))
}

// MarshalJSON implements json.Marshaler for UserPromptExpansionHookSpecificOutput.
func (o UserPromptExpansionHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain UserPromptExpansionHookSpecificOutput
	return marshalHookSpecificOutput("UserPromptExpansion", plain(o))
}

// MarshalJSON implements json.Marshaler for SessionStartHookSpecificOutput.
func (o SessionStartHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain SessionStartHookSpecificOutput
	return marshalHookSpecificOutput("SessionStart", plain(o))
}

// MarshalJSON implements json.Marshaler for NotificationHookSpecificOutput.
func (o NotificationHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain NotificationHookSpecificOutput
	return marshalHookSpecificOutput("Notification", plain(o))
}

// MarshalJSON implements json.Marshaler for SubagentStartHookSpecificOutput.
func (o SubagentStartHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain SubagentStartHookSpecificOutput
	return marshalHookSpecificOutput("SubagentStart", plain(o))
}

// MarshalJSON implements json.Marshaler for PermissionRequestHookSpecificOutput.
func (o PermissionRequestHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PermissionRequestHookSpecificOutput
	return marshalHookSpecificOutput("PermissionRequest", plain(o))
}

// MarshalJSON implements json.Marshaler for PostToolBatchHookSpecificOutput.
func (o PostToolBatchHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PostToolBatchHookSpecificOutput
	return marshalHookSpecificOutput("PostToolBatch", plain(o))
}

// MarshalJSON implements json.Marshaler for StopHookSpecificOutput.
func (o StopHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain StopHookSpecificOutput
	return marshalHookSpecificOutput("Stop", plain(o))
}

// MarshalJSON implements json.Marshaler for SubagentStopHookSpecificOutput.
func (o SubagentStopHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain SubagentStopHookSpecificOutput
	return marshalHookSpecificOutput("SubagentStop", plain(o))
}

// MarshalJSON implements json.Marshaler for PermissionDeniedHookSpecificOutput.
func (o PermissionDeniedHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PermissionDeniedHookSpecificOutput
	return marshalHookSpecificOutput("PermissionDenied", plain(o))
}

// MarshalJSON implements json.Marshaler for PreModelSwitchHookSpecificOutput.
func (o PreModelSwitchHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PreModelSwitchHookSpecificOutput
	return marshalHookSpecificOutput("PreModelSwitch", plain(o))
}

// MarshalJSON implements json.Marshaler for PostModelSwitchHookSpecificOutput.
func (o PostModelSwitchHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain PostModelSwitchHookSpecificOutput
	return marshalHookSpecificOutput("PostModelSwitch", plain(o))
}

// MarshalJSON implements json.Marshaler for ElicitationHookSpecificOutput.
func (o ElicitationHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain ElicitationHookSpecificOutput
	return marshalHookSpecificOutput("Elicitation", plain(o))
}

// MarshalJSON implements json.Marshaler for ElicitationResultHookSpecificOutput.
func (o ElicitationResultHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain ElicitationResultHookSpecificOutput
	return marshalHookSpecificOutput("ElicitationResult", plain(o))
}

// MarshalJSON implements json.Marshaler for CwdChangedHookSpecificOutput.
func (o CwdChangedHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain CwdChangedHookSpecificOutput
	return marshalHookSpecificOutput("CwdChanged", plain(o))
}

// MarshalJSON implements json.Marshaler for FileChangedHookSpecificOutput.
func (o FileChangedHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain FileChangedHookSpecificOutput
	return marshalHookSpecificOutput("FileChanged", plain(o))
}

// MarshalJSON implements json.Marshaler for WorktreeCreateHookSpecificOutput.
func (o WorktreeCreateHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain WorktreeCreateHookSpecificOutput
	return marshalHookSpecificOutput("WorktreeCreate", plain(o))
}

// MarshalJSON implements json.Marshaler for MessageDisplayHookSpecificOutput.
func (o MessageDisplayHookSpecificOutput) MarshalJSON() ([]byte, error) {
	type plain MessageDisplayHookSpecificOutput
	return marshalHookSpecificOutput("MessageDisplay", plain(o))
}

// MCP Server Status Types

// McpServerConnectionStatus represents the connection status of an MCP server.
type McpServerConnectionStatus string

const (
	McpServerStatusConnected McpServerConnectionStatus = "connected"
	McpServerStatusFailed    McpServerConnectionStatus = "failed"
	McpServerStatusNeedsAuth McpServerConnectionStatus = "needs-auth"
	McpServerStatusPending   McpServerConnectionStatus = "pending"
	McpServerStatusDisabled  McpServerConnectionStatus = "disabled"
)

// McpToolAnnotations represents optional hints for tool usage.
type McpToolAnnotations struct {
	ReadOnly    bool `json:"readOnly,omitempty"`
	Destructive bool `json:"destructive,omitempty"`
	OpenWorld   bool `json:"openWorld,omitempty"`
}

// McpToolInfo represents information about an MCP tool.
type McpToolInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Annotations *McpToolAnnotations `json:"annotations,omitempty"`
}

// McpServerInfo represents server information from an MCP server.
type McpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// McpSdkServerConfigStatus represents the status configuration for an SDK MCP server.
type McpSdkServerConfigStatus struct {
	Type string `json:"type"` // "sdk"
	Name string `json:"name"`
}

// McpClaudeAIProxyServerConfig represents the configuration for a Claude.ai proxy server.
type McpClaudeAIProxyServerConfig struct {
	Type string `json:"type"` // "claudeai-proxy"
	URL  string `json:"url"`
	ID   string `json:"id"`
}

// McpServerStatusConfig is a union type for MCP server status configurations.
type McpServerStatusConfig interface {
	mcpServerStatusConfigMarker()
}

func (c *McpSdkServerConfigStatus) mcpServerStatusConfigMarker()     {}
func (c *McpClaudeAIProxyServerConfig) mcpServerStatusConfigMarker() {}

// McpServerStatus represents the status of an MCP server.
type McpServerStatus struct {
	Name       string                    `json:"name"`
	Status     McpServerConnectionStatus `json:"status"`
	ServerInfo *McpServerInfo            `json:"serverInfo,omitempty"`
	Error      string                    `json:"error,omitempty"`
	Config     map[string]interface{}    `json:"config,omitempty"`
	Scope      string                    `json:"scope,omitempty"`
	Tools      []McpToolInfo             `json:"tools,omitempty"`
}

// McpStatusResponse represents the response from an MCP status request.
type McpStatusResponse struct {
	MCPServers []McpServerStatus `json:"mcpServers"`
}

// SDK Control Request Types for MCP

// SDKControlMcpReconnectRequest requests reconnection to an MCP server.
type SDKControlMcpReconnectRequest struct {
	Subtype    string `json:"subtype"` // "mcp_reconnect"
	ServerName string `json:"serverName"`
}

// SDKControlMcpToggleRequest requests toggling an MCP server on/off.
type SDKControlMcpToggleRequest struct {
	Subtype    string `json:"subtype"` // "mcp_toggle"
	ServerName string `json:"serverName"`
	Enabled    bool   `json:"enabled"`
}

// SDKControlStopTaskRequest requests stopping a running task.
type SDKControlStopTaskRequest struct {
	Subtype string `json:"subtype"` // "stop_task"
	TaskID  string `json:"task_id"`
}

// Session Management Types

// SessionMessageType represents the type of a session message.
type SessionMessageType string

const (
	SessionMessageTypeUser      SessionMessageType = "user"
	SessionMessageTypeAssistant SessionMessageType = "assistant"
)

// SDKSessionInfo contains metadata about a Claude Code session.
type SDKSessionInfo struct {
	SessionID    string  `json:"session_id"`
	Summary      string  `json:"summary"`
	LastModified int64   `json:"last_modified"`
	FileSize     *int64  `json:"file_size,omitempty"`
	CustomTitle  *string `json:"custom_title,omitempty"`
	FirstPrompt  *string `json:"first_prompt,omitempty"`
	GitBranch    *string `json:"git_branch,omitempty"`
	CWD          *string `json:"cwd,omitempty"`
	Tag          *string `json:"tag,omitempty"`
	CreatedAt    *int64  `json:"created_at,omitempty"`
}

// SessionMessage represents a message in a session's conversation.
type SessionMessage struct {
	Type      SessionMessageType `json:"type"`
	UUID      string             `json:"uuid"`
	SessionID string             `json:"session_id"`
	Message   interface{}        `json:"message"`
	// ParentToolUseID is, for messages returned by GetSubagentMessages /
	// GetSubagentMessagesFromStore, the id of the Agent tool_use block in the
	// parent session that spawned the subagent (recovered from the subagent's
	// metadata; nil if that metadata is unavailable). Always nil for
	// top-level GetSessionMessages / GetSessionMessagesFromStore results.
	ParentToolUseID *string `json:"parent_tool_use_id,omitempty"`
	// ParentAgentID is, for subagent messages, the agent id of the subagent
	// that spawned this subagent, or nil if it was spawned by the main
	// session (or the metadata is unavailable). Always nil for top-level
	// session messages.
	ParentAgentID *string `json:"parent_agent_id,omitempty"`
}

// ServerToolName identifies a server-side tool (e.g. advisor, web_search).
type ServerToolName = string

const (
	ServerToolNameAdvisor                 = "advisor"
	ServerToolNameWebSearch               = "web_search"
	ServerToolNameWebFetch                = "web_fetch"
	ServerToolNameCodeExecution           = "code_execution"
	ServerToolNameBashCodeExecution       = "bash_code_execution"
	ServerToolNameTextEditorCodeExecution = "text_editor_code_execution"
	ServerToolNameToolSearchRegex         = "tool_search_tool_regex"
	ServerToolNameToolSearchBM25          = "tool_search_tool_bm25"
)

// ServerToolUseBlock represents a server-side tool use content block (e.g. advisor, web_search).
//
// These are tools the API executes server-side on the model's behalf, so they
// appear in the message stream alongside regular tool_use blocks but the
// caller never needs to return a result. Name is a discriminator — branch on
// it to know which server tool was invoked.
type ServerToolUseBlock struct {
	ID    string                 `json:"id"`
	Name  ServerToolName         `json:"name"`
	Input map[string]interface{} `json:"input"`
}

func (b *ServerToolUseBlock) contentBlockMarker() {}

// MarshalJSON implements json.Marshaler for ServerToolUseBlock.
func (b *ServerToolUseBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type":  "server_tool_use",
		"id":    b.ID,
		"name":  b.Name,
		"input": b.Input,
	})
}

// ServerToolResultBlock represents the result of a server-side tool call.
//
// Content is the raw dict from the API, opaque to this layer — callers that
// care about a specific server tool's result schema can inspect Content["type"].
type ServerToolResultBlock struct {
	ToolUseID string                 `json:"tool_use_id"`
	Content   map[string]interface{} `json:"content"`
}

func (b *ServerToolResultBlock) contentBlockMarker() {}

// MarshalJSON implements json.Marshaler for ServerToolResultBlock.
func (b *ServerToolResultBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type":        "advisor_tool_result",
		"tool_use_id": b.ToolUseID,
		"content":     b.Content,
	})
}

// MirrorErrorMessage is a system message emitted when a SessionStore.Append call fails.
//
// Non-fatal — the local-disk transcript is already durable, so the session
// continues unaffected. The mirrored copy in the external store will be
// missing the failed batch.
type MirrorErrorMessage struct {
	Subtype   string      `json:"subtype"` // always "mirror_error"
	Data      interface{} `json:"data"`
	Key       *SessionKey `json:"key,omitempty"`
	Error     string      `json:"error"`
	UUID      string      `json:"uuid,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
}

func (m *MirrorErrorMessage) messageMarker() {}

// HookEventMessage is a hook event emitted by the CLI when IncludeHookEvents is enabled.
//
// When ClaudeAgentOptions.IncludeHookEvents is true, the CLI emits hook lifecycle
// events (PreToolUse, PostToolUse, Stop, etc.) into the message stream. Each event
// is identified by HookEventName and the full raw payload is available in Data.
//
// These arrive on the wire as {"type": "system", "subtype": "hook_started" | "hook_response", ...}.
type HookEventMessage struct {
	Subtype       string                 `json:"subtype"` // "hook_started" or "hook_response"
	HookEventName string                 `json:"hook_event_name"`
	Data          map[string]interface{} `json:"data"`
	SessionID     string                 `json:"session_id,omitempty"`
	UUID          string                 `json:"uuid,omitempty"`
}

func (m *HookEventMessage) messageMarker() {}

// ---------------------------------------------------------------------------
// Session Store Types (ported from Python SDK 0.1.64)
// ---------------------------------------------------------------------------

// SessionKey identifies a session transcript or subagent transcript in a store.
//
// Main transcripts have an empty Subpath; subagent transcripts include a Subpath
// like "subagents/agent-{id}" that mirrors the on-disk directory structure.
type SessionKey struct {
	ProjectKey string `json:"project_key"`
	SessionID  string `json:"session_id"`
	// Subpath is empty for the main transcript; set for subagent files.
	// Opaque to the adapter — just use it as a storage key suffix.
	Subpath string `json:"subpath,omitempty"`
}

// SessionStoreEntry is one JSONL transcript line as observed by a SessionStore adapter.
// Adapters should treat entries as pass-through blobs; round-tripping via JSON
// is the only required invariant.
type SessionStoreEntry = map[string]interface{}

// SessionStoreListEntry is an entry returned by SessionStore.ListSessions.
type SessionStoreListEntry struct {
	SessionID string `json:"session_id"`
	// Mtime is the last-modified time in Unix epoch milliseconds.
	Mtime int64 `json:"mtime"`
}

// SessionSummaryEntry is an incrementally-maintained session summary.
//
// Stores obtain this from FoldSessionSummary inside Append and persist it
// verbatim; they return the full set from ListSessionSummaries. The Data
// field is opaque SDK-owned state — stores MUST NOT interpret it.
type SessionSummaryEntry struct {
	SessionID string `json:"session_id"`
	// Mtime is the storage write time of the sidecar, in Unix epoch milliseconds.
	Mtime int64 `json:"mtime"`
	// Data is opaque SDK-owned summary state. Persist verbatim; do not interpret.
	Data map[string]interface{} `json:"data"`
}

// SessionListSubkeysKey is a key argument to SessionStore.ListSubkeys (no Subpath).
type SessionListSubkeysKey struct {
	ProjectKey string `json:"project_key"`
	SessionID  string `json:"session_id"`
}

// SessionStore is the interface for adapters that mirror session transcripts to external storage.
//
// The subprocess still writes to local disk; the adapter receives a secondary copy.
// Only Append and Load are required. The remaining methods are optional.
type SessionStore interface {
	// Append mirrors a batch of transcript entries.
	// Called AFTER the subprocess's local write succeeds — durability is
	// already guaranteed locally.
	Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error

	// Load loads a full session for resume.
	// Return nil for a key that was never written.
	Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error)

	// ListSessions lists sessions for a project_key. Returns IDs + modification times.
	// Optional — if unimplemented, list operations raise.
	ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error)

	// ListSessionSummaries returns incrementally-maintained summaries for all
	// sessions in one call. Optional.
	ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error)

	// Delete deletes a session. Deleting a main-transcript key (Subpath=="")
	// must cascade to all subkeys so subagent transcripts aren't orphaned.
	// Optional.
	Delete(ctx context.Context, key SessionKey) error

	// ListSubkeys lists the subpath keys for a session (e.g. subagent transcripts).
	// Optional.
	ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error)
}

// BaseSessionStore provides default "not implemented" implementations of all
// SessionStore methods. Embed *BaseSessionStore (or BaseSessionStore) in your
// custom store struct so you only need to override the methods you support.
//
//	type MyStore struct {
//	    claude.BaseSessionStore
//	    // ... your fields
//	}
//
//	func (s *MyStore) Append(ctx context.Context, key claude.SessionKey, entries []claude.SessionStoreEntry) error { ... }
//	func (s *MyStore) Load(ctx context.Context, key claude.SessionKey) ([]claude.SessionStoreEntry, error) { ... }
//	// ListSessions, ListSessionSummaries, Delete, ListSubkeys are inherited
//	// from BaseSessionStore and return ErrNotImplemented.
type BaseSessionStore struct{}

func (*BaseSessionStore) Append(_ context.Context, _ SessionKey, _ []SessionStoreEntry) error {
	return ErrNotImplemented
}
func (*BaseSessionStore) Load(_ context.Context, _ SessionKey) ([]SessionStoreEntry, error) {
	return nil, ErrNotImplemented
}
func (*BaseSessionStore) ListSessions(_ context.Context, _ string) ([]SessionStoreListEntry, error) {
	return nil, ErrNotImplemented
}
func (*BaseSessionStore) ListSessionSummaries(_ context.Context, _ string) ([]SessionSummaryEntry, error) {
	return nil, ErrNotImplemented
}
func (*BaseSessionStore) Delete(_ context.Context, _ SessionKey) error {
	return ErrNotImplemented
}
func (*BaseSessionStore) ListSubkeys(_ context.Context, _ SessionListSubkeysKey) ([]string, error) {
	return nil, ErrNotImplemented
}

// SessionStoreFlushMode controls when transcript-mirror entries are flushed to
// a SessionStore.
//
//   - "batched" (default): buffer entries and flush once per turn (on the
//     result message) or when the pending buffer exceeds thresholds. Keeps
//     adapter latency off the streaming hot path.
//   - "eager": trigger a background flush after every transcript_mirror frame
//     so SessionStore.Append sees entries in near real time. Appends are still
//     serialized in enqueue order; a slow adapter will not stall the read loop.
type SessionStoreFlushMode = string

const (
	// SessionStoreFlushModeBatched buffers entries and flushes once per turn.
	// This is the default behavior.
	SessionStoreFlushModeBatched SessionStoreFlushMode = "batched"
	// SessionStoreFlushModeEager triggers a flush after every frame for
	// near-real-time delivery to the SessionStore.
	SessionStoreFlushModeEager SessionStoreFlushMode = "eager"
)
