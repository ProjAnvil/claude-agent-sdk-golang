package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const (
	// DefaultMaxBufferSize is the default maximum buffer size (1MB).
	DefaultMaxBufferSize = 1024 * 1024
	// MinimumCLIVersion is the minimum supported Claude Code version.
	MinimumCLIVersion = "2.0.0"
)

// SubprocessTransport implements Transport using Claude Code CLI subprocess.
type SubprocessTransport struct {
	prompt        interface{} // string or chan map[string]interface{}
	options       *TransportOptions
	cliPath       string
	cwd           string
	process       *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	stderr        io.ReadCloser
	ready         bool
	maxBufferSize int
	writeMu       sync.Mutex
	exitError     error
	messages      chan map[string]interface{}
	errors        chan error
	closed        bool
	closeMu       sync.Mutex
}

// NewSubprocessTransport creates a new subprocess transport.
func NewSubprocessTransport(prompt interface{}, opts *TransportOptions) (*SubprocessTransport, error) {
	if opts == nil {
		opts = DefaultTransportOptions()
	}

	cliPath := opts.CLIPath
	if cliPath == "" {
		var err error
		cliPath, err = findCLI()
		if err != nil {
			return nil, err
		}
	}

	maxBufferSize := opts.MaxBufferSize
	if maxBufferSize <= 0 {
		maxBufferSize = DefaultMaxBufferSize
	}

	return &SubprocessTransport{
		prompt:        prompt,
		options:       opts,
		cliPath:       cliPath,
		cwd:           opts.CWD,
		maxBufferSize: maxBufferSize,
		messages:      make(chan map[string]interface{}, 100),
		errors:        make(chan error, 10),
	}, nil
}

// CLINotFoundError indicates that the Claude Code CLI is not installed.
type CLINotFoundError struct {
	CLIPath string
}

func (e *CLINotFoundError) Error() string {
	msg := "Claude Code not found. Install with:\n" +
		"  curl -fsSL https://claude.ai/install.sh | bash\n\n" +
		"If already installed, provide the path via options:\n" +
		"  &TransportOptions{CLIPath: \"/path/to/claude\"}"
	if e.CLIPath != "" {
		msg = fmt.Sprintf("%s: %s", msg, e.CLIPath)
	}
	return msg
}

// CLIConnectionError indicates connection issues.
type CLIConnectionError struct {
	Message string
	Cause   error
}

func (e *CLIConnectionError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *CLIConnectionError) Unwrap() error {
	return e.Cause
}

// ProcessError indicates CLI process failure.
type ProcessError struct {
	Message  string
	ExitCode int
	Stderr   string
}

func (e *ProcessError) Error() string {
	msg := e.Message
	if e.ExitCode != 0 {
		msg = fmt.Sprintf("%s (exit code: %d)", msg, e.ExitCode)
	}
	if e.Stderr != "" {
		msg = fmt.Sprintf("%s\nError output: %s", msg, e.Stderr)
	}
	return msg
}

// JSONDecodeError indicates JSON parsing failure.
type JSONDecodeError struct {
	Line  string
	Cause error
}

func (e *JSONDecodeError) Error() string {
	truncated := e.Line
	if len(truncated) > 100 {
		truncated = truncated[:100] + "..."
	}
	return fmt.Sprintf("failed to decode JSON: %s", truncated)
}

func (e *JSONDecodeError) Unwrap() error {
	return e.Cause
}

// BufferOverflowError indicates buffer size exceeded.
type BufferOverflowError struct {
	BufferSize int
	Limit      int
}

func (e *BufferOverflowError) Error() string {
	return fmt.Sprintf("JSON message exceeded maximum buffer size of %d bytes (current: %d)", e.Limit, e.BufferSize)
}

