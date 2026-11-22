package transport

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// initFrame builds the first system/init frame a CLI emits. A missing
// claude_code_version stands in for a CLI too old to report one.
func initFrame(version string) map[string]interface{} {
	frame := map[string]interface{}{"type": "system", "subtype": "init"}
	if version != "" {
		frame["claude_code_version"] = version
	}
	return frame
}

// TestCheckCLIVersionFromInitFrame mirrors Python's
// TestOlderCLIWarning and TestCLIVersionCheck (v0.2.181). The version now
// comes from the running CLI's init frame, not from a `cli_path -v` probe.
func TestCheckCLIVersionFromInitFrame(t *testing.T) {
	tests := []struct {
		name            string
		verbatimPrompts bool
		version         string
		wantWarnings    int
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "older CLI warns when verbatim prompts are on",
			verbatimPrompts: true,
			version:         "2.1.247",
			wantWarnings:    1,
			wantContains:    []string{"verbatim_prompts", "2.1.248"},
		},
		{
			name:            "old 2.0 CLI warns when verbatim prompts are on",
			verbatimPrompts: true,
			version:         "2.0.5",
			wantWarnings:    1,
			wantContains:    []string{"verbatim_prompts"},
		},
		{
			name:            "supported CLI does not warn",
			verbatimPrompts: true,
			version:         "2.1.248",
			wantWarnings:    0,
		},
		{
			name:            "newer CLI does not warn",
			verbatimPrompts: true,
			version:         "3.0.0",
			wantWarnings:    0,
		},
		{
			name:            "option off does not warn",
			verbatimPrompts: false,
			version:         "2.1.100",
			wantWarnings:    0,
		},
		{
			name:            "CLI that reports no version warns once",
			verbatimPrompts: false,
			version:         "",
			wantWarnings:    1,
			wantContains:    []string{"too old to report its version", "2.0.0"},
		},
		{
			name:            "unparseable version warns twice with option on",
			verbatimPrompts: true,
			version:         "not-a-version",
			wantWarnings:    2,
			wantContains:    []string{"too old to report", "verbatim_prompts"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: tt.verbatimPrompts})
			buf := captureSlogWarnings(t)

			tr.checkCLIVersion(initFrame(tt.version))

			out := buf.String()
			if got := strings.Count(out, "level=WARN"); got != tt.wantWarnings {
				t.Errorf("expected %d warning(s), got %d: %s", tt.wantWarnings, got, out)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("expected warning to mention %q, got: %s", want, out)
				}
			}
			for _, banned := range tt.wantNotContains {
				if strings.Contains(out, banned) {
					t.Errorf("expected warning not to mention %q, got: %s", banned, out)
				}
			}
		})
	}
}

// TestCheckCLIVersionWarnsOnlyOnce mirrors Python's
// test_cli_too_old_to_report_its_version_warns_once: the CLI sends an init
// frame per turn, but the warning is emitted only for the first one.
func TestCheckCLIVersionWarnsOnlyOnce(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{})
	buf := captureSlogWarnings(t)

	tr.checkCLIVersion(initFrame(""))
	tr.checkCLIVersion(initFrame(""))
	tr.checkCLIVersion(initFrame("2.1.247"))

	if got := strings.Count(buf.String(), "level=WARN"); got != 1 {
		t.Errorf("expected exactly one warning, got %d: %s", got, buf.String())
	}
}

// TestCheckCLIVersionIgnoresOtherFrames keeps the check from consuming the
// first arbitrary message as the version report.
func TestCheckCLIVersionIgnoresOtherFrames(t *testing.T) {
	tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: true})
	buf := captureSlogWarnings(t)

	tr.checkCLIVersion(map[string]interface{}{"type": "assistant"})
	tr.checkCLIVersion(map[string]interface{}{"type": "system", "subtype": "status"})
	if !strings.Contains(buf.String(), "level=WARN") {
		// No warning yet is the point; assert the guard really waits.
		if buf.String() != "" {
			t.Errorf("expected no warnings before an init frame, got: %s", buf.String())
		}
	}
	tr.checkCLIVersion(initFrame("2.1.247"))
	if got := strings.Count(buf.String(), "level=WARN"); got != 1 {
		t.Errorf("expected the init frame to warn once, got %d: %s", got, buf.String())
	}
}

// TestCheckCLIVersionSkipEnvEscapeHatch mirrors Python's
// test_version_warning_can_be_skipped.
func TestCheckCLIVersionSkipEnvEscapeHatch(t *testing.T) {
	t.Run("set non-empty skips the check", func(t *testing.T) {
		t.Setenv(skipVersionCheckEnv, "1")
		tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: true})
		buf := captureSlogWarnings(t)
		tr.checkCLIVersion(initFrame("2.1.247"))
		if buf.String() != "" {
			t.Errorf("expected no warnings with the check skipped, got: %s", buf.String())
		}
	})
	t.Run("set to empty still checks", func(t *testing.T) {
		t.Setenv(skipVersionCheckEnv, "")
		tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: true})
		buf := captureSlogWarnings(t)
		tr.checkCLIVersion(initFrame("2.1.247"))
		if !strings.Contains(buf.String(), "verbatim_prompts") {
			t.Errorf("expected the verbatim_prompts warning, got: %s", buf.String())
		}
	})
	t.Run("unset still checks", func(t *testing.T) {
		unsetEnvForTest(t, skipVersionCheckEnv)
		tr := newTestTransport(t, &TransportOptions{VerbatimPrompts: true})
		buf := captureSlogWarnings(t)
		tr.checkCLIVersion(initFrame(""))
		if !strings.Contains(buf.String(), "too old to report") {
			t.Errorf("expected the old-CLI warning, got: %s", buf.String())
		}
	})
}

// TestConnectChecksCLIVersionFromInitFrame proves the real read loop wires
// the check to stdout, and that Connect spawns cli_path only once: no
// separate `-v` version probe (Python SDK #1483).
func TestConnectChecksCLIVersionFromInitFrame(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script stands in for the CLI")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "invocations.log")
	cliPath := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		"printf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"claude_code_version\":\"2.1.247\"}'\n"
	if err := os.WriteFile(cliPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake CLI: %v", err)
	}

	buf := captureSlogWarnings(t)
	tr, err := NewSubprocessTransport("test", &TransportOptions{
		CLIPath:         cliPath,
		VerbatimPrompts: true,
	})
	if err != nil {
		t.Fatalf("NewSubprocessTransport failed: %v", err)
	}
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer func() { _ = tr.Close() }()

	select {
	case _, ok := <-tr.messages:
		if !ok {
			t.Fatal("messages channel closed before the init frame arrived")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the init frame")
	}
	if !strings.Contains(buf.String(), "verbatim_prompts") {
		t.Errorf("expected the verbatim_prompts warning from the init frame, got: %s", buf.String())
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading invocation log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly one CLI spawn, got %d: %q", len(lines), log)
	}
	for _, token := range strings.Fields(string(log)) {
		if token == "-v" {
			t.Errorf("Connect must not run a separate -v version probe, got: %q", log)
		}
	}
}
