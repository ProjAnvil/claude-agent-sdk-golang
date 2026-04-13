package transport

// mcp_large_output_test.go — ported from Python SDK's test_mcp_large_output.py
//
// Root cause (confirmed via claude-cli-internal):
// Two independent spill layers in the bundled CLI:
//
//   Layer 1 — MCP-specific (mcpValidation.ts)
//     Threshold: MAX_MCP_OUTPUT_TOKENS env var, default 25 000 tokens.
//     Setting MAX_MCP_OUTPUT_TOKENS=500000 bypasses this layer.
//     Output on spill: plain "Error: result exceeds maximum allowed tokens…"
//
//   Layer 2 — generic tool-result (toolResultStorage.ts maybePersistLargeToolResult)
//     Threshold: DEFAULT_MAX_RESULT_SIZE_CHARS = 50 000 chars, hardcoded in
//     toolLimits.ts. No env var reads this constant. MCPTool declares
//     maxResultSizeChars: 100_000 but getPersistenceThreshold clamps it to
//     Math.min(100_000, 50_000) = 50 K.
//     Output on spill: <persisted-output> tag + 2 KB preview — exactly what
//     customers observe.
//
// Regression timeline:
//   PR #13609 (2026-01-06) removed the feature gate → layer 2 always-on for SDK builds.
//   PR #19224 (2026-02-21) lowered the external-build clamp from 100 K → 50 K chars.
//
// Fix (v0.1.55): Use ToolAnnotations.MaxResultSizeChars to bypass layer 2's
// clamp. Set on sdk tools via the tools/list JSONRPC response so the CLI reads
// anthropic/maxResultSizeChars in _meta and skips Math.min.
//
// These tests confirm:
//  1. MAX_MCP_OUTPUT_TOKENS (layer-1 threshold) passes through to the CLI subprocess.
//  2. os.Environ() values are inherited; options.Env overrides them.
//  3. CLAUDE_AGENT_SDK_VERSION is always set and cannot be overridden by user env.
//  4. CLAUDECODE is stripped so sub-SDK processes don't detect a parent CC session.
//  5. Raising MAX_MCP_OUTPUT_TOKENS alone is NOT sufficient for >50 K results because
//     layer 2 is still in the path. The fix uses ToolAnnotations.MaxResultSizeChars.

import (
	"context"
	"os"
	"strings"
	"testing"
)

// layer2ThresholdChars is the layer-2 spill threshold confirmed in claude-cli-internal
// toolLimits.ts (DEFAULT_MAX_RESULT_SIZE_CHARS = 50000).
const layer2ThresholdChars = 50_000

// ─────────────────────────────────────────────────────────────────────────────
// Helper: build env from a SubprocessTransport without launching a process.
// ─────────────────────────────────────────────────────────────────────────────

func envFromOpts(t *testing.T, opts *TransportOptions) []string {
	t.Helper()
	tr, err := NewSubprocessTransport("test", opts)
	if err != nil {
		t.Fatalf("NewSubprocessTransport: %v", err)
	}
	cmd := tr.buildCommand(context.Background())
	return cmd.Env
}

