package claude

import (
	"encoding/json"
	"time"
)

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
	// PermissionModeDontAsk allows all tools without prompting.
	PermissionModeDontAsk PermissionMode = "dontAsk"
	// PermissionModeAuto automatically decides tool permissions.
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
	Type                   string `json:"type"`                              // "preset"
	Preset                 string `json:"preset"`                            // "claude_code"
	Append                 string `json:"append,omitempty"`
	ExcludeDynamicSections *bool  `json:"exclude_dynamic_sections,omitempty"` // strip per-user dynamic sections for cross-user prompt caching
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
	Description     string        `json:"description"`
	Prompt          string        `json:"prompt"`
	Tools           []string      `json:"tools,omitempty"`
	DisallowedTools []string      `json:"disallowedTools,omitempty"`
	Model           string        `json:"model,omitempty"`
	Skills          []string      `json:"skills,omitempty"`
	Memory          string        `json:"memory,omitempty"` // "user", "project", "local"
	McpServers      []interface{} `json:"mcpServers,omitempty"`
	InitialPrompt   string        `json:"initialPrompt,omitempty"`
	MaxTurns        *int          `json:"maxTurns,omitempty"`
	Background      *bool          `json:"background,omitempty"`
	Effort          string         `json:"effort,omitempty"`         // "low", "medium", "high", "max"
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
