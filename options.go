package claude

import "fmt"

// ClaudeAgentOptions configures SDK behavior.
type ClaudeAgentOptions struct {
	// Tools specifies the base set of tools available.
	Tools []string
	// ToolsPreset specifies a preset for tools (e.g., "claude_code").
	ToolsPreset *ToolsPreset
	// AllowedTools specifies which tools are allowed.
	AllowedTools []string
	// SystemPrompt sets a custom system prompt.
	SystemPrompt string
	// SystemPromptPreset specifies a system prompt preset.
	SystemPromptPreset *SystemPromptPreset
	// SystemPromptFile specifies a system prompt from a file.
	SystemPromptFile *SystemPromptFile
	// MCPServers configures MCP servers (external or SDK).
	MCPServers map[string]MCPServerConfig
	// PermissionMode sets the permission level for tool execution.
	PermissionMode PermissionMode
	// ContinueConversation continues an existing conversation.
	ContinueConversation bool
	// Resume resumes a specific session.
	Resume string
	// SessionID specifies a session ID to use.
	SessionID string
	// MaxTurns limits the number of conversation turns.
	MaxTurns int
	// MaxBudgetUSD sets a cost limit in USD.
	MaxBudgetUSD float64
	// DisallowedTools specifies which tools are not allowed.
	DisallowedTools []string
	// Model specifies the AI model to use.
	Model string
	// FallbackModel specifies a fallback model.
	FallbackModel string
	// Betas enables beta features.
	Betas []SdkBeta
	// PermissionPromptToolName sets the tool for permission prompts.
	PermissionPromptToolName string
	// CWD sets the working directory for the CLI.
	CWD string
	// CLIPath specifies a custom path to the Claude CLI.
	CLIPath string
	// Settings specifies settings as JSON string or file path.
	Settings string
	// AddDirs adds additional directories.
	AddDirs []string
	// Env sets environment variables for the CLI process.
	Env map[string]string
	// ExtraArgs passes arbitrary CLI flags.
	ExtraArgs map[string]string
	// MaxBufferSize sets the maximum buffer size for CLI output.
	MaxBufferSize int
	// StderrCallback receives stderr output from CLI.
	StderrCallback func(string)
	// CanUseTool is a callback for tool permission requests.
	CanUseTool CanUseToolFunc
	// Hooks configures hook callbacks for various events.
	Hooks map[HookEvent][]HookMatcher
	// User specifies the user for the CLI process.
	User string
	// IncludePartialMessages enables streaming of partial messages.
	IncludePartialMessages bool
	// ForkSession forks resumed sessions to a new session ID.
	ForkSession bool
	// Agents defines custom agent configurations.
	Agents map[string]AgentDefinition
	// SettingSources specifies which setting sources to load.
	SettingSources []SettingSource
	// Sandbox configures bash command sandboxing.
	Sandbox *SandboxSettings
	// Plugins configures custom plugins.
	Plugins []PluginConfig
	// MaxThinkingTokens sets the max tokens for thinking blocks.
	// Deprecated: Use Thinking instead.
	MaxThinkingTokens int
	// Thinking configures extended thinking behavior.
	Thinking *ThinkingConfig
	// OutputFormat specifies the output format for structured outputs.
	OutputFormat map[string]interface{}
	// EnableFileCheckpointing enables file change tracking.
	EnableFileCheckpointing bool
	// Effort controls thinking depth. Use EffortLevel constants (EffortLevelLow, EffortLevelMedium, etc.).
	Effort string
	// TaskBudget sets an API-side task budget in tokens.
	TaskBudget *TaskBudget
	// Skills configures the skills allowlist.
	// Accepted values:
	//   nil        → no SDK auto-configuration (CLI defaults apply)
	//   "all"      → enable every discovered skill ("Skill" tool injected)
	//   []string   → enable only the named skills ("Skill(name)" entries injected)
	// When Skills is set and SettingSources is nil, SettingSources defaults to
	// ["user","project"].
	Skills interface{}
	// SessionStore is the transcript-mirror store adapter.
	// When set, --session-mirror is passed to the CLI subprocess and incoming
	// transcript_mirror frames are forwarded to the store via the batcher.
	SessionStore SessionStore
	// SessionStoreFlush controls when transcript-mirror entries are flushed to
	// SessionStore. "batched" (default) coalesces entries and flushes once per
	// turn or at overflow. "eager" triggers a background flush after every
	// frame for near-real-time delivery. Ignored when SessionStore is nil.
	SessionStoreFlush SessionStoreFlushMode
	// StrictMCPConfig when true, only use MCP servers passed via MCPServers,
	// ignoring all other MCP configurations the CLI would otherwise load (e.g.
	// project .mcp.json, user/global settings, plugin-provided servers).
	// Maps to the CLI's --strict-mcp-config flag.
	StrictMCPConfig bool
	// IncludeHookEvents when true, the CLI emits hook events (PreToolUse, PostToolUse,
	// Stop, etc.) as HookEventMessage objects in the message stream.
	IncludeHookEvents bool
	// ResumeSessionAt, when resuming, only loads the conversation up to and
	// including the message with this UUID. Use with Resume (and usually
	// ForkSession) to branch from an earlier point in the conversation.
	//
	// Accepts any transcript-entry UUID — typically an AssistantMessage UUID
	// observed live, or a SessionMessage UUID from GetSessionMessages. See
	// ResumeDropsTurn for guidance on choosing the fork point.
	ResumeSessionAt string
	// ResumeDropsTurn, with ResumeSessionAt, is the UUID of the user prompt
	// whose turn this truncating resume intends to discard.
	//
	// When set, the CLI validates at load time that every transcript entry
	// after the ResumeSessionAt point is attributable to that turn, and
	// refuses the resume otherwise — e.g. when the discarded range contains a
	// queued user message or task notification the session absorbed mid-turn
	// that the caller had not yet observed. A refusal surfaces as an error
	// returned from Query / ClaudeSDKClient (a ProcessError, or ResultError)
	// whose message contains "Resume rejected by --resume-drops-turn:" — match
	// on that text. Treat it as deterministic: clear the pending fork target
	// and resume plainly rather than retrying the same request. Leave nil to
	// keep the unvalidated truncation behavior.
	//
	// A non-nil pointer is always forwarded, even to an empty string, so the
	// CLI rejects a malformed declaration instead of the SDK silently
	// disarming the guard (mirrors the Python SDK's `is not None` forwarding).
	//
	// Rule of thumb: set ResumeSessionAt to the *last* transcript entry of the
	// turn you are keeping (whatever its type), and ResumeDropsTurn to the
	// prompt UUID of the turn immediately after it.
	ResumeDropsTurn *string
	// ForwardSubagentText forwards subagent text and thinking blocks as
	// messages in the stream.
	//
	// By default only tool_use / tool_result blocks from subagents (spawned
	// via the Agent tool) are emitted, as AssistantMessage / UserMessage
	// objects whose ParentToolUseID is the spawning Agent tool_use id —
	// enough for a progress heartbeat. When true, the subagent's text and
	// thinking blocks are forwarded the same way, so consumers can render the
	// full nested transcript. Matches the TypeScript SDK's
	// forwardSubagentText.
	ForwardSubagentText bool
	// LoadTimeoutMs is the upper bound on SessionStore.Load / ListSubkeys calls
	// during resume materialization, in milliseconds.
	// A value of 0 means immediate timeout; use a large value to effectively
	// disable. Defaults to 60 000 ms (60 seconds).
	LoadTimeoutMs int
}

