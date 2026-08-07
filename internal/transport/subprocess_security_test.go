package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestTransport builds a transport with an explicit (fake) CLI path so no
// CLI discovery runs.
func newTestTransport(t *testing.T, opts *TransportOptions) *SubprocessTransport {
	t.Helper()
	if opts == nil {
		opts = &TransportOptions{}
	}
	if opts.CLIPath == "" {
		opts.CLIPath = "/fake/path/claude"
	}
	transport, err := NewSubprocessTransport("test", opts)
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	return transport
}

// containsToken reports whether token appears as a whole argv element.
func containsToken(args []string, token string) bool {
	for _, a := range args {
		if a == token {
			return true
		}
	}
	return false
}

// --- #1123: --resume / --session-id equals form ---------------------------

// TestBuildCommand_ResumeSessionIDEqualsForm is the #1123 regression test:
// dash-leading values must bind to their flag as a single --flag=value argv
// token and never appear as standalone tokens.
func TestBuildCommand_ResumeSessionIDEqualsForm(t *testing.T) {
	tests := []struct {
		name        string
		opts        *TransportOptions
		wantToken   string
		bareFlag    string
		injectedTok string
	}{
		{
			name:        "dash-leading resume value",
			opts:        &TransportOptions{Resume: "--evil"},
			wantToken:   "--resume=--evil",
			bareFlag:    "--resume",
			injectedTok: "--evil",
		},
		{
			name:        "dash-leading session id",
			opts:        &TransportOptions{SessionID: "-r"},
			wantToken:   "--session-id=-r",
			bareFlag:    "--session-id",
			injectedTok: "-r",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newTestTransport(t, tt.opts)
			args := transport.buildCommand(context.Background()).Args[1:]

			if !containsToken(args, tt.wantToken) {
				t.Errorf("Expected single token %q in args: %v", tt.wantToken, args)
			}
			if containsToken(args, tt.bareFlag) {
				t.Errorf("Bare %q must never appear as a standalone token: %v", tt.bareFlag, args)
			}
			if containsToken(args, tt.injectedTok) {
				t.Errorf("Injected value %q must never appear as a standalone token: %v", tt.injectedTok, args)
			}
		})
	}
}

// TestBuildCommand_ResumeSessionIDEqualsFormOrdinary checks that ordinary
// values still use the equals form and bind correctly.
func TestBuildCommand_ResumeSessionIDEqualsFormOrdinary(t *testing.T) {
	transport := newTestTransport(t, &TransportOptions{
		Resume:    "session-123",
		SessionID: "test-session-123",
	})
	args := transport.buildCommand(context.Background()).Args[1:]

	if !containsToken(args, "--resume=session-123") {
		t.Errorf("Expected --resume=session-123 in args: %v", args)
	}
	if !containsToken(args, "--session-id=test-session-123") {
		t.Errorf("Expected --session-id=test-session-123 in args: %v", args)
	}
}

// --- #1127: Windows batch-script refusal -----------------------------------

