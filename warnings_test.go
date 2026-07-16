package claude

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureSlog replaces the default slog logger with one writing to a buffer
// at LevelWarn (so Warn-level advisories are captured) and restores it on
// test cleanup. Tests that don't expect a warning use it to assert silence.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// noopCanUseTool is a non-nil CanUseTool callback used to exercise the
// "callback is set" branch without caring about its result.
func noopCanUseTool(toolName string, input map[string]interface{}, ctx ToolPermissionContext) (PermissionResult, error) {
	return &PermissionResultAllow{}, nil
}

func TestWholeToolAllowed(t *testing.T) {
	tests := []struct {
		entry   string
		tool    string
		ok      bool
	}{
		{"Read", "Read", true},
		{"Read()", "Read", true},
		{"Read(*)", "Read", true},
		{"Bash(ls:*)", "", false}, // real specifier → only matching calls
		{"Bash(ls)", "", false},
		{"(foo)", "", false},  // "(" at index 0 → malformed
		{"Read(x", "", false}, // no closing ")" → malformed
		{"", "", false},       // blank
		{"   ", "", false},    // whitespace only
		{"Skill", "Skill", true},
		{"Skill(my-skill)", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.entry, func(t *testing.T) {
			tool, ok := wholeToolAllowed(tc.entry)
			if tool != tc.tool || ok != tc.ok {
				t.Errorf("wholeToolAllowed(%q) = (%q, %v), want (%q, %v)",
					tc.entry, tool, ok, tc.tool, tc.ok)
			}
		})
	}
}

func TestCanUseToolShadowedMessage_BypassPermissions(t *testing.T) {
	msg := canUseToolShadowedMessage(PermissionModeBypassPermissions, []string{"Read"})
	if msg == "" {
		t.Fatal("Expected non-empty message for bypassPermissions")
	}
	if !strings.Contains(msg, "bypassPermissions") {
		t.Errorf("Expected message to mention bypassPermissions, got: %s", msg)
	}
	if !strings.Contains(msg, "PreToolUse hook") {
		t.Errorf("Expected message to suggest a PreToolUse hook, got: %s", msg)
	}
}

func TestCanUseToolShadowedMessage_BypassPrecedence(t *testing.T) {
	// When bypassPermissions is set, the whole-tool list is not consulted —
	// the message names bypass, never individual tools.
	msg := canUseToolShadowedMessage(PermissionModeBypassPermissions, []string{"Read", "Grep"})
	if strings.Contains(msg, "Read, Grep") {
		t.Errorf("bypassPermissions should take precedence and not list tools, got: %s", msg)
	}
}

func TestCanUseToolShadowedMessage_WholeToolsDedupesAndOrders(t *testing.T) {
	// "Read" and "Read()" resolve to the same tool — reported once, in order.
	msg := canUseToolShadowedMessage(PermissionModeDefault, []string{"Read", "Grep", "Read()"})
	if msg == "" {
		t.Fatal("Expected non-empty message for whole-tool entries")
	}
	if !strings.Contains(msg, "Read, Grep") {
		t.Errorf("Expected deduped, order-preserving 'Read, Grep', got: %s", msg)
	}
}

func TestCanUseToolShadowedMessage_SpecifiersNotShadowed(t *testing.T) {
	// A real specifier only allows matching invocations — not a whole tool.
	msg := canUseToolShadowedMessage(PermissionModeDefault, []string{"Bash(ls:*)", "Skill(my)"})
	if msg != "" {
		t.Errorf("Expected empty message for specifiers only, got: %s", msg)
	}
}

func TestCanUseToolShadowedMessage_None(t *testing.T) {
	if msg := canUseToolShadowedMessage(PermissionModeDefault, nil); msg != "" {
		t.Errorf("Expected empty message when nothing shadows, got: %s", msg)
	}
}