// findCLI searches for the Claude Code CLI binary.
func findCLI() (string, error) {
	// Check bundled CLI first
	if bundled := findBundledCLI(); bundled != "" {
		return bundled, nil
	}

	// Check PATH
	if cli, err := exec.LookPath("claude"); err == nil {
		return cli, nil
	}

	// Check common locations
	home, _ := os.UserHomeDir()
	locations := []string{
		filepath.Join(home, ".npm-global/bin/claude"),
		"/usr/local/bin/claude",
		filepath.Join(home, ".local/bin/claude"),
		filepath.Join(home, "node_modules/.bin/claude"),
		filepath.Join(home, ".yarn/bin/claude"),
		filepath.Join(home, ".claude/local/claude"),
	}

	for _, path := range locations {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	return "", &CLINotFoundError{}
}

// findBundledCLI looks for a bundled CLI binary.
func findBundledCLI() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}

	cliName := "claude"
	if runtime.GOOS == "windows" {
		cliName = "claude.exe"
	}

	bundledPath := filepath.Join(filepath.Dir(execPath), "_bundled", cliName)
	if info, err := os.Stat(bundledPath); err == nil && !info.IsDir() {
		return bundledPath
	}

	return ""
}

// Connect starts the CLI subprocess.
func (t *SubprocessTransport) Connect(ctx context.Context) error {
	if t.process != nil {
		return nil
	}

	cmd := t.buildCommand(ctx)
	t.process = cmd

	var err error
	t.stdin, err = cmd.StdinPipe()
	if err != nil {
		return &CLIConnectionError{Message: "failed to create stdin pipe", Cause: err}
	}

	t.stdout, err = cmd.StdoutPipe()
	if err != nil {
		return &CLIConnectionError{Message: "failed to create stdout pipe", Cause: err}
	}

	t.stderr, err = cmd.StderrPipe()
	if err != nil {
		return &CLIConnectionError{Message: "failed to create stderr pipe", Cause: err}
	}

	// Set environment
	env := os.Environ()
	for k, v := range t.options.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = append(env, "CLAUDE_CODE_ENTRYPOINT=sdk-go")
	if t.cwd != "" {
		env = append(env, fmt.Sprintf("PWD=%s", t.cwd))
	}
	if t.options.EnableFileCheckpointing {
		env = append(env, "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true")
	}
	cmd.Env = env

	if t.cwd != "" {
		cmd.Dir = t.cwd
	}

	if err := cmd.Start(); err != nil {
		if os.IsNotExist(err) {
			return &CLINotFoundError{CLIPath: t.cliPath}
		}
		return &CLIConnectionError{Message: "failed to start Claude Code", Cause: err}
	}

	t.ready = true

	go t.readStdout()
	go t.readStderr()

	// Handle input
	if ch, ok := t.prompt.(chan map[string]interface{}); ok {
		// For channel prompts, stream messages to stdin
		go t.streamInput(ch)
	}
	// For string prompts, stdin is kept open so caller can write messages
	// (matching Python SDK behavior where write happens after connect)

	return nil
}

// streamInput reads messages from the channel and writes them to stdin.
func (t *SubprocessTransport) streamInput(ch chan map[string]interface{}) {
	defer t.stdin.Close()

	for msg := range ch {
		data, err := json.Marshal(msg)
		if err != nil {
			// Skip invalid messages
			continue
		}
		if err := t.Write(string(data) + "\n"); err != nil {
			// Stop if writing fails
			break
		}
	}
}

