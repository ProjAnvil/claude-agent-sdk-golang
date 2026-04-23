package transport

// TransportOptions contains options needed by the transport layer.
// This is a subset of ClaudeAgentOptions to avoid circular imports.
type TransportOptions struct {
	Tools                    []string
	ToolsPreset              *ToolsPreset
	AllowedTools             []string
	SystemPrompt             string
	SystemPromptPreset       *SystemPromptPreset
	MCPServers               map[string]interface{}
	PermissionMode           string
	ContinueConversation     bool
	Resume                   string
	MaxTurns                 int
	MaxBudgetUSD             float64
	DisallowedTools          []string
	Model                    string
	FallbackModel            string
	Betas                    []string
	PermissionPromptToolName string
	CWD                      string
	CLIPath                  string
	Settings                 string
	AddDirs                  []string
	Env                      map[string]string
	ExtraArgs                map[string]string
	MaxBufferSize            int
	StderrCallback           func(string)
	IncludePartialMessages   bool
	ForkSession              bool
	Agents                   map[string]interface{}
	SettingSources           []string
	Plugins                  []PluginConfig
	MaxThinkingTokens        int
	Thinking                 *ThinkingConfig
	OutputFormat             map[string]interface{}
	EnableFileCheckpointing  bool
	Sandbox                  *SandboxSettings
	// Effort controls thinking depth ("low", "medium", "high", "max").
	Effort string
	// SessionID is the session ID for resuming a session.
	SessionID string
	// TaskBudget is the total task budget in tokens.
	TaskBudget *int
	// SystemPromptFile is the path to a file containing the system prompt.
	SystemPromptFile *SystemPromptFile
	// Skills is an optional skill allowlist. Accepted values:
	//   nil  → no SDK auto-configuration (CLI defaults apply)
	//   "all" → enable every discovered skill (Skill tool injected)
	//   []string → enable only the named skills (Skill(name) entries injected)
	Skills interface{}
	// SessionStore is the store adapter for mirroring session transcripts.
	// When set, --session-mirror is passed to the CLI subprocess.
	// The interface uses interface{} here to avoid circular imports with the
	// claude package; the actual value must satisfy the claude.SessionStore interface.
	SessionStore interface{}
}

// SystemPromptFile represents a file-based system prompt.
type SystemPromptFile struct {
	Type string
	Path string
}

// SystemPromptPreset represents a system prompt preset.
type SystemPromptPreset struct {
	Type   string
	Preset string
	Append string
}

// ToolsPreset represents a tools preset.
type ToolsPreset struct {
	Type   string
	Preset string
}

// PluginConfig represents a plugin configuration.
type PluginConfig struct {
	Type string
	Path string
}

// ThinkingConfig represents thinking configuration.
type ThinkingConfig struct {
	Type         string `json:"type"` // "adaptive", "enabled", "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	// Display controls how thinking text is surfaced: "summarized" or "omitted".
	// Only valid for "adaptive" and "enabled" types.
	Display string `json:"display,omitempty"` // "summarized" or "omitted"
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

// DefaultTransportOptions returns default options.
func DefaultTransportOptions() *TransportOptions {
	return &TransportOptions{
		MaxBufferSize: 1024 * 1024, // 1MB
	}
}
