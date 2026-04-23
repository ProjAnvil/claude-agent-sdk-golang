package claude

import (
	"testing"
)

// TestDefaultOptions tests Options with default values.
func TestDefaultOptions(t *testing.T) {
	options := DefaultOptions()
	if len(options.AllowedTools) != 0 {
		t.Errorf("Expected AllowedTools=[], got %v", options.AllowedTools)
	}
	if options.SystemPrompt != "" {
		t.Errorf("Expected SystemPrompt='', got %q", options.SystemPrompt)
	}
	if options.PermissionMode != "" {
		t.Errorf("Expected PermissionMode='', got %q", options.PermissionMode)
	}
	if options.ContinueConversation {
		t.Error("Expected ContinueConversation=false")
	}
	if len(options.DisallowedTools) != 0 {
		t.Errorf("Expected DisallowedTools=[], got %v", options.DisallowedTools)
	}
}

// TestOptionsWithTools tests Options with built-in tools.
func TestOptionsWithTools(t *testing.T) {
	options := &ClaudeAgentOptions{
		AllowedTools:    []string{"Read", "Write", "Edit"},
		DisallowedTools: []string{"Bash"},
	}

	if len(options.AllowedTools) != 3 {
		t.Errorf("Expected 3 allowed tools, got %d", len(options.AllowedTools))
	}
	if options.DisallowedTools[0] != "Bash" {
		t.Errorf("Expected DisallowedTools[0]='Bash'")
	}
}

// TestOptionsWithPermissionMode tests Options with permission mode.
func TestOptionsWithPermissionMode(t *testing.T) {
	opts := &ClaudeAgentOptions{PermissionMode: PermissionModeBypassPermissions}
	if opts.PermissionMode != PermissionModeBypassPermissions {
		t.Errorf("Expected %v, got %v", PermissionModeBypassPermissions, opts.PermissionMode)
	}

	opts = &ClaudeAgentOptions{PermissionMode: PermissionModePlan}
	if opts.PermissionMode != PermissionModePlan {
		t.Errorf("Expected %v, got %v", PermissionModePlan, opts.PermissionMode)
	}

	opts = &ClaudeAgentOptions{PermissionMode: PermissionModeDefault}
	if opts.PermissionMode != PermissionModeDefault {
		t.Errorf("Expected %v, got %v", PermissionModeDefault, opts.PermissionMode)
	}

	opts = &ClaudeAgentOptions{PermissionMode: PermissionModeAcceptEdits}
	if opts.PermissionMode != PermissionModeAcceptEdits {
		t.Errorf("Expected %v, got %v", PermissionModeAcceptEdits, opts.PermissionMode)
	}

	opts = &ClaudeAgentOptions{PermissionMode: PermissionModeAuto}
	if opts.PermissionMode != PermissionModeAuto {
		t.Errorf("Expected %v, got %v", PermissionModeAuto, opts.PermissionMode)
	}
}

// TestOptionsWithSystemPromptString tests Options with system prompt as string.
func TestOptionsWithSystemPromptString(t *testing.T) {
	opts := &ClaudeAgentOptions{SystemPrompt: "You are a helpful assistant."}
	if opts.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("Expected system prompt mismatch")
	}
}

// TestOptionsWithSystemPromptPreset tests Options with system prompt preset.
func TestOptionsWithSystemPromptPreset(t *testing.T) {
	opts := &ClaudeAgentOptions{
		SystemPromptPreset: &SystemPromptPreset{
			Type:   "preset",
			Preset: "claude_code",
		},
	}
	if opts.SystemPromptPreset.Type != "preset" {
		t.Errorf("Expected Type='preset'")
	}
	if opts.SystemPromptPreset.Preset != "claude_code" {
		t.Errorf("Expected Preset='claude_code'")
	}
}

// TestOptionsWithSystemPromptPresetAndAppend tests Options with preset and append.
func TestOptionsWithSystemPromptPresetAndAppend(t *testing.T) {
	opts := &ClaudeAgentOptions{
		SystemPromptPreset: &SystemPromptPreset{
			Type:   "preset",
			Preset: "claude_code",
			Append: "Be concise.",
		},
	}
	if opts.SystemPromptPreset.Append != "Be concise." {
		t.Errorf("Expected Append='Be concise.'")
	}
}

