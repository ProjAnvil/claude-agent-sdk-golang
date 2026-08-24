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
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	// DefaultMaxBufferSize is the default maximum buffer size (1MB).
	DefaultMaxBufferSize = 1024 * 1024
	// MinimumCLIVersion is the minimum supported Claude Code version.
	MinimumCLIVersion = "2.0.0"
)

// cmdExeMetacharacters are the cmd.exe metacharacters (plus the quote
// character cmd.exe uses to toggle its quoting state, and "!", which expands
// like "%" when delayed expansion is enabled). See rejectWindowsBatchCLI /
// rejectWindowsCmdMetacharacters.
const cmdExeMetacharacters = `&|<>^%!"`

// skillNameInvalidChars matches characters that can never ride safely in a
// Skill(name) rule: parentheses and commas are delimiters to the
// --allowedTools tokenizer, and control characters (C0, DEL, C1) never appear
// in a skill directory name. U+FEFF is here rather than with the whitespace
// check because the CLI trims it as whitespace and unicode.IsSpace does not.
var skillNameInvalidChars = regexp.MustCompile(`[(),\x00-\x1f\x7f-\x9f\x{feff}]`)

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
	// waitMu serializes process reaping: os/exec.Cmd.Wait is not safe for
	// concurrent use, and both readStdout's teardown and Close's graceful
	// shutdown need the process's exit status. The first caller runs Wait;
	// later callers block until it finishes and observe the stored result.
	waitMu     sync.Mutex
	waitCalled bool
	waitErr    error
	// goos overrides runtime.GOOS for the Windows-only validation in
	// Connect; empty means runtime.GOOS. Test seam only.
	goos string
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
	// windows selects the Windows remediation message (the native
	// installer); npm is not recommended there because it installs a
	// claude.cmd shim, which Connect refuses to run.
	windows bool
}

func (e *CLINotFoundError) Error() string {
	var msg string
	if e.windows {
		msg = "Claude Code not found. Install the native claude.exe with (PowerShell):\n" +
			"  irm https://claude.ai/install.ps1 | iex\n\n" +
			"Or provide the path to a claude.exe via options:\n" +
			"  &TransportOptions{CLIPath: \"C:\\\\path\\\\to\\\\claude.exe\"}\n\n" +
			"(npm install -g @anthropic-ai/claude-code produces a claude.cmd shim, " +
			"which this SDK refuses to run on Windows.)"
	} else {
		msg = "Claude Code not found. Install with:\n" +
			"  curl -fsSL https://claude.ai/install.sh | bash\n\n" +
			"If already installed, provide the path via options:\n" +
			"  &TransportOptions{CLIPath: \"/path/to/claude\"}"
	}
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
	return findCLIForGOOS(runtime.GOOS)
}

// findCLIForGOOS is findCLI with the target OS as a parameter, so the
// Windows-only branches are testable on POSIX hosts.
func findCLIForGOOS(goos string) (string, error) {
	// Check bundled CLI first
	if bundled := findBundledCLI(); bundled != "" {
		return bundled, nil
	}

	// Check PATH
	var whichHit string
	if cli, err := exec.LookPath("claude"); err == nil {
		if goos != "windows" || isWindowsNativeExe(cli) {
			return cli, nil
		}
		// Windows resolved something CreateProcess cannot run directly as
		// the CLI: npm's claude.cmd shim (which Connect refuses to spawn)
		// or an extensionless wrapper script from a git-bash / WSL setup
		// (which fails at spawn with WinError 193). LookPath walks PATH
		// directory-major, so such an entry in an early PATH directory
		// shadows a native claude.exe installed in a later one. Prefer any
		// discoverable native executable, and keep this hit only as the
		// last resort so a shim-only machine still gets the explanatory
		// batch-script refusal from Connect. The claude.exe probe is
		// vetted too: PATHEXT resolution can append an extension and hand
		// back "claude.exe.cmd".
		if exe, err := exec.LookPath("claude.exe"); err == nil && isWindowsNativeExe(exe) {
			return exe, nil
		}
		whichHit = cli
	}

	// Check common locations
	home, _ := os.UserHomeDir()
	for _, path := range cliSearchLocations(home, goos) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	if whichHit != "" {
		// No native executable was discoverable anywhere: return the
		// original PATH hit so Connect raises the batch-script refusal
		// (with its remediation) for a shim, or the spawn error for a
		// wrapper script, rather than a bare not-found error.
		return whichHit, nil
	}

	return "", &CLINotFoundError{windows: goos == "windows"}
}

