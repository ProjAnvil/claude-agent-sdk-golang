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

// TestCoverage_JSONDecodeErrorUnwrap verifies the cause is reachable via
// Unwrap/errors.Is.
func TestCoverage_JSONDecodeErrorUnwrap(t *testing.T) {
	cause := errors.New("underlying parse failure")
	err := &JSONDecodeError{Line: `{"broken"`, Cause: cause}

	if got := err.Unwrap(); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is should match the cause through Unwrap")
	}
}

// TestCoverage_WindowsGuardWrappersOffWindows verifies the public
// runtime.GOOS-based wrappers delegate to the goos-parameterized helpers:
// off Windows they are no-ops even for values that would be refused on
// Windows. (The Windows branches themselves are covered through the
// *ForGOOS helpers in subprocess_security_test.go.)
func TestCoverage_WindowsGuardWrappersOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wrapper no-op behavior is only observable off Windows")
	}

	if isWindowsBatchCLI(`C:\tools\claude.cmd`) {
		t.Error("isWindowsBatchCLI must be false off Windows, even for a .cmd path")
	}
	if err := rejectWindowsBatchCLI(`C:\tools\claude.bat`); err != nil {
		t.Errorf("rejectWindowsBatchCLI must be a no-op off Windows: %v", err)
	}
	if err := rejectWindowsCmdMetacharacters("resume", "a & b | c"); err != nil {
		t.Errorf("rejectWindowsCmdMetacharacters must be a no-op off Windows: %v", err)
	}
}

// TestCoverage_ConnectSessionIDMetacharacterRejection verifies Connect
// surfaces the session_id metacharacter rejection before any spawn on
// Windows (the resume variant is covered elsewhere).
func TestCoverage_ConnectSessionIDMetacharacterRejection(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{SessionID: "x&calc"})
	tr.goos = "windows"

	err := tr.Connect(context.Background())
	if err == nil {
		t.Fatal("Expected Connect to reject cmd.exe metacharacters in session_id on Windows")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("Error should name the session_id option: %v", err)
	}
	if tr.process != nil {
		t.Error("Rejection must happen before any spawn; transport.process should be nil")
	}
}

// TestCoverage_FindCLIFallbackLocation verifies discovery falls back to the
// fixed install locations when PATH has no claude binary.
func TestCoverage_FindCLIFallbackLocation(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no claude on PATH
	home := t.TempDir()
	t.Setenv("HOME", home)

	cli := filepath.Join(home, ".npm-global", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findCLIForGOOS("darwin")
	if err != nil {
		t.Fatalf("findCLIForGOOS failed: %v", err)
	}
	if got != cli {
		t.Errorf("Expected fallback location %q, got %q", cli, got)
	}
}

// TestCoverage_FindCLINotFound verifies a bare CLINotFoundError when no
// binary is discoverable anywhere, and that NewSubprocessTransport surfaces
// it instead of a half-built transport.
func TestCoverage_FindCLINotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	// /usr/local/bin/claude is a fixed fallback location outside our control.
	if _, err := os.Stat("/usr/local/bin/claude"); err == nil {
		t.Skip("host has /usr/local/bin/claude; cannot exercise the not-found path")
	}

	_, err := findCLI()
	var nf *CLINotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("Expected *CLINotFoundError, got %T: %v", err, err)
	}

	_, err = NewSubprocessTransport("prompt", &TransportOptions{})
	if !errors.As(err, &nf) {
		t.Fatalf("NewSubprocessTransport should surface *CLINotFoundError, got %T: %v", err, err)
	}
}

// TestCoverage_FindBundledCLI verifies a CLI shipped next to the running
// executable under _bundled/ is found and preferred over PATH discovery.
func TestCoverage_FindBundledCLI(t *testing.T) {
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}
	bundledDir := filepath.Join(filepath.Dir(execPath), "_bundled")
	cliName := "claude"
	if runtime.GOOS == "windows" {
		cliName = "claude.exe"
	}
	cli := filepath.Join(bundledDir, cliName)

	if err := os.MkdirAll(bundledDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(bundledDir)
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findBundledCLI(); got != cli {
		t.Errorf("findBundledCLI() = %q, want %q", got, cli)
	}
	// Bundled wins over everything else in discovery.
	got, err := findCLIForGOOS(runtime.GOOS)
	if err != nil {
		t.Fatalf("findCLIForGOOS failed: %v", err)
	}
	if got != cli {
		t.Errorf("Discovery should prefer the bundled CLI %q, got %q", cli, got)
	}

	// A directory at the bundled path is not a runnable CLI.
	if err := os.Remove(cli); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cli, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findBundledCLI(); got != "" {
		t.Errorf("A directory at the bundled path must be ignored, got %q", got)
	}
}

// TestCoverage_ApplySkillsDefaultsDedup verifies a skill rule already present
// in AllowedTools is not duplicated, for both the "all" and list forms.
func TestCoverage_ApplySkillsDefaultsDedup(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{
		Skills:       "all",
		AllowedTools: []string{"Skill", "Bash"},
	})
	allowed, sources, sourcesSet := tr.applySkillsDefaults()

	skillCount := 0
	for _, tool := range allowed {
		if tool == "Skill" {
			skillCount++
		}
	}
	if skillCount != 1 {
		t.Errorf("Skill tool must not be duplicated, got allowedTools=%v", allowed)
	}
	if !sourcesSet || len(sources) != 2 || sources[0] != "user" || sources[1] != "project" {
		t.Errorf("skills should default setting_sources to [user project], got %v (set=%v)", sources, sourcesSet)
	}

	tr = newTestTransport(t, &TransportOptions{
		Skills:       []string{"pdf"},
		AllowedTools: []string{"Skill(pdf)"},
	})
	allowed, _, _ = tr.applySkillsDefaults()

	pdfCount := 0
	for _, tool := range allowed {
		if tool == "Skill(pdf)" {
			pdfCount++
		}
	}
	if pdfCount != 1 {
		t.Errorf("Skill(pdf) rule must not be duplicated, got allowedTools=%v", allowed)
	}
}