func TestWarnIfCanUseToolShadowed_NilOpts(t *testing.T) {
	buf := captureSlog(t)
	warnIfCanUseToolShadowed(nil)
	if buf.Len() != 0 {
		t.Errorf("Expected no warning for nil opts, got: %s", buf.String())
	}
}

func TestWarnIfCanUseToolShadowed_NoCallback(t *testing.T) {
	buf := captureSlog(t)
	// CanUseTool is nil → no warning even with shadowing options.
	warnIfCanUseToolShadowed(&ClaudeAgentOptions{
		PermissionMode: PermissionModeBypassPermissions,
	})
	if buf.Len() != 0 {
		t.Errorf("Expected no warning when CanUseTool is nil, got: %s", buf.String())
	}
}

func TestWarnIfCanUseToolShadowed_NoShadow(t *testing.T) {
	buf := captureSlog(t)
	warnIfCanUseToolShadowed(&ClaudeAgentOptions{
		CanUseTool: noopCanUseTool,
		// No bypass, no whole-tool entries.
	})
	if buf.Len() != 0 {
		t.Errorf("Expected no warning when nothing shadows, got: %s", buf.String())
	}
}

func TestWarnIfCanUseToolShadowed_BypassPermissions(t *testing.T) {
	buf := captureSlog(t)
	warnIfCanUseToolShadowed(&ClaudeAgentOptions{
		CanUseTool:     noopCanUseTool,
		PermissionMode: PermissionModeBypassPermissions,
	})
	out := buf.String()
	if !strings.Contains(out, "shadowed") {
		t.Errorf("Expected shadowed warning in slog output, got: %s", out)
	}
	if !strings.Contains(out, "bypassPermissions") {
		t.Errorf("Expected bypassPermissions in slog output, got: %s", out)
	}
}

func TestWarnIfCanUseToolShadowed_WholeToolEntry(t *testing.T) {
	buf := captureSlog(t)
	warnIfCanUseToolShadowed(&ClaudeAgentOptions{
		CanUseTool:    noopCanUseTool,
		AllowedTools:  []string{"Read", "Grep"},
	})
	out := buf.String()
	if !strings.Contains(out, "Read, Grep") {
		t.Errorf("Expected whole-tool names in slog output, got: %s", out)
	}
}

func TestWarnIfCanUseToolShadowed_SkillsAllInjectsSkill(t *testing.T) {
	buf := captureSlog(t)
	// skills="all" injects a bare "Skill" into effective allowed_tools,
	// shadowing the callback even when AllowedTools is empty.
	warnIfCanUseToolShadowed(&ClaudeAgentOptions{
		CanUseTool: noopCanUseTool,
		Skills:     "all",
	})
	out := buf.String()
	if !strings.Contains(out, "Skill") {
		t.Errorf("Expected Skill in shadowed warning, got: %s", out)
	}
}

func TestWarnIfCanUseToolShadowed_SkillsListDoesNotInject(t *testing.T) {
	buf := captureSlog(t)
	// skills=[]string{...} appends Skill(name) specifiers, which do NOT
	// shadow the callback.
	warnIfCanUseToolShadowed(&ClaudeAgentOptions{
		CanUseTool: noopCanUseTool,
		Skills:     []string{"my-skill"},
	})
	if buf.Len() != 0 {
		t.Errorf("Expected no warning for skills list, got: %s", buf.String())
	}
}

func TestWarnIfCanUseToolShadowed_DoesNotMutateOptions(t *testing.T) {
	captureSlog(t) // swallow the advisory
	opts := &ClaudeAgentOptions{
		CanUseTool:    noopCanUseTool,
		Skills:        "all",
		AllowedTools:  []string{"Read"},
	}
	before := len(opts.AllowedTools)
	warnIfCanUseToolShadowed(opts)
	if len(opts.AllowedTools) != before {
		t.Errorf("warnIfCanUseToolShadowed mutated opts.AllowedTools: had %d, now %d",
			before, len(opts.AllowedTools))
	}
}
