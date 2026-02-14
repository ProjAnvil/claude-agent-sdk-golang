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
)

// SettingSource specifies which setting sources to load.
type SettingSource string

const (
	SettingSourceUser    SettingSource = "user"
	SettingSourceProject SettingSource = "project"
	SettingSourceLocal   SettingSource = "local"
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
	Content         []ContentBlock `json:"content"`
	Model           string         `json:"model"`
	ParentToolUseID string         `json:"parent_tool_use_id,omitempty"`
	Error           string         `json:"error,omitempty"`
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
	Subtype          string                 `json:"subtype"`
	DurationMS       int                    `json:"duration_ms"`
	DurationAPIMS    int                    `json:"duration_api_ms"`
	IsError          bool                   `json:"is_error"`
	NumTurns         int                    `json:"num_turns"`
	SessionID        string                 `json:"session_id"`
	TotalCostUSD     float64                `json:"total_cost_usd,omitempty"`
	Usage            map[string]interface{} `json:"usage,omitempty"`
	Result           string                 `json:"result,omitempty"`
	StructuredOutput interface{}            `json:"structured_output,omitempty"`
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
	Type   string `json:"type"`   // "preset"
	Preset string `json:"preset"` // "claude_code"
	Append string `json:"append,omitempty"`
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

// AgentDefinition defines a custom agent configuration.
type AgentDefinition struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"` // "sonnet", "opus", "haiku", "inherit"
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
	ToolName              string                 `json:"tool_name,omitempty"`              // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	ToolInput             map[string]interface{} `json:"tool_input,omitempty"`             // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	ToolResponse          interface{}            `json:"tool_response,omitempty"`          // PostToolUse
	ToolUseID             string                 `json:"tool_use_id,omitempty"`            // PreToolUse, PostToolUse, PostToolUseFailure
	Error                 string                 `json:"error,omitempty"`                  // PostToolUseFailure
	IsInterrupt           bool                   `json:"is_interrupt,omitempty"`           // PostToolUseFailure
	Prompt                string                 `json:"prompt,omitempty"`                 // UserPromptSubmit
	StopHookActive        bool                   `json:"stop_hook_active,omitempty"`       // Stop, SubagentStop
	AgentID               string                 `json:"agent_id,omitempty"`               // SubagentStop, SubagentStart
	AgentTranscriptPath   string                 `json:"agent_transcript_path,omitempty"`  // SubagentStop
	AgentType             string                 `json:"agent_type,omitempty"`             // SubagentStop, SubagentStart
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