// cliSearchLocations lists the fixed fallback install locations probed after
// PATH. On Windows only the native installer's claude.exe is probed: an
// extensionless match (a WSL / git-bash script artifact at
// ~/.local/bin/claude) would preempt the explanatory batch-script refusal
// with an opaque spawn failure, and a rooted-but-driveless
// "/usr/local/bin/claude" resolves against the current drive
// (C:\usr\local\bin\...), a location another local user can create -- a
// binary-planting probe.
func cliSearchLocations(home, goos string) []string {
	if goos == "windows" {
		return []string{filepath.Join(home, ".local/bin/claude.exe")}
	}
	return []string{
		filepath.Join(home, ".npm-global/bin/claude"),
		"/usr/local/bin/claude",
		filepath.Join(home, ".local/bin/claude"),
		filepath.Join(home, "node_modules/.bin/claude"),
		filepath.Join(home, ".yarn/bin/claude"),
		filepath.Join(home, ".claude/local/claude"),
	}
}

// isWindowsNativeExe reports whether cliPath's final component names an image
// CreateProcess runs directly (.exe / .com), used only to decide which
// discovery result to prefer. It is not a security gate: every returned path
// still passes rejectWindowsBatchCLI in Connect.
func isWindowsNativeExe(cliPath string) bool {
	name := strings.ReplaceAll(cliPath, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.ToLower(strings.TrimRight(name, ". "))
	return strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".com")
}

// isWindowsBatchCLI reports whether cliPath names a .bat/.cmd batch script on
// Windows. Always false off Windows. See rejectWindowsBatchCLI for why
// spawning such a script is refused.
func isWindowsBatchCLI(cliPath string) bool {
	return isWindowsBatchCLIForGOOS(cliPath, runtime.GOOS)
}

// isWindowsBatchCLIForGOOS is isWindowsBatchCLI with the target OS as a
// parameter, so the Windows branch is testable on POSIX CI hosts.
//
// Deliberately plain string logic rather than filepath: filepath parses
// several of the cases below differently per OS, and the tests run on POSIX
// CI while the code runs on Windows.
//
// EVERY path component is classified, not only the final one. Win32 opens a
// path after lexical normalization -- "." / ".." collapsing, repeated
// separators, and position-dependent trailing dot/space trimming (a middle
// ".. " or "..." stays a literal name while a final one trims to ".." or
// vanishes) -- and any attempt to re-derive the effective final component
// here is a race against that ruleset: get one rule slightly wrong and a
// spelling such as "claude.cmd\...\.." resolves to claude.cmd on Windows
// while the simulation lands on some other name. Refusing whenever ANY
// component carries a batch extension closes that whole class outright,
// because every normalization trick still has to spell the .bat/.cmd
// component somewhere in the string. It costs nothing legitimate: no real
// claude.exe lives beneath a directory named like a batch file.
//
// Within a component, Win32 finds the extension with a last-dot scan over
// the WHOLE component, stream spec included -- "claude:evil.cmd" has
// extension ".cmd" -- while an NTFS stream spec also opens its base file --
// "claude.cmd:stream" opens claude.cmd -- and a drive prefix
// ("C:claude.cmd") rides in the same component. Splitting each component on
// ":" covers all of these: colons cannot appear in real file names, so no
// legitimate segment is over-refused. Trailing dots and spaces, which
// Windows strips at path resolution, are stripped per segment (the same
// normalization Rust's CVE-2024-24576 fix applies), and a bare ".cmd"
// counts as a batch extension (as Win32 PathFindExtension treats it).
func isWindowsBatchCLIForGOOS(cliPath, goos string) bool {
	if goos != "windows" {
		return false
	}
	for _, component := range strings.Split(strings.ReplaceAll(cliPath, "\\", "/"), "/") {
		for _, segment := range strings.Split(component, ":") {
			segment = strings.ToLower(strings.TrimRight(segment, ". "))
			if strings.HasSuffix(segment, ".bat") || strings.HasSuffix(segment, ".cmd") {
				return true
			}
		}
	}
	return false
}