// TestIsWindowsBatchCLI mirrors the classification table from the Python
// TestWindowsBatchScriptRefusal suffix-trick cases.
func TestIsWindowsBatchCLI(t *testing.T) {
	batchPaths := []string{
		`claude.cmd`,
		`CLAUDE.CMD`,
		`dir\claude.bat`,
		`C:\tools\claude.cmd.`,
		`C:\tools\claude.CMD `,
		`C:\tools\claude.cmd:stream`,
		`C:\tools\.cmd`,
		`.cmd`,
		`C:claude.cmd`,
		`C:/tools/claude.cmd`,
		`\\server\share\claude.cmd`,
		`C:\tools\claude.cmd\.`,
		`C:\tools\claude.cmd\x\..`,
		`C:\tools\claude.cmd\x\.. `,
		`C:\tools\claude.cmd\x\.. .`,
		`C:\tools\claude.cmd\\.`,
		`C:/tools/claude.cmd//x/..`,
		`C:\tools\claude.cmd\`,
		`C:\tools\claude.cmd\...`,
		`C:\tools\claude.cmd\....`,
		`C:\tools\claude:evil.cmd`,
		`C:\tools\claude.exe:evil.cmd`,
		`:claude.cmd`,
		// A middle dots/spaces-only component is a literal name on Win32
		// (trailing-dot trimming applies to the final segment only), so a
		// following ".." pops that literal and lands on claude.cmd.
		`C:\tools\claude.cmd\...\..`,
		`C:\tools\claude.cmd\. .\..`,
		`C:\tools\claude.cmd\ \..`,
		`C:\tools\claude.cmd\.. \..`,
	}
	for _, path := range batchPaths {
		if !isWindowsBatchCLIForGOOS(path, "windows") {
			t.Errorf("isWindowsBatchCLIForGOOS(%q, \"windows\") = false, want true", path)
		}
	}

	notBatchPaths := []string{
		`claude.exe`,
		`claude`,
		`claude.command`, // ends in "and", not ".cmd"
		`claude.com`,
		`C:\tools\claude.EXE`,
		`C:\Users\u\.local\bin\claude.exe`,
		`/usr/local/bin/claude`,
		`C:\tools\batch.dir\claude`,
	}
	for _, path := range notBatchPaths {
		if isWindowsBatchCLIForGOOS(path, "windows") {
			t.Errorf("isWindowsBatchCLIForGOOS(%q, \"windows\") = true, want false", path)
		}
	}

	// Off Windows the classifier is always false.
	for _, goos := range []string{"linux", "darwin"} {
		for _, path := range batchPaths {
			if isWindowsBatchCLIForGOOS(path, goos) {
				t.Errorf("isWindowsBatchCLIForGOOS(%q, %q) = true, want false", path, goos)
			}
		}
	}
}

// TestRejectWindowsBatchCLI checks the refusal error type and remediation
// content, and that native exes / POSIX are unaffected.
func TestRejectWindowsBatchCLI(t *testing.T) {
	err := rejectWindowsBatchCLIForGOOS(`C:\tools\claude.bat`, "windows")
	if err == nil {
		t.Fatal("Expected refusal error for .bat CLI on Windows")
	}
	var connErr *CLIConnectionError
	if !errors.As(err, &connErr) {
		t.Errorf("Expected *CLIConnectionError, got %T", err)
	}
	msg := err.Error()
	for _, want := range []string{"batch script", "install.ps1", "claude.exe"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Refusal message should mention %q: %s", want, msg)
		}
	}

	if err := rejectWindowsBatchCLIForGOOS(`C:\tools\claude.exe`, "windows"); err != nil {
		t.Errorf("Native exe should be allowed on Windows: %v", err)
	}
	if err := rejectWindowsBatchCLIForGOOS(`/odd/claude.cmd`, "linux"); err != nil {
		t.Errorf("Guard should be a no-op off Windows: %v", err)
	}
}

// TestConnect_RefusesBatchCLIBeforeSpawn verifies Connect refuses a batch
// CLI before anything is spawned (t.process stays nil).
func TestConnect_RefusesBatchCLIBeforeSpawn(t *testing.T) {
	transport := newTestTransport(t, &TransportOptions{CLIPath: `C:\tools\claude.cmd`})
	transport.goos = "windows"

	err := transport.Connect(context.Background())
	if err == nil {
		t.Fatal("Expected Connect to refuse a .cmd CLI on Windows")
	}
	var connErr *CLIConnectionError
	if !errors.As(err, &connErr) {
		t.Errorf("Expected *CLIConnectionError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "batch script") {
		t.Errorf("Error should mention batch script: %v", err)
	}
	if transport.process != nil {
		t.Error("Refusal must happen before any spawn; transport.process should be nil")
	}
}

// TestConnect_BatchCLIOKOnPOSIX verifies POSIX behavior is unchanged: a
// .cmd-suffixed executable spawns fine off Windows.
func TestConnect_BatchCLIOKOnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only test")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "claude.cmd")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	transport := newTestTransport(t, &TransportOptions{CLIPath: cli})
	if err := transport.Connect(context.Background()); err != nil {
		t.Fatalf("Connect should not refuse a .cmd path off Windows: %v", err)
	}
	transport.Close()
}

// --- #1127: findCLI Windows behavior ----------------------------------------

// TestCLISearchLocations checks the fallback location lists per OS.
func TestCLISearchLocations(t *testing.T) {
	home := "/home/user"

	win := cliSearchLocations(home, "windows")
	if len(win) != 1 || !strings.HasSuffix(win[0], filepath.Join(".local", "bin", "claude.exe")) {
		t.Errorf("Windows locations should probe only ~/.local/bin/claude.exe, got %v", win)
	}

	posix := cliSearchLocations(home, "linux")
	if len(posix) != 6 {
		t.Errorf("POSIX locations should have 6 entries, got %v", posix)
	}
	for _, p := range posix {
		if strings.HasSuffix(p, ".exe") {
			t.Errorf("POSIX locations should not probe .exe names: %v", p)
		}
	}
}

// TestCLINotFoundErrorMessages checks that the Windows not-found message
// recommends the native installer and never recommends npm, while the POSIX
// message is unchanged.
func TestCLINotFoundErrorMessages(t *testing.T) {
	winMsg := (&CLINotFoundError{windows: true}).Error()
	if !strings.Contains(winMsg, "install.ps1") {
		t.Error("Windows message should recommend the native installer (install.ps1)")
	}
	if !strings.Contains(winMsg, "claude.exe") {
		t.Error("Windows message should mention claude.exe")
	}
	if !strings.Contains(winMsg, "refuses") {
		t.Error("Windows message should explain the npm shim is refused")
	}
	// npm may only appear as the explanation of what NOT to do.
	npmIdx := strings.Index(winMsg, "npm")
	if npmIdx < 0 || strings.Contains(winMsg[npmIdx:], "Install with") {
		t.Errorf("Windows message must not recommend npm as an install route: %s", winMsg)
	}

	posixMsg := (&CLINotFoundError{}).Error()
	if !strings.HasPrefix(posixMsg, "Claude Code not found. Install with:\n  curl -fsSL https://claude.ai/install.sh | bash") {
		t.Errorf("POSIX message changed unexpectedly: %s", posixMsg)
	}
	if strings.Contains(posixMsg, "install.ps1") {
		t.Error("POSIX message should not mention install.ps1")
	}
}

// TestIsWindowsNativeExe checks the native-exe classifier used to prefer a
// claude.exe over a shim PATH hit.
func TestIsWindowsNativeExe(t *testing.T) {
	native := []string{
		`C:\Users\u\.local\bin\claude.exe`,
		`C:\Users\u\.local\bin\claude.EXE`,
		`C:\tools\claude.com`,
		`claude.exe`,
		`C:\tools\claude.exe `, // trailing space trimmed
	}
	for _, p := range native {
		if !isWindowsNativeExe(p) {
			t.Errorf("isWindowsNativeExe(%q) = false, want true", p)
		}
	}
	notNative := []string{
		`C:\Users\u\AppData\Roaming\npm\claude.CMD`,
		`C:\tools\claude.exe.cmd`, // PATHEXT resolution can hand this back
		`C:\Users\u\bin\claude`,
		`claude.bat`,
	}
	for _, p := range notNative {
		if isWindowsNativeExe(p) {
			t.Errorf("isWindowsNativeExe(%q) = true, want false", p)
		}
	}
}

// TestFindCLIForGOOS_WindowsPrefersNativeExe exercises the Windows discovery
// branches with a synthetic PATH: a shadowing "claude" entry plus a native
// "claude.exe" must resolve to the exe; a shim-only PATH must still return
// the shim (so Connect raises the batch refusal) rather than a not-found.
func TestFindCLIForGOOS_WindowsPrefersNativeExe(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude")
	exe := filepath.Join(dir, "claude.exe")
	for _, p := range []string{shim, exe} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	// Both present: the native exe wins over the extensionless shim.
	got, err := findCLIForGOOS("windows")
	if err != nil {
		t.Fatalf("findCLIForGOOS failed: %v", err)
	}
	if got != exe {
		t.Errorf("Expected native exe %q, got %q", exe, got)
	}

	// Shim only: discovery returns the shim so Connect refuses it with the
	// explanatory message, rather than reporting a bare not-found.
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	got, err = findCLIForGOOS("windows")
	if err != nil {
		t.Fatalf("findCLIForGOOS failed: %v", err)
	}
	if got != shim {
		t.Errorf("Expected shim %q as last resort, got %q", shim, got)
	}

	// POSIX discovery uses the LookPath("claude") result directly.
	got, err = findCLIForGOOS("linux")
	if err != nil {
		t.Fatalf("findCLIForGOOS failed: %v", err)
	}
	if got != shim {
		t.Errorf("Expected POSIX discovery to return %q, got %q", shim, got)
	}
}

// --- #1127: ExtraArgs value binding -----------------------------------------

// TestBuildCommand_ExtraArgsValueBinding mirrors TestExtraArgsValueBinding:
// a dash-leading value binds via --flag=value; ordinary values keep the
// two-token form; an empty value stays a bare flag.
func TestBuildCommand_ExtraArgsValueBinding(t *testing.T) {
	transport := newTestTransport(t, &TransportOptions{
		ExtraArgs: map[string]string{"future-flag": "--evil"},
	})
	args := transport.buildCommand(context.Background()).Args[1:]

	if !containsToken(args, "--future-flag=--evil") {
		t.Errorf("Expected --future-flag=--evil in args: %v", args)
	}
	if containsToken(args, "--evil") {
		t.Errorf("--evil must not appear as a standalone token: %v", args)
	}
	if containsToken(args, "--future-flag") {
		t.Errorf("bare --future-flag must not appear with a dash-leading value: %v", args)
	}

	transport = newTestTransport(t, &TransportOptions{
		ExtraArgs: map[string]string{"future-flag": "plain", "bool-flag": ""},
	})
	args = transport.buildCommand(context.Background()).Args[1:]

	idx := -1
	for i, a := range args {
		if a == "--future-flag" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != "plain" {
		t.Errorf("Ordinary value should keep the two-token form: %v", args)
	}
	if !containsToken(args, "--bool-flag") {
		t.Errorf("Empty value should stay a bare flag: %v", args)
	}
}

// --- #1127: cmd.exe metacharacter rejection ---------------------------------

// TestRejectWindowsCmdMetacharacters mirrors TestWindowsCmdMetacharacterRejection.
func TestRejectWindowsCmdMetacharacters(t *testing.T) {
	badValues := []string{
		"x&calc",
		"x|whoami",
		"x<in",
		"x>out",
		"x^y",
		"x%PATH%y",
		"x!VAR!y",
		`x"y`,
		"x\ny",
		"x\ry",
		"R&D notes",
	}
	for _, v := range badValues {
		err := rejectWindowsCmdMetacharactersForGOOS("resume", v, "windows")
		if err == nil {
			t.Errorf("Expected rejection for %q on Windows", v)
			continue
		}
		if !strings.Contains(err.Error(), "unsafe") {
			t.Errorf("Error for %q should mention 'unsafe': %v", v, err)
		}
		if !strings.Contains(err.Error(), "resume") {
			t.Errorf("Error for %q should name the option: %v", v, err)
		}
	}

	// The session_id option is covered too, and names itself in the error.
	err := rejectWindowsCmdMetacharactersForGOOS("session_id", "x&ver", "windows")
	if err == nil || !strings.Contains(err.Error(), "session_id") {
		t.Errorf("Expected session_id rejection naming the option: %v", err)
	}

	// Offending characters are listed sorted, once each.
	err = rejectWindowsCmdMetacharactersForGOOS("resume", "b&a%&", "windows")
	if err == nil || !strings.Contains(err.Error(), "['%', '&']") {
		t.Errorf("Expected sorted unique offenders ['%%', '&']: %v", err)
	}

	// Ordinary titles pass on Windows.
	title := "My project - daily notes (v2) #3"
	if err := rejectWindowsCmdMetacharactersForGOOS("resume", title, "windows"); err != nil {
		t.Errorf("Ordinary title should be accepted on Windows: %v", err)
	}

	// POSIX is a no-op, even for metacharacter-laden values.
	if err := rejectWindowsCmdMetacharactersForGOOS("resume", "title & % | notes", "linux"); err != nil {
		t.Errorf("POSIX should allow metacharacters: %v", err)
	}
	if err := rejectWindowsCmdMetacharactersForGOOS("session_id", "a>b", "darwin"); err != nil {
		t.Errorf("POSIX should allow metacharacters: %v", err)
	}
}