// buildCommand constructs the CLI command with arguments.
func (t *SubprocessTransport) buildCommand(ctx context.Context) *exec.Cmd {
	args := []string{"--output-format", "stream-json", "--verbose"}

	// System prompt
	if t.options.SystemPrompt == "" && t.options.SystemPromptPreset == nil {
		args = append(args, "--system-prompt", "")
	} else if t.options.SystemPrompt != "" {
		args = append(args, "--system-prompt", t.options.SystemPrompt)
	} else if t.options.SystemPromptPreset != nil && t.options.SystemPromptPreset.Append != "" {
		args = append(args, "--append-system-prompt", t.options.SystemPromptPreset.Append)
	}

	// Tools
	if len(t.options.Tools) > 0 {
		args = append(args, "--tools", strings.Join(t.options.Tools, ","))
	} else if t.options.ToolsPreset != nil {
		args = append(args, "--tools", "default")
	}

	if len(t.options.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(t.options.AllowedTools, ","))
	}

	if t.options.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(t.options.MaxTurns))
	}

	if t.options.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.6f", t.options.MaxBudgetUSD))
	}

	if len(t.options.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(t.options.DisallowedTools, ","))
	}

	if t.options.Model != "" {
		args = append(args, "--model", t.options.Model)
	}

	if t.options.FallbackModel != "" {
		args = append(args, "--fallback-model", t.options.FallbackModel)
	}

	if len(t.options.Betas) > 0 {
		args = append(args, "--betas", strings.Join(t.options.Betas, ","))
	}

	if t.options.PermissionPromptToolName != "" {
		args = append(args, "--permission-prompt-tool", t.options.PermissionPromptToolName)
	}

	if t.options.PermissionMode != "" {
		args = append(args, "--permission-mode", t.options.PermissionMode)
	}

	if t.options.ContinueConversation {
		args = append(args, "--continue")
	}

	if t.options.Resume != "" {
		args = append(args, "--resume", t.options.Resume)
	}

	if t.options.Settings != "" {
		args = append(args, "--settings", t.options.Settings)
	}

	for _, dir := range t.options.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	// MCP servers
	if len(t.options.MCPServers) > 0 {
		mcpConfig := t.buildMCPConfig()
		if mcpConfig != "" {
			args = append(args, "--mcp-config", mcpConfig)
		}
	}

	if t.options.IncludePartialMessages {
		args = append(args, "--include-partial-messages")
	}

	if t.options.ForkSession {
		args = append(args, "--fork-session")
	}

	// Agents
	if len(t.options.Agents) > 0 {
		agentsJSON, _ := json.Marshal(t.options.Agents)
		args = append(args, "--agents", string(agentsJSON))
	}

	// Setting sources
	if t.options.SettingSources != nil {
		args = append(args, "--setting-sources", strings.Join(t.options.SettingSources, ","))
	} else {
		args = append(args, "--setting-sources", "")
	}

	// Plugins
	for _, plugin := range t.options.Plugins {
		if plugin.Type == "local" {
			args = append(args, "--plugin-dir", plugin.Path)
		}
	}

	// Extra args
	for flag, value := range t.options.ExtraArgs {
		if value == "" {
			args = append(args, fmt.Sprintf("--%s", flag))
		} else {
			args = append(args, fmt.Sprintf("--%s", flag), value)
		}
	}

	// Thinking
	if t.options.Thinking != nil {
		if t.options.Thinking.Type != "" {
			args = append(args, "--thinking-mode", t.options.Thinking.Type)
		}
		if t.options.Thinking.BudgetTokens > 0 {
			args = append(args, "--thinking-budget-tokens", strconv.Itoa(t.options.Thinking.BudgetTokens))
		}
	} else if t.options.MaxThinkingTokens > 0 {
		// Fallback for deprecated option
		args = append(args, "--max-thinking-tokens", strconv.Itoa(t.options.MaxThinkingTokens))
	}

	// Sandbox
	if t.options.Sandbox != nil {
		sandboxJSON, _ := json.Marshal(t.options.Sandbox)
		args = append(args, "--sandbox", string(sandboxJSON))
	}

	// Output format (JSON schema)
	if t.options.OutputFormat != nil {
		if t.options.OutputFormat["type"] == "json_schema" {
			if schema, ok := t.options.OutputFormat["schema"]; ok {
				schemaJSON, _ := json.Marshal(schema)
				args = append(args, "--json-schema", string(schemaJSON))
			}
		}
	}

	// Input format - always use stream-json mode for consistency
	// (matching Python SDK behavior where prompts are sent via stdin)
	args = append(args, "--input-format", "stream-json")

	return exec.CommandContext(ctx, t.cliPath, args...)
}