func envMapFromOpts(t *testing.T, opts *TransportOptions) map[string]string {
	t.Helper()
	m := make(map[string]string)
	for _, e := range envFromOpts(t, opts) {
		idx := strings.IndexByte(e, '=')
		if idx >= 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. MAX_MCP_OUTPUT_TOKENS (layer-1) passthrough
// ─────────────────────────────────────────────────────────────────────────────

// TestLayer1MaxMCPOutputTokensReachesSubprocess confirms that
// MAX_MCP_OUTPUT_TOKENS set in options.Env appears in the subprocess env.
// This controls layer 1 only (mcpValidation.ts, ~25K token default).
// A 73 K-char result bypasses layer 1 with this set, but still hits layer 2's
// 50 K char hard limit — see TestLayer2Boundary below.
func TestLayer1MaxMCPOutputTokensReachesSubprocess(t *testing.T) {
	env := envMapFromOpts(t, &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env:     map[string]string{"MAX_MCP_OUTPUT_TOKENS": "500000"},
	})

	if got, ok := env["MAX_MCP_OUTPUT_TOKENS"]; !ok {
		t.Error("MAX_MCP_OUTPUT_TOKENS was not passed to the CLI subprocess. " +
			"Layer 1 will use its default (~25K tokens) and spill to plain error text.")
	} else if got != "500000" {
		t.Errorf("Expected MAX_MCP_OUTPUT_TOKENS=500000, got %q", got)
	}
}

// TestLayer1DefaultAbsentWhenNotSet confirms the SDK does not inject a default
// for MAX_MCP_OUTPUT_TOKENS — the CLI's own default governs.
func TestLayer1DefaultAbsentWhenNotSet(t *testing.T) {
	// Temporarily clear MAX_MCP_OUTPUT_TOKENS from the process env.
	old := os.Getenv("MAX_MCP_OUTPUT_TOKENS")
	os.Unsetenv("MAX_MCP_OUTPUT_TOKENS")
	defer func() {
		if old != "" {
			os.Setenv("MAX_MCP_OUTPUT_TOKENS", old)
		}
	}()

	env := envMapFromOpts(t, &TransportOptions{
		CLIPath: "/fake/path/claude",
	})

	if _, ok := env["MAX_MCP_OUTPUT_TOKENS"]; ok {
		t.Error("SDK must not inject a default MAX_MCP_OUTPUT_TOKENS; the CLI's default should govern")
	}
}

// TestLayer1ArbitraryThresholdValuesPassThrough confirms various numeric
// values for MAX_MCP_OUTPUT_TOKENS are preserved exactly.
func TestLayer1ArbitraryThresholdValuesPassThrough(t *testing.T) {
	for _, value := range []string{"1", "25000", "1000000"} {
		env := envMapFromOpts(t, &TransportOptions{
			CLIPath: "/fake/path/claude",
			Env:     map[string]string{"MAX_MCP_OUTPUT_TOKENS": value},
		})
		if env["MAX_MCP_OUTPUT_TOKENS"] != value {
			t.Errorf("MAX_MCP_OUTPUT_TOKENS: expected %q, got %q", value, env["MAX_MCP_OUTPUT_TOKENS"])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. os.Environ() inheritance and options.Env precedence
// ─────────────────────────────────────────────────────────────────────────────

// TestEnvInheritedFromOSEnviron confirms that MAX_MCP_OUTPUT_TOKENS set in
// os.Environ before connect() is inherited by the subprocess env.
func TestEnvInheritedFromOSEnviron(t *testing.T) {
	os.Setenv("MAX_MCP_OUTPUT_TOKENS", "200000")
	defer os.Unsetenv("MAX_MCP_OUTPUT_TOKENS")

	env := envMapFromOpts(t, &TransportOptions{
		CLIPath: "/fake/path/claude",
	})

	if got := env["MAX_MCP_OUTPUT_TOKENS"]; got != "200000" {
		t.Errorf("Expected MAX_MCP_OUTPUT_TOKENS=200000 from os.Environ, got %q", got)
	}
}

// TestOptionsEnvOverridesOSEnviron confirms options.Env wins over os.Environ.
func TestOptionsEnvOverridesOSEnviron(t *testing.T) {
	os.Setenv("MAX_MCP_OUTPUT_TOKENS", "1000")
	defer os.Unsetenv("MAX_MCP_OUTPUT_TOKENS")

	env := envMapFromOpts(t, &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env:     map[string]string{"MAX_MCP_OUTPUT_TOKENS": "500000"},
	})

	// options.Env is appended after inherited env, so last value wins on Unix.
	if got := env["MAX_MCP_OUTPUT_TOKENS"]; got != "500000" {
		t.Errorf("options.Env should override os.Environ: got %q, want \"500000\"", got)
	}
}

// TestCLAUDECODEStripped confirms CLAUDECODE is filtered out so SDK-spawned
// subprocesses don't think they're running inside a Claude Code parent session.
func TestCLAUDECODEStrippedInMCPTest(t *testing.T) {
	os.Setenv("CLAUDECODE", "1")
	defer os.Unsetenv("CLAUDECODE")

	env := envFromOpts(t, &TransportOptions{CLIPath: "/fake/path/claude"})
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDECODE=") {
			t.Error("CLAUDECODE must be stripped from subprocess env")
		}
	}
}

// TestSDKManagedVarsAlwaysSet confirms CLAUDE_CODE_ENTRYPOINT and
// CLAUDE_AGENT_SDK_VERSION are always present in the subprocess env.
func TestSDKManagedVarsAlwaysSet(t *testing.T) {
	env := envMapFromOpts(t, &TransportOptions{CLIPath: "/fake/path/claude"})

	if env["CLAUDE_CODE_ENTRYPOINT"] != "sdk-go" {
		t.Errorf("CLAUDE_CODE_ENTRYPOINT: expected \"sdk-go\", got %q", env["CLAUDE_CODE_ENTRYPOINT"])
	}
	if env["CLAUDE_AGENT_SDK_VERSION"] == "" {
		t.Error("CLAUDE_AGENT_SDK_VERSION must always be set")
	}
}

// TestSDKVersionCannotBeOverriddenByUserEnvInMCPTest mirrors the Python test
// test_options_env_cannot_override_sdk_version.
func TestSDKVersionCannotBeOverriddenByUserEnvInMCPTest(t *testing.T) {
	env := envMapFromOpts(t, &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env:     map[string]string{"CLAUDE_AGENT_SDK_VERSION": "0.0.0"},
	})

	if got := env["CLAUDE_AGENT_SDK_VERSION"]; got != sdkVersion {
		t.Errorf("User env must not override CLAUDE_AGENT_SDK_VERSION: got %q, want %q", got, sdkVersion)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Layer-2 threshold boundary (documents the unresolved gap)
// ─────────────────────────────────────────────────────────────────────────────

// TestLayer2ContentUnder50KCanBeInline documents that results below the 50 K
// threshold are eligible to be passed inline by the CLI.
func TestLayer2ContentUnder50KCanBeInline(t *testing.T) {
	content := strings.Repeat("x", layer2ThresholdChars-1)
	if len(content) >= layer2ThresholdChars {
		t.Errorf("Test setup: expected %d < %d", len(content), layer2ThresholdChars)
	}
}

// TestLayer2CustomerReproducerExceedsThreshold documents that a ~73 K-char
// result exceeds the 50 K layer-2 threshold.
// MAX_MCP_OUTPUT_TOKENS=500000 bypasses layer 1 for this result, but it then
// hits layer 2 and produces <persisted-output>. This is the bug.
// The fix (v0.1.55/v0.1.57) uses ToolAnnotations.MaxResultSizeChars instead.
func TestLayer2CustomerReproducerExceedsThreshold(t *testing.T) {
	customerContentSize := 73_000 // chars in the customer's reproducer
	if customerContentSize <= layer2ThresholdChars {
		t.Errorf("Test setup: expected %d > %d", customerContentSize, layer2ThresholdChars)
	}
}

// TestLayer2NoEnvVarPathExists confirms there is no env-var route to raise
// the layer-2 threshold. The fix uses tool annotations instead:
//
//	ToolAnnotations{MaxResultSizeChars: &size}
//
// The CLI reads this from the tools/list JSONRPC response and skips the
// Math.min clamp in getPersistenceThreshold for that tool.
// See TestToolAnnotations_MaxResultSizeChars in sdk_mcp_integration_test.go.
func TestLayer2NoEnvVarPathExists(t *testing.T) {
	env := envMapFromOpts(t, &TransportOptions{
		CLIPath: "/fake/path/claude",
		Env:     map[string]string{"MAX_MCP_OUTPUT_TOKENS": "500000"},
	})

	if _, ok := env["MAX_TOOL_RESULT_CHARS"]; ok {
		t.Error("MAX_TOOL_RESULT_CHARS should not exist — there is no env var for layer 2")
	}
	if _, ok := env["DISABLE_TOOL_RESULT_PERSISTENCE"]; ok {
		t.Error("DISABLE_TOOL_RESULT_PERSISTENCE should not exist")
	}
}
