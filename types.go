package claude

import (
	"context"
	"encoding/json"
	"errors"
	"time"
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

// UserMessage represents a user message.
type UserMessage struct {
	Content         interface{}            `json:"content"` // string or []ContentBlock
	UUID            string                 `json:"uuid,omitempty"`
	ParentToolUseID string                 `json:"parent_tool_use_id,omitempty"`
	ToolUseResult   map[string]interface{} `json:"tool_use_result,omitempty"`
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
type SystemMessage struct {
	Subtype string                 `json:"subtype"`
	Data    map[string]interface{} `json:"data"`
}

func (m *SystemMessage) messageMarker() {}

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
	ModelUsage        map[string]interface{} `json:"model_usage,omitempty"`
	PermissionDenials []interface{}          `json:"permission_denials,omitempty"`
	Errors            []string               `json:"errors,omitempty"`
	UUID              string                 `json:"uuid,omitempty"`
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
			continue
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
	Type     string      `json:"type"` // "sdk"
	Name     string      `json:"name"`
	Instance interface{} `json:"-"` // The actual server instance (not serialized)
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
	Effort          interface{}    `json:"effort,omitempty"`         // "low", "medium", "high", "max", or int
	PermissionMode  PermissionMode `json:"permissionMode,omitempty"` // e.g. PermissionModeDefault
}

// SandboxSettings configures bash command sandboxing.
type SandboxSettings struct {
	Enabled                   bool                     `json:"enabled,omitempty"`
	AutoAllowBashIfSandboxed  bool                     `json:"autoAllowBashIfSandboxed,omitempty"`
	ExcludedCommands          []string                 `json:"excludedCommands,omitempty"`
	AllowUnsandboxedCommands  bool                     `json:"allowUnsandboxedCommands,omitempty"`
	Network                   *SandboxNetworkConfig    `json:"network,omitempty"`
	IgnoreViolations          *SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	EnableWeakerNestedSandbox bool                     `json:"enableWeakerNestedSandbox,omitempty"`
}

// SandboxNetworkConfig configures network settings for sandbox.
type SandboxNetworkConfig struct {
	AllowUnixSockets    []string `json:"allowUnixSockets,omitempty"`
	AllowAllUnixSockets bool     `json:"allowAllUnixSockets,omitempty"`
	AllowLocalBinding   bool     `json:"allowLocalBinding,omitempty"`
	HTTPProxyPort       int      `json:"httpProxyPort,omitempty"`
	SOCKSProxyPort      int      `json:"socksProxyPort,omitempty"`
}

// SandboxIgnoreViolations specifies violations to ignore.
type SandboxIgnoreViolations struct {
	File    []string `json:"file,omitempty"`
	Network []string `json:"network,omitempty"`
}

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
	Signal      interface{}        // Future: abort signal support
	Suggestions []PermissionUpdate // Permission suggestions from CLI
	ToolUseID   string             // Unique identifier for this specific tool call
	AgentID     string             // If running within a sub-agent, the sub-agent's ID
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

// HookInput contains data passed to hook callbacks.
type HookInput struct {
	HookEventName         string                 `json:"hook_event_name"`
	SessionID             string                 `json:"session_id"`
	TranscriptPath        string                 `json:"transcript_path"`
	CWD                   string                 `json:"cwd"`
	PermissionMode        string                 `json:"permission_mode,omitempty"`
	ToolName              string                 `json:"tool_name,omitempty"`              // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest (includes agent_id/agent_type for subagents)
	ToolInput             map[string]interface{} `json:"tool_input,omitempty"`             // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	ToolResponse          interface{}            `json:"tool_response,omitempty"`          // PostToolUse
	ToolUseID             string                 `json:"tool_use_id,omitempty"`            // PreToolUse, PostToolUse, PostToolUseFailure
	Error                 string                 `json:"error,omitempty"`                  // PostToolUseFailure
	IsInterrupt           bool                   `json:"is_interrupt,omitempty"`           // PostToolUseFailure
	Prompt                string                 `json:"prompt,omitempty"`                 // UserPromptSubmit
	StopHookActive        bool                   `json:"stop_hook_active,omitempty"`       // Stop, SubagentStop
	AgentID               string                 `json:"agent_id,omitempty"`               // SubagentStop, SubagentStart, PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	AgentTranscriptPath   string                 `json:"agent_transcript_path,omitempty"`  // SubagentStop
	AgentType             string                 `json:"agent_type,omitempty"`             // SubagentStop, SubagentStart, PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	Trigger               string                 `json:"trigger,omitempty"`                // PreCompact: "manual" or "auto"
	CustomInstructions    string                 `json:"custom_instructions,omitempty"`    // PreCompact
	Message               string                 `json:"message,omitempty"`                // Notification
	Title                 string                 `json:"title,omitempty"`                  // Notification
	NotificationType      string                 `json:"notification_type,omitempty"`      // Notification
	PermissionSuggestions []interface{}          `json:"permission_suggestions,omitempty"` // PermissionRequest
}

// HookOutput defines the response from a hook callback.
type HookOutput struct {
	Continue           *bool                  `json:"continue,omitempty"`
	Async              bool                   `json:"async,omitempty"` // Mapped to async_ in Python
	AsyncTimeout       int                    `json:"asyncTimeout,omitempty"`
	SuppressOutput     bool                   `json:"suppressOutput,omitempty"`
	StopReason         string                 `json:"stopReason,omitempty"`
	Decision           string                 `json:"decision,omitempty"` // "block"
	SystemMessage      string                 `json:"systemMessage,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	HookSpecificOutput map[string]interface{} `json:"hookSpecificOutput,omitempty"`
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
	Type            SessionMessageType `json:"type"`
	UUID            string             `json:"uuid"`
	SessionID       string             `json:"session_id"`
	Message         interface{}        `json:"message"`
	ParentToolUseID *string            `json:"parent_tool_use_id,omitempty"`
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
