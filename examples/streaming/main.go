// Package main demonstrates ClaudeSDKClient for bidirectional conversations.
package main

import (
	"context"
	"fmt"
	"log"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

func main() {
	ctx := context.Background()

	opts := &claude.ClaudeAgentOptions{
		AllowedTools: []string{"Read", "Write", "Bash"},
	}

	client := claude.NewClient(opts)

	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	// First query
	fmt.Println("=== First Query ===")
	messages, err := client.Query(ctx, "What is the current directory?")
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}

	for msg := range messages {
		displayMessage(msg)
		if _, ok := msg.(*claude.ResultMessage); ok {
			break
		}
	}

	// Follow-up query
	fmt.Println("\n=== Follow-up Query ===")
	messages, err = client.Query(ctx, "List the files in it")
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
	case *claude.UserMessage:
		if content, ok := m.Content.(string); ok {
			fmt.Printf("User: %s\n", content)
		}
	case *claude.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case *claude.TextBlock:
				fmt.Printf("Claude: %s\n", b.Text)
			case *claude.ToolUseBlock:
				fmt.Printf("Using tool: %s\n", b.Name)
			}
		}
	case *claude.ResultMessage:
		fmt.Println("--- Response complete ---")
		if m.TotalCostUSD > 0 {
			fmt.Printf("Cost: $%.6f\n", m.TotalCostUSD)
		}
	}
}
