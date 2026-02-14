// Package main demonstrates hooks with the Claude Agent SDK.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

func main() {
	ctx := context.Background()

	// Define a PreToolUse hook that blocks certain commands
	checkBashCommand := func(input claude.HookInput, toolUseID string, ctx claude.HookContext) (claude.HookOutput, error) {
		if input.ToolName != "Bash" {
			return claude.HookOutput{}, nil
		}

		command, _ := input.ToolInput["command"].(string)
		blockPatterns := []string{"rm -rf", "sudo", "foo.sh"}

		for _, pattern := range blockPatterns {
			if strings.Contains(command, pattern) {
				fmt.Printf("⚠️  Blocked command: %s\n", command)
				return claude.HookOutput{
					HookSpecificOutput: map[string]interface{}{
						"hookEventName":            "PreToolUse",
						"permissionDecision":       "deny",
						"permissionDecisionReason": fmt.Sprintf("Command contains blocked pattern: %s", pattern),
					},
				}, nil
			}
		}

		fmt.Printf("✓ Allowed command: %s\n", command)
		return claude.HookOutput{}, nil
	}

	// Define a PostToolUse hook that logs tool results
	logToolResult := func(input claude.HookInput, toolUseID string, ctx claude.HookContext) (claude.HookOutput, error) {
		fmt.Printf("📝 Tool %s completed\n", input.ToolName)
		return claude.HookOutput{}, nil
	}

	opts := &claude.ClaudeAgentOptions{
		AllowedTools: []string{"Bash"},
		Hooks: map[claude.HookEvent][]claude.HookMatcher{
			claude.HookEventPreToolUse: {
				{
					Matcher: "Bash",
					Hooks:   []claude.HookCallback{checkBashCommand},
				},
			},
			claude.HookEventPostToolUse: {
				{
					Matcher: "Bash",
					Hooks:   []claude.HookCallback{logToolResult},
				},
			},
		},
	}

	client := claude.NewClient(opts)

	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	// Test 1: Command that should be blocked
	fmt.Println("\n=== Test 1: Blocked Command ===")
	fmt.Println("Trying: ./foo.sh --help")

	messages, err := client.Query(ctx, "Run the bash command: ./foo.sh --help")
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}

	for msg := range messages {
		displayMessage(msg)
		if _, ok := msg.(*claude.ResultMessage); ok {
			break
		}
	}

	// Test 2: Command that should be allowed
	fmt.Println("\n=== Test 2: Allowed Command ===")
	fmt.Println("Trying: echo 'Hello from hooks!'")

	messages, err = client.Query(ctx, "Run the bash command: echo 'Hello from hooks!'")
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}

	for msg := range messages {
		displayMessage(msg)
		if _, ok := msg.(*claude.ResultMessage); ok {
			break
		}
	}
}

func displayMessage(msg claude.Message) {
	switch m := msg.(type) {
	case *claude.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case *claude.TextBlock:
				fmt.Printf("Claude: %s\n", b.Text)
			case *claude.ToolUseBlock:
				fmt.Printf("Tool: %s\n", b.Name)
			}
		}
	case *claude.ResultMessage:
		fmt.Println("--- Done ---")
	}
}