// DefaultOptions returns ClaudeAgentOptions with default values.
func DefaultOptions() *ClaudeAgentOptions {
	return &ClaudeAgentOptions{
		MaxBufferSize: 1024 * 1024, // 1MB default
	}
}

// configureCanUseTool validates CanUseTool and routes permission prompts
// over stdio.
//
// Shared by Query() and ClaudeSDKClient.Connect() so both entry points
// enforce the same rules. Returns opts unchanged when no callback is set;
// otherwise checks it is not combined with PermissionPromptToolName, emits
// the shadowing advisory, and returns a copy with
// PermissionPromptToolName="stdio" so the CLI sends permission requests over
// the control protocol. Mirrors the Python SDK's _configure_can_use_tool.
func configureCanUseTool(opts *ClaudeAgentOptions) (*ClaudeAgentOptions, error) {
	if opts == nil || opts.CanUseTool == nil {
		return opts, nil
	}
	// CanUseTool and PermissionPromptToolName are mutually exclusive.
	if opts.PermissionPromptToolName != "" {
		return nil, fmt.Errorf(
			"can_use_tool callback cannot be used with permission_prompt_tool_name. " +
				"Please use one or the other.")
	}
	// Advisory: warn if other options shadow the callback.
	warnIfCanUseToolShadowed(opts)
	// Automatically set permission_prompt_tool_name to "stdio" for the
	// control protocol.
	configured := *opts
	configured.PermissionPromptToolName = "stdio"
	return &configured, nil
}