// rejectWindowsBatchCLI refuses to execute a .bat/.cmd script as the CLI on
// Windows.
//
// Windows has no shebang mechanism: CreateProcess runs batch scripts by
// silently rewriting the spawn into a "cmd.exe /c" invocation, and cmd.exe
// re-parses the whole command line at execution time. Go's os/exec quotes
// arguments for the MSVCRT argv rules only, not for cmd.exe, so cmd.exe
// metacharacters inside an argument value -- for example a session title
// passed to --resume -- reach cmd.exe unescaped and can execute injected
// commands. Reliable escaping for cmd.exe does not exist (%VAR% expands even
// inside double quotes), so spawning a batch script with runtime-provided
// arguments cannot be made safe. Refusing is the same remediation Node.js
// shipped for this vulnerability class (CVE-2024-27980, "BatBadBut").
//
// In practice this refuses npm's claude.cmd shim, which findCLI returns only
// when no native claude.exe is discoverable. The alternatives in the error
// message avoid cmd.exe entirely.
func rejectWindowsBatchCLI(cliPath string) error {
	return rejectWindowsBatchCLIForGOOS(cliPath, runtime.GOOS)
}

func rejectWindowsBatchCLIForGOOS(cliPath, goos string) error {
	if !isWindowsBatchCLIForGOOS(cliPath, goos) {
		return nil
	}
	return &CLIConnectionError{Message: fmt.Sprintf(
		"Refusing to execute batch script %q: Windows runs "+
			".bat/.cmd files via cmd.exe, which can execute commands "+
			"injected through CLI arguments, and no reliable escaping for "+
			"cmd.exe exists. Use a native claude executable instead: "+
			"install Claude Code natively "+
			"(irm https://claude.ai/install.ps1 | iex), or point "+
			"TransportOptions.CLIPath at a claude.exe.", cliPath)}
}

// rejectWindowsCmdMetacharacters is defense in depth for Windows: it rejects
// cmd.exe metacharacters in option values.
//
// With batch-script spawning refused (rejectWindowsBatchCLI), these
// characters are harmless: os/exec quotes correctly for native executables.
// They are rejected anyway so that Resume / SessionID / ResumeSessionAt /
// ResumeDropsTurn values, which applications commonly take from external
// input, stay inert even if a cmd.exe hop is ever reintroduced between the
// SDK and the CLI. No format is imposed beyond this (resume values may be
// arbitrary session titles, not only UUIDs), and POSIX behavior is unchanged.
func rejectWindowsCmdMetacharacters(optionName, value string) error {
	return rejectWindowsCmdMetacharactersForGOOS(optionName, value, runtime.GOOS)
}

func rejectWindowsCmdMetacharactersForGOOS(optionName, value, goos string) error {
	if goos != "windows" {
		return nil
	}
	var bad []rune
	seen := make(map[rune]bool)
	for _, c := range value {
		if (strings.ContainsRune(cmdExeMetacharacters, c) || c == '\r' || c == '\n') && !seen[c] {
			seen[c] = true
			bad = append(bad, c)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i] < bad[j] })
	quoted := make([]string, len(bad))
	for i, c := range bad {
		quoted[i] = "'" + string(c) + "'"
	}
	return fmt.Errorf("%s value %q contains characters that are unsafe to pass "+
		"on a Windows command line: [%s]", optionName, value, strings.Join(quoted, ", "))
}

