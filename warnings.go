package claude

import (
	"fmt"
	"log/slog"
	"strings"
)

// can_use_tool is only consulted when the CLI's permission ladder lands on
// "ask". Anything that auto-approves a tool call earlier in the ladder
// (bypassPermissions, or an allowed_tools entry that allows a whole tool)
// means the callback never runs — a security-critical footgun when the
// callback is the gate that restricts tool access. This file ports the
// Python SDK's #1081 advisory warning that names the shadowing option at
// query/connect time. The warning is advisory only: shadowing can be
// intentional, so it never raises.

// wholeToolAllowed reports the tool name an allowed_tools entry permits
// outright, and ok=false when the entry only allows specific invocations
// (or is malformed). It mirrors the CLI's rule parser as encoded in the
// Python SDK (#1081, _whole_tool_allowed):
//
//   - no "(...)" specifier        → "Read"      allows the whole tool
//   - empty / lone "*" specifier  → "Read()" / "Read(*)" allow the whole tool
//   - a real specifier            → "Bash(ls:*)" allows only matching calls
//   - malformed                   → "(foo)" / "Read(x" match nothing
//   - blank                       → ""          ignored
func wholeToolAllowed(entry string) (tool string, ok bool) {
	if strings.TrimSpace(entry) == "" {
		return "", false
	}
	openIndex := strings.Index(entry, "(")
	if openIndex == -1 {
		return entry, true
	}
	// A "(" at index 0, or a missing closing ")", means the entry is
	// malformed; the CLI falls back to the whole string as a (non-matching)
	// tool name, so it shadows nothing.
	if openIndex == 0 || !strings.HasSuffix(entry, ")") {
		return "", false
	}
	inner := entry[openIndex+1 : len(entry)-1]
	if inner == "" || inner == "*" {
		return entry[:openIndex], true
	}
	return "", false
}

// canUseToolShadowedMessage returns the advisory text describing how
// can_use_tool is shadowed by the given options, or "" when nothing shadows
// it. Mirrors Python SDK #1081's _get_can_use_tool_shadowed_warning.
//
// bypassPermissions takes precedence: it auto-approves everything except
// explicit deny rules, so the callback is fully shadowed. Otherwise the
// whole-tool allowed_tools entries are named, deduped and order-preserving.
func canUseToolShadowedMessage(permissionMode PermissionMode, allowedTools []string) string {
	if permissionMode == PermissionModeBypassPermissions {
		return "can_use_tool will not be invoked: permission_mode 'bypassPermissions' " +
			"auto-approves every tool call (except explicit deny rules) before the " +
			"callback is consulted. To gate every tool call, use a PreToolUse hook instead."
	}

	// dict.fromkeys in Python: dedupe while preserving order. Redundant
	// configs like ["Read", "Read()"] resolve to the same tool and must not
	// report it twice.
	seen := make(map[string]bool, len(allowedTools))
	var shadowed []string
	for _, entry := range allowedTools {
		tool, ok := wholeToolAllowed(entry)
		if !ok || seen[tool] {
			continue
		}
		seen[tool] = true
		shadowed = append(shadowed, tool)
	}
	if len(shadowed) == 0 {
		return ""
	}
	return fmt.Sprintf("can_use_tool will not be invoked for: %s. "+
		"An allowed_tools entry that allows a whole tool auto-approves it "+
		"before the callback is consulted. To gate every tool call, use a "+
		"PreToolUse hook; or narrow the entry so calls fall through to "+
		"can_use_tool. Allow rules from settings files can also shadow the "+
		"callback but are not visible here.", strings.Join(shadowed, ", "))
}

// warnIfCanUseToolShadowed emits a single advisory slog warning when the
// caller's CanUseTool callback is set alongside options that visibly shadow
// it. No-op when CanUseTool is nil or nothing shadows it.
//
// skills="all" makes the transport append a bare "Skill" to the effective
// allowed_tools (see applySkillsDefaults in subprocess.go), so it shadows the
// callback just like a hand-written entry; skills=[]string{...} appends
// Skill(name) specifiers, which do not. Mirrors Python SDK #1081's
// _warn_if_can_use_tool_shadowed.
func warnIfCanUseToolShadowed(opts *ClaudeAgentOptions) {
	if opts == nil || opts.CanUseTool == nil {
		return
	}

	allowedTools := opts.AllowedTools
	if skills, ok := opts.Skills.(string); ok && skills == "all" {
		if !containsString(allowedTools, "Skill") {
			// Copy before appending so opts.AllowedTools is not mutated.
			allowedTools = append([]string{}, allowedTools...)
			allowedTools = append(allowedTools, "Skill")
		}
	}

	if msg := canUseToolShadowedMessage(opts.PermissionMode, allowedTools); msg != "" {
		slog.Warn("can_use_tool is shadowed by other permission options",
			"message", msg)
	}
}

// containsString reports whether ss contains s.
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