// TestConnect_CmdMetacharacterRejection verifies Connect surfaces the
// rejection before any spawn on Windows, and not on POSIX.
func TestConnect_CmdMetacharacterRejection(t *testing.T) {
	transport := newTestTransport(t, &TransportOptions{Resume: "R&D notes"})
	transport.goos = "windows"

	err := transport.Connect(context.Background())
	if err == nil {
		t.Fatal("Expected Connect to reject cmd.exe metacharacters on Windows")
	}
	// Plain error (mirrors Python's ValueError), not a connection error.
	var connErr *CLIConnectionError
	if errors.As(err, &connErr) {
		t.Errorf("Metacharacter rejection should be a plain error, got %T", err)
	}
	if transport.process != nil {
		t.Error("Rejection must happen before any spawn; transport.process should be nil")
	}

	transport = newTestTransport(t, &TransportOptions{Resume: "R&D notes", CLIPath: "echo"})
	if err := transport.Connect(context.Background()); err != nil {
		t.Errorf("POSIX Connect should not reject metacharacters: %v", err)
	}
	transport.Close()
}

// --- #1145: skill name validation --------------------------------------------

// TestValidateSkillsOptions_Rejection mirrors the #1145 parametrized
// rejection cases.
func TestValidateSkillsOptions_Rejection(t *testing.T) {
	tests := []struct {
		name    string
		skills  interface{}
		wantMsg string
	}{
		// Rule-syntax delimiters can never be represented as a single
		// Skill(name) entry.
		{"paren comma mix", []string{"x),Bash(*"}, "invalid skill name"},
		{"rule injection", []string{"safe),Bash,Skill(dummy"}, "invalid skill name"},
		{"commas", []string{"name,with,commas"}, "invalid skill name"},
		{"unbalanced open paren", []string{"unbalanced("}, "invalid skill name"},
		{"unbalanced close paren", []string{"unbalanced)"}, "invalid skill name"},
		{"empty parens", []string{"()"}, "invalid skill name"},
		// Control characters (C0, DEL, C1).
		{"newline", []string{"with\nnewline"}, "invalid skill name"},
		{"tab", []string{"with\ttab"}, "invalid skill name"},
		{"nul", []string{"nul\x00byte"}, "invalid skill name"},
		{"del", []string{"del\x7fchar"}, "invalid skill name"},
		{"c1 nel", []string{"nel\u0085end"}, "invalid skill name"},
		{"c1 csi", []string{"csi\u009bend"}, "invalid skill name"},
		// The CLI trims U+FEFF as whitespace; unicode.IsSpace does not.
		{"leading bom", []string{"\ufeffpdf"}, "invalid skill name"},
		{"trailing bom", []string{"pdf\ufeff"}, "invalid skill name"},
		// Empty or whitespace-only.
		{"empty", []string{""}, "non-empty"},
		{"space", []string{" "}, "non-empty"},
		{"whitespace only", []string{"  \t "}, "non-empty"},
		// Surrounding whitespace: a padded rule can never match.
		{"leading space", []string{" pdf"}, "whitespace"},
		{"trailing space", []string{"pdf "}, "whitespace"},
		{"leading tab", []string{"\tpdf"}, "whitespace"},
		{"padded", []string{" pdf "}, "whitespace"},
		// Wildcards.
		{"bare wildcard", []string{"*"}, `use skills="all"`},
		{"plugin wildcard", []string{"pdf:*"}, "wildcard-suffix"},
		{"space wildcard", []string{"my skill *"}, "wildcard-suffix"},
		{"colon-only wildcard", []string{":*"}, "wildcard-suffix"},
		// Slash-command form hides every skill.
		{"leading slash", []string{"/pdf"}, "may not start with"},
		{"leading slash qualified", []string{"/myplugin:pdf"}, "may not start with"},
		// Backslash shapes.
		{"trailing double backslash", []string{`name\\`}, "consecutive backslashes"},
		{"trailing triple backslash", []string{`name\\\`}, "consecutive backslashes"},
		{"middle double backslash", []string{`mid\\dle`}, "consecutive backslashes"},
		{"trailing single backslash", []string{`name\`}, "unpaired backslash"},
		// Invalid UTF-8 (Go's equivalent of the Python surrogate check).
		{"invalid utf8", []string{"bad\xffname"}, "invalid UTF-8"},
		// Non-list, non-"all" Skills values.
		{"bare string", "pdf", "must be a list of skill names"},
		{"bare string plural", "pdf-tools", "must be a list of skill names"},
		{"bare string ALL", "ALL", "must be a list of skill names"},
		{"other type", 42, "must be a list of skill names"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSkillsOptions(tt.skills)
			if err == nil {
				t.Fatalf("Expected error for skills=%v", tt.skills)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("Error for skills=%v should contain %q: %v", tt.skills, tt.wantMsg, err)
			}
		})
	}
}

// TestValidateSkillsOptions_BareStringSuggestion checks the Did-you-mean
// hint for a bare string.
func TestValidateSkillsOptions_BareStringSuggestion(t *testing.T) {
	err := validateSkillsOptions("pdf")
	if err == nil {
		t.Fatal("Expected error for bare string skills")
	}
	if !strings.Contains(err.Error(), "Did you mean ['pdf']?") {
		t.Errorf("Expected Did-you-mean suggestion: %v", err)
	}

	// Non-string, non-list values get no suggestion.
	err = validateSkillsOptions(42)
	if err == nil {
		t.Fatal("Expected error for non-list skills")
	}
	if strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("Non-string values should not get a suggestion: %v", err)
	}
}

// TestValidateSkillsOptions_Acceptance mirrors the #1145 acceptance cases:
// ordinary names still pass validation and build identical argv to before.
func TestValidateSkillsOptions_Acceptance(t *testing.T) {
	if err := validateSkillsOptions(nil); err != nil {
		t.Errorf("nil skills should pass: %v", err)
	}
	if err := validateSkillsOptions("all"); err != nil {
		t.Errorf("skills=\"all\" should pass: %v", err)
	}

	benign := []string{
		"pdf-tools",
		"my_skill.v2",
		"myplugin:pdf",
		"skill with spaces",
		`dir\sub`,
		"日本語スキル",
	}
	for _, name := range benign {
		if err := validateSkillsOptions([]string{name}); err != nil {
			t.Errorf("Expected %q to be accepted: %v", name, err)
		}

		transport := newTestTransport(t, &TransportOptions{Skills: []string{name}})
		args := transport.buildCommand(context.Background()).Args[1:]
		idx := -1
		for i, a := range args {
			if a == "--allowedTools" {
				idx = i
				break
			}
		}
		if idx < 0 || idx+1 >= len(args) || args[idx+1] != "Skill("+name+")" {
			t.Errorf("Expected --allowedTools Skill(%s), got: %v", name, args)
		}
	}
}

// TestConnect_SkillsValidation verifies invalid skills surface as errors
// from Connect before anything is spawned.
func TestConnect_SkillsValidation(t *testing.T) {
	transport := newTestTransport(t, &TransportOptions{Skills: []string{"plugin:*"}})
	err := transport.Connect(context.Background())
	if err == nil {
		t.Fatal("Expected Connect to reject wildcard-suffix skill names")
	}
	if !strings.Contains(err.Error(), "wildcard-suffix") {
		t.Errorf("Expected wildcard-suffix error: %v", err)
	}
	if transport.process != nil {
		t.Error("Validation must happen before any spawn; transport.process should be nil")
	}

	transport = newTestTransport(t, &TransportOptions{Skills: "pdf"})
	err = transport.Connect(context.Background())
	if err == nil {
		t.Fatal("Expected Connect to reject a bare-string skills value")
	}
	if !strings.Contains(err.Error(), "must be a list of skill names") {
		t.Errorf("Expected non-list error: %v", err)
	}
}
