package transport

import (
	"context"
	"strings"
	"testing"
)

// coverageFlagValue returns the token following flag in args.
func coverageFlagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// TestCoverage_BuildCommandOptionFlags exercises the buildCommand branches
// for options not covered elsewhere, asserting each one reaches the argv.
func TestCoverage_BuildCommandOptionFlags(t *testing.T) {
	opts := &TransportOptions{
		ToolsPreset:              &ToolsPreset{Type: "preset", Preset: "claude_code"},
		MaxBudgetUSD:             2.5,
		DisallowedTools:          []string{"Bash", "Write"},
		Betas:                    []string{"context-1m"},
		PermissionPromptToolName: "perm_tool",
		IncludePartialMessages:   true,
		MCPServers: map[string]interface{}{
			"remote": map[string]interface{}{"type": "http", "url": "https://example.com/mcp"},
		},
		Plugins: []PluginConfig{
			{Type: "local", Path: "/plugins/one"},
			{Type: "remote", Path: "/plugins/ignored"},
		},
		OutputFormat: map[string]interface{}{
			"type":   "json_schema",
			"schema": map[string]interface{}{"type": "object"},
		},
	}
	tr := newTestTransport(t, opts)
	args := tr.buildCommand(context.Background()).Args[1:]

	wantPairs := map[string]string{
		"--tools":                  "default",
		"--max-budget-usd":         "2.500000",
		"--disallowedTools":        "Bash,Write",
		"--betas":                  "context-1m",
		"--permission-prompt-tool": "perm_tool",
	}
	for flag, want := range wantPairs {
		got, ok := coverageFlagValue(args, flag)
		if !ok || got != want {
			t.Errorf("Expected %s %q in args, got %q (found=%v): %v", flag, want, got, ok, args)
		}
	}

	if !containsToken(args, "--include-partial-messages") {
		t.Errorf("Expected --include-partial-messages in args: %v", args)
	}

	if got, ok := coverageFlagValue(args, "--plugin-dir"); !ok || got != "/plugins/one" {
		t.Errorf("Expected --plugin-dir /plugins/one, got %q (found=%v): %v", got, ok, args)
	}
	if containsToken(args, "/plugins/ignored") {
		t.Errorf("Non-local plugin must not be passed to the CLI: %v", args)
	}

	mcpConfig, ok := coverageFlagValue(args, "--mcp-config")
	if !ok || !strings.Contains(mcpConfig, `"remote"`) {
		t.Errorf("Expected --mcp-config naming the server, got %q (found=%v)", mcpConfig, ok)
	}

	schema, ok := coverageFlagValue(args, "--json-schema")
	if !ok || !strings.Contains(schema, `"object"`) {
		t.Errorf("Expected --json-schema with the schema JSON, got %q (found=%v)", schema, ok)
	}
}

// TestCoverage_BuildCommandCWDEnv verifies CWD and file checkpointing reach
// the subprocess environment.
func TestCoverage_BuildCommandCWDEnv(t *testing.T) {
	cwd := t.TempDir()
	tr := newTestTransport(t, &TransportOptions{
		CWD:                     cwd,
		EnableFileCheckpointing: true,
	})
	env := tr.buildCommand(context.Background()).Env

	wantPWD := "PWD=" + cwd
	foundPWD, foundCheckpoint := false, false
	for _, e := range env {
		if e == wantPWD {
			foundPWD = true
		}
		if e == "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true" {
			foundCheckpoint = true
		}
	}
	if !foundPWD {
		t.Errorf("Expected %q in subprocess env", wantPWD)
	}
	if !foundCheckpoint {
		t.Error("Expected CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true in subprocess env")
	}
}