// validateSkillsOptions rejects Skills values other than nil, "all", or a
// []string of valid skill names.
//
// A bare string other than "all" is almost always a caller bug (in the
// Python SDK it would silently iterate as characters), and any other type
// would be silently ignored when the argv is built, installing no skill
// filter at all. Failing closed mirrors the Python SDK (#1145).
func validateSkillsOptions(skills interface{}) error {
	switch s := skills.(type) {
	case nil:
		return nil
	case string:
		if s == "all" {
			return nil
		}
		return fmt.Errorf("ClaudeAgentOptions.skills must be a list of skill names or "+
			"\"all\", got '%s'. Did you mean ['%s']?", s, s)
	case []string:
		for _, name := range s {
			if err := validateSkillName(name); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("ClaudeAgentOptions.skills must be a list of skill names or "+
			"\"all\", got %v.", skills)
	}
}

// validateSkillName rejects skill names that cannot ride safely in a
// Skill(name) rule.
//
// Names from Options.Skills are formatted into the --allowedTools value,
// which the CLI splits into rules on commas and spaces outside parentheses.
// That tokenizer does not honor escape sequences -- escaping exists only in
// the per-rule grammar, applied after splitting -- so a name carrying a
// delimiter cannot be passed through reliably: what it tokenizes into
// depends on what surrounds it.
//
// Names that tokenize cleanly but can never match the listed skill are
// rejected too, so a dead rule fails loudly here instead of silently
// granting nothing. Each check below states its own reason.
func validateSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("skill names must be non-empty strings")
	}
	// Go strings are UTF-8 bytes, so the Python surrogate check has no
	// direct equivalent; reject invalid UTF-8 instead -- no CLI-discovered
	// skill name contains it.
	if !utf8.ValidString(name) {
		return fmt.Errorf("invalid skill name %q: contains invalid UTF-8,"+
			" which can never match a skill the CLI discovered.", name)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("invalid skill name %q: leading or trailing whitespace"+
			" can never match — the Skill tool trims the invoked name.", name)
	}
	if skillNameInvalidChars.MatchString(name) {
		return fmt.Errorf("invalid skill name %q: parentheses, commas, control"+
			" characters, and byte-order marks are not allowed. Names match"+
			" the skill's directory name, or 'plugin:skill' for"+
			" plugin-qualified skills.", name)
	}
	if name == "*" {
		return fmt.Errorf("invalid skill name '*': use skills=\"all\" to enable every skill.")
	}
	if strings.HasSuffix(name, ":*") || strings.HasSuffix(name, " *") {
		return fmt.Errorf("invalid skill name %q: wildcard-suffix names are not"+
			" allowed; list each skill by its exact name.", name)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("invalid skill name %q: skill names may not start with"+
			" '/'. The skills option takes the canonical name, not the"+
			" slash-command form.", name)
	}
	if strings.Contains(name, "\\\\") {
		return fmt.Errorf("invalid skill name %q: consecutive backslashes are not"+
			" allowed — the per-rule parser collapses them, so the rule"+
			" would name a different skill.", name)
	}
	if strings.HasSuffix(name, "\\") {
		return fmt.Errorf("invalid skill name %q: names may not end with an"+
			" unpaired backslash.", name)
	}
	return nil
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

	goos := t.goos
	if goos == "" {
		goos = runtime.GOOS
	}

	// Validate the resolved CLI path and option values before anything is
	// spawned with them (#1127, #1145).
	if err := rejectWindowsBatchCLIForGOOS(t.cliPath, goos); err != nil {
		return err
	}
	if err := rejectWindowsCmdMetacharactersForGOOS("resume", t.options.Resume, goos); err != nil {
		return err
	}
	if err := rejectWindowsCmdMetacharactersForGOOS("session_id", t.options.SessionID, goos); err != nil {
		return err
	}
	if t.options.ResumeSessionAt != "" {
		if err := rejectWindowsCmdMetacharactersForGOOS("resume_session_at", t.options.ResumeSessionAt, goos); err != nil {
			return err
		}
	}
	if t.options.ResumeDropsTurn != nil {
		if err := rejectWindowsCmdMetacharactersForGOOS("resume_drops_turn", *t.options.ResumeDropsTurn, goos); err != nil {
			return err
		}
	}
	if err := validateSkillsOptions(t.options.Skills); err != nil {
		return err
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
// NOTE: Does NOT close stdin when the channel is drained. Stdin must remain
// open so the SDK can write MCP control_response messages back to the CLI.
// Stdin will be closed when Transport.Close() is called.
func (t *SubprocessTransport) streamInput(ch chan map[string]interface{}) {

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

// applySkillsDefaults computes the effective allowed_tools and setting_sources
// after applying any Skills override.
//
//   - nil skills → no change (caller's AllowedTools and SettingSources are used as-is)
//   - "all"      → inject the bare "Skill" tool; default SettingSources to ["user","project"] when nil
//   - []string   → inject "Skill(name)" for each name; same SettingSources default
//
// Each listed skill name is validated in Connect via validateSkillsOptions
// before being formatted into a rule; invalid Skills values surface as
// errors there, not here. Does not mutate t.options.
func (t *SubprocessTransport) applySkillsDefaults() (allowedTools []string, settingSources []string, settingSourcesSet bool) {
	allowedTools = make([]string, len(t.options.AllowedTools))
	copy(allowedTools, t.options.AllowedTools)

	// settingSourcesSet tracks whether SettingSources was explicitly set on
	// the options (nil = not set, even empty slice = set).
	settingSourcesSet = t.options.SettingSources != nil
	if settingSourcesSet {
		settingSources = make([]string, len(t.options.SettingSources))
		copy(settingSources, t.options.SettingSources)
	}

	if t.options.Skills == nil {
		return
	}

	// Determine the skill entries to inject.
	switch s := t.options.Skills.(type) {
	case string:
		if s == "all" {
			found := false
			for _, tool := range allowedTools {
				if tool == "Skill" {
					found = true
					break
				}
			}
			if !found {
				allowedTools = append(allowedTools, "Skill")
			}
		}
	case []string:
		for _, name := range s {
			pattern := fmt.Sprintf("Skill(%s)", name)
			found := false
			for _, tool := range allowedTools {
				if tool == pattern {
					found = true
					break
				}
			}
			if !found {
				allowedTools = append(allowedTools, pattern)
			}
		}
	}

	// Default setting_sources to ["user","project"] when not explicitly set.
	if !settingSourcesSet {
		settingSources = []string{"user", "project"}
		settingSourcesSet = true
	}

	return
}

// buildCommand constructs the CLI command with arguments.
func (t *SubprocessTransport) buildCommand(ctx context.Context) *exec.Cmd {
	// -p "" enables print mode (non-interactive). Claude CLI v2.x requires
	// --print for --output-format and --input-format flags to take effect;
	// without it the CLI enters interactive TUI mode.
	args := []string{"-p", "", "--output-format", "stream-json", "--verbose"}

	// System prompt
	if t.options.SystemPromptFile != nil && t.options.SystemPromptFile.Path != "" {
		args = append(args, "--system-prompt-file", t.options.SystemPromptFile.Path)
	} else if t.options.SystemPrompt == "" && t.options.SystemPromptPreset == nil {
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

	// Apply skills defaults: merges skills into allowed_tools and
	// defaults setting_sources when skills are configured.
	effectiveAllowedTools, effectiveSettingSources, effectiveSettingSourcesSet := t.applySkillsDefaults()

	if len(effectiveAllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(effectiveAllowedTools, ","))
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

	if t.options.TaskBudget != nil {
		args = append(args, "--task-budget", strconv.Itoa(*t.options.TaskBudget))
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

	// Pass these as --flag=value rather than as two argv tokens. The CLI
	// declares --resume with an optional value, so in the two-token form a
	// dash-leading value is not bound to the flag and is instead parsed as
	// a separate CLI flag -- letting an untrusted value inject arbitrary
	// flags. The equals form always binds the value to the flag.
	if t.options.Resume != "" {
		args = append(args, "--resume="+t.options.Resume)
	}

	if t.options.SessionID != "" {
		args = append(args, "--session-id="+t.options.SessionID)
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

	// Equals form so the value can never be parsed as a separate flag, even
	// if the CLI's declaration of these options ever changes.
	if t.options.ResumeSessionAt != "" {
		args = append(args, "--resume-session-at="+t.options.ResumeSessionAt)
	}

	// Non-nil, not non-empty: an empty string is forwarded so the CLI rejects
	// it as a malformed declaration instead of the SDK silently disarming the
	// guard the caller believes is armed.
	if t.options.ResumeDropsTurn != nil {
		args = append(args, "--resume-drops-turn="+*t.options.ResumeDropsTurn)
	}

	// Agents
	if len(t.options.Agents) > 0 {
		agentsJSON, _ := json.Marshal(t.options.Agents)
		args = append(args, "--agents", string(agentsJSON))
	}

	// Setting sources — use `=` format so an empty value round-trips correctly.
	// nil SettingSources (and skills == nil) means omit the flag entirely;
	// an explicitly-set empty slice means `--setting-sources=` (clear all sources).
	if effectiveSettingSourcesSet {
		args = append(args, fmt.Sprintf("--setting-sources=%s", strings.Join(effectiveSettingSources, ",")))
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
			// Boolean flag without value
			args = append(args, fmt.Sprintf("--%s", flag))
		} else if strings.HasPrefix(value, "-") {
			// In the two-token form, a dash-leading value is not bound
			// to its flag when the CLI declares the option with an
			// optional value -- it parses as a separate flag instead
			// (the same injection the --resume change above closes).
			// The equals form always binds.
			args = append(args, fmt.Sprintf("--%s=%s", flag, value))
		} else {
			args = append(args, fmt.Sprintf("--%s", flag), value)
		}
	}

	// Resolve thinking config -> --thinking / --max-thinking-tokens
	// `thinking` takes precedence over the deprecated `max_thinking_tokens`
	if t.options.Thinking != nil {
		switch t.options.Thinking.Type {
		case "adaptive":
			args = append(args, "--thinking", "adaptive")
		case "enabled":
			args = append(args, "--max-thinking-tokens", strconv.Itoa(t.options.Thinking.BudgetTokens))
		case "disabled":
			args = append(args, "--thinking", "disabled")
		}
		// --thinking-display is valid only when thinking is active.
		if t.options.Thinking.Type != "disabled" && t.options.Thinking.Display != "" {
			args = append(args, "--thinking-display", t.options.Thinking.Display)
		}
	} else if t.options.MaxThinkingTokens > 0 {
		// Fallback for deprecated option
		args = append(args, "--max-thinking-tokens", strconv.Itoa(t.options.MaxThinkingTokens))
	}

	// Session mirror — enable when a SessionStore is configured.
	if t.options.SessionStore != nil {
		args = append(args, "--session-mirror")
	}

	// Strict MCP config — only use MCP servers from --mcp-config.
	if t.options.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}

	// Include hook events — emit hook lifecycle events in the message stream.
	if t.options.IncludeHookEvents {
		args = append(args, "--include-hook-events")
	}

	// Sandbox
	if t.options.Sandbox != nil {
		sandboxJSON, _ := json.Marshal(t.options.Sandbox)
		args = append(args, "--sandbox", string(sandboxJSON))
	}

	// Effort
	if t.options.Effort != "" {
		args = append(args, "--effort", t.options.Effort)
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

	cmd := exec.CommandContext(ctx, t.cliPath, args...)

	// Set environment: inherit → default ENTRYPOINT → user env → SDK required.
	// Filter out CLAUDECODE so SDK-spawned subprocesses don't think
	// they're running inside a Claude Code parent.
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			env = append(env, e)
		}
	}
	env = append(env, "CLAUDE_CODE_ENTRYPOINT=sdk-go")
	for k, v := range t.options.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	if t.cwd != "" {
		env = append(env, fmt.Sprintf("PWD=%s", t.cwd))
	}
	if t.options.EnableFileCheckpointing {
		env = append(env, "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true")
	}
	// CLAUDE_AGENT_SDK_VERSION is always set by the SDK and cannot be
	// overridden by user-provided env (matches Python SDK behavior).
	env = append(env, fmt.Sprintf("CLAUDE_AGENT_SDK_VERSION=%s", sdkVersion))

	cmd.Env = env

	return cmd
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

		// Skip non-JSON lines (e.g. [SandboxDebug]) when not
		// mid-parse — they corrupt the buffer otherwise.
		if jsonBuffer.Len() == 0 && !strings.HasPrefix(line, "{") {
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
		closed := t.closed
		t.closeMu.Unlock()
		if !closed {
			t.messages <- data
		}
	}

	if err := scanner.Err(); err != nil {
		if err == bufio.ErrTooLong {
			t.errors <- &BufferOverflowError{BufferSize: t.maxBufferSize, Limit: t.maxBufferSize}
		} else {
			t.errors <- &JSONDecodeError{Line: jsonBuffer.String(), Cause: err}
		}
	}

	if err := t.waitForProcessExit(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.exitError = &ProcessError{Message: "command failed", ExitCode: exitErr.ExitCode()}
			t.errors <- t.exitError
		}
	}
}

// waitForProcessExit reaps the child exactly once. Concurrent callers
// (readStdout's teardown and Close's graceful-shutdown path) serialize on
// waitMu; later callers block until the first Wait finishes and then return
// the stored result instead of racing inside os/exec.Cmd.Wait, which is not
// safe for concurrent use.
func (t *SubprocessTransport) waitForProcessExit() error {
	t.waitMu.Lock()
	defer t.waitMu.Unlock()
	if t.waitCalled {
		return t.waitErr
	}
	t.waitCalled = true
	t.waitErr = t.process.Wait()
	return t.waitErr
}

// readStderr reads stderr output.
// Each line is delivered to the StderrCallback if set. A panic in the
// user-provided callback is recovered per-line so a failing callback does not
// terminate the loop and silently drop every subsequent stderr line for the
// rest of the session.
func (t *SubprocessTransport) readStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if t.options.StderrCallback != nil {
			// Isolate per-line so a panic in the user's callback doesn't
			// kill the read loop.
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Log and continue reading subsequent lines.
					}
				}()
				t.options.StderrCallback(line)
			}()
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

	// Wait for graceful shutdown after stdin EOF, then terminate if needed.
	// The subprocess needs time to flush its session file after receiving
	// EOF on stdin. Without this grace period, SIGTERM can interrupt the
	// write and cause the last assistant message to be lost.
	if t.process != nil && t.process.Process != nil {
		done := make(chan struct{})
		go func() {
			// Returns immediately if the process was already reaped (e.g.
			// by readStdout's teardown).
			t.waitForProcessExit()
			close(done)
		}()

		select {
		case <-done:
			// Process exited gracefully
		case <-time.After(5 * time.Second):
			// Graceful shutdown timed out — send SIGTERM
			t.process.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				// SIGTERM timed out — force kill
				t.process.Process.Kill()
				<-done
			}
		}
	}

	return nil
}

// IsReady returns true if the transport is ready for communication.
func (t *SubprocessTransport) IsReady() bool {
	return t.ready
}
