package claude

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
	// MCPServers configures MCP servers (external or SDK).
	MCPServers map[string]MCPServerConfig
	// PermissionMode sets the permission level for tool execution.
	PermissionMode PermissionMode
	// ContinueConversation continues an existing conversation.
	ContinueConversation bool
	// Resume resumes a specific session.
	Resume string
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
	Betas []string
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
}

// DefaultOptions returns ClaudeAgentOptions with default values.
func DefaultOptions() *ClaudeAgentOptions {
	return &ClaudeAgentOptions{
		MaxBufferSize: 1024 * 1024, // 1MB default
	}
}