// TestOptionsWithSystemPromptPresetAndExcludeDynamicSections tests Options with
// preset and exclude_dynamic_sections (v0.1.57).
func TestOptionsWithSystemPromptPresetAndExcludeDynamicSections(t *testing.T) {
	trueVal := true
	opts := &ClaudeAgentOptions{
		SystemPromptPreset: &SystemPromptPreset{
			Type:                   "preset",
			Preset:                 "claude_code",
			ExcludeDynamicSections: &trueVal,
		},
	}
	if opts.SystemPromptPreset.ExcludeDynamicSections == nil {
		t.Fatal("Expected ExcludeDynamicSections to be non-nil")
	}
	if *opts.SystemPromptPreset.ExcludeDynamicSections != true {
		t.Errorf("Expected ExcludeDynamicSections=true, got %v", *opts.SystemPromptPreset.ExcludeDynamicSections)
	}
}

// TestOptionsWithSessionContinuation tests Options with session continuation.
func TestOptionsWithSessionContinuation(t *testing.T) {
	opts := &ClaudeAgentOptions{
		ContinueConversation: true,
		Resume:               "session-123",
	}
	if !opts.ContinueConversation {
		t.Error("Expected ContinueConversation=true")
	}
	if opts.Resume != "session-123" {
		t.Errorf("Expected Resume='session-123'")
	}
}

// TestOptionsWithModelSpecification tests Options with model specification.
func TestOptionsWithModelSpecification(t *testing.T) {
	opts := &ClaudeAgentOptions{
		Model:                    "claude-sonnet-4-5",
		PermissionPromptToolName: "CustomTool",
	}
	if opts.Model != "claude-sonnet-4-5" {
		t.Errorf("Expected Model='claude-sonnet-4-5'")
	}
	if opts.PermissionPromptToolName != "CustomTool" {
		t.Errorf("Expected PermissionPromptToolName='CustomTool'")
	}
}

// ---- Tests for new options added in v0.1.58–v0.1.65 ----

// TestOptionsWithSkillsAll verifies that Skills="all" is stored correctly.
func TestOptionsWithSkillsAll(t *testing.T) {
	opts := &ClaudeAgentOptions{Skills: "all"}
	s, ok := opts.Skills.(string)
	if !ok || s != "all" {
		t.Errorf("Expected Skills=\"all\", got %v", opts.Skills)
	}
}

// TestOptionsWithSkillsList verifies that Skills=[]string{...} is stored correctly.
func TestOptionsWithSkillsList(t *testing.T) {
	skills := []string{"my-skill", "other-skill"}
	opts := &ClaudeAgentOptions{Skills: skills}
	got, ok := opts.Skills.([]string)
	if !ok {
		t.Fatalf("Expected []string, got %T", opts.Skills)
	}
	if len(got) != 2 || got[0] != "my-skill" {
		t.Errorf("Skills mismatch: %v", got)
	}
}

// TestOptionsWithNilSkills verifies nil Skills is the default.
func TestOptionsWithNilSkills(t *testing.T) {
	opts := DefaultOptions()
	if opts.Skills != nil {
		t.Errorf("Expected nil Skills by default, got %v", opts.Skills)
	}
}

// TestOptionsWithSessionStore verifies that SessionStore is stored correctly.
func TestOptionsWithSessionStore(t *testing.T) {
	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{SessionStore: store}
	if opts.SessionStore == nil {
		t.Error("Expected non-nil SessionStore")
	}
}

// TestOptionsThinkingConfigDisplay verifies the new Display field.
func TestOptionsThinkingConfigDisplay(t *testing.T) {
	opts := &ClaudeAgentOptions{
		Thinking: &ThinkingConfig{
			Type:    "adaptive",
			Display: "summarized",
		},
	}
	if opts.Thinking.Display != "summarized" {
		t.Errorf("Thinking.Display mismatch: %s", opts.Thinking.Display)
	}
}