// buildMCPConfig builds the MCP configuration JSON.
func (t *SubprocessTransport) buildMCPConfig() string {
	servers := make(map[string]interface{})

	for name, config := range t.options.MCPServers {
		if configMap, ok := config.(map[string]interface{}); ok {
			serverType, _ := configMap["type"].(string)
			switch serverType {
			case "sdk":
				// SDK servers: pass type and name only
				servers[name] = map[string]interface{}{
					"type": "sdk",
					"name": configMap["name"],
				}
			default:
				// Pass through other server configs
				servers[name] = configMap
			}
		}
	}

	if len(servers) == 0 {
		return ""
	}

	config := map[string]interface{}{
		"mcpServers": servers,
	}
	configJSON, _ := json.Marshal(config)
	return string(configJSON)
}

// readStdout reads and parses JSON messages from stdout.
func (t *SubprocessTransport) readStdout() {
	defer close(t.messages)
	defer close(t.errors)

	scanner := bufio.NewScanner(t.stdout)
	scanner.Buffer(make([]byte, t.maxBufferSize), t.maxBufferSize)

	var jsonBuffer strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		jsonBuffer.WriteString(line)

		if jsonBuffer.Len() > t.maxBufferSize {
			t.errors <- &BufferOverflowError{BufferSize: jsonBuffer.Len(), Limit: t.maxBufferSize}
			jsonBuffer.Reset()
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonBuffer.String()), &data); err != nil {
			continue
		}

		jsonBuffer.Reset()

		t.closeMu.Lock()
		if !t.closed {
			t.messages <- data
		}
		t.closeMu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		if err == bufio.ErrTooLong {
			t.errors <- &BufferOverflowError{BufferSize: t.maxBufferSize, Limit: t.maxBufferSize}
		} else {
			t.errors <- &JSONDecodeError{Line: jsonBuffer.String(), Cause: err}
		}
	}

	if err := t.process.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.exitError = &ProcessError{Message: "command failed", ExitCode: exitErr.ExitCode()}
			t.errors <- t.exitError
		}
	}
}

// readStderr reads stderr output.
func (t *SubprocessTransport) readStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if t.options.StderrCallback != nil {
			t.options.StderrCallback(line)
		}
	}
}

// Write sends data to the CLI stdin.
func (t *SubprocessTransport) Write(data string) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if !t.ready {
		return &CLIConnectionError{Message: "transport is not ready for writing"}
	}

	if t.stdin == nil {
		return &CLIConnectionError{Message: "stdin is closed"}
	}

	if t.exitError != nil {
		return &CLIConnectionError{Message: "process has exited", Cause: t.exitError}
	}

	_, err := t.stdin.Write([]byte(data))
	if err != nil {
		return &CLIConnectionError{Message: "failed to write to stdin", Cause: err}
	}

	return nil
}

// ReadMessages returns the channel of parsed JSON messages.
func (t *SubprocessTransport) ReadMessages() <-chan map[string]interface{} {
	return t.messages
}

// Errors returns the channel for transport errors.
func (t *SubprocessTransport) Errors() <-chan error {
	return t.errors
}

// EndInput closes the stdin stream.
func (t *SubprocessTransport) EndInput() error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.stdin != nil {
		err := t.stdin.Close()
		t.stdin = nil
		return err
	}
	return nil
}

// Close terminates the connection and cleans up resources.
func (t *SubprocessTransport) Close() error {
	t.closeMu.Lock()
	t.closed = true
	t.closeMu.Unlock()

	t.ready = false
	t.EndInput()

	if t.process != nil && t.process.Process != nil {
		t.process.Process.Kill()
	}

	return nil
}

// IsReady returns true if the transport is ready for communication.
func (t *SubprocessTransport) IsReady() bool {
	return t.ready
}
