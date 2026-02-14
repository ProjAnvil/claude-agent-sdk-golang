// Package main demonstrates basic usage of the Claude Agent SDK.
package main

import (
	"context"
	"fmt"
	"log"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

func main() {
	ctx := context.Background()

	// Basic example - simple question
	fmt.Println("=== Basic Example ===")
	basicExample(ctx)

	// With options example
	fmt.Println("\n=== With Options Example ===")
	withOptionsExample(ctx)
}

func basicExample(ctx context.Context) {
	messages, errs := claude.Query(ctx, "What is 2 + 2?", nil)

	for {
		select {
		case msg, ok := <-messages:
			if !ok {
				return
			}
			if assistant, ok := msg.(*claude.AssistantMessage); ok {
				for _, block := range assistant.Content {
					if text, ok := block.(*claude.TextBlock); ok {
						fmt.Printf("Claude: %s\n", text.Text)
					}
				}
			}
		case err := <-errs:
			if err != nil {
				log.Printf("Error: %v\n", err)
			}
		}
	}
}

func withOptionsExample(ctx context.Context) {
	opts := &claude.ClaudeAgentOptions{
		SystemPrompt: "You are a helpful assistant that explains things simply.",
		MaxTurns:     1,
	}

	messages, errs := claude.Query(ctx, "Explain what Go is in one sentence.", opts)

	for {
		select {
		case msg, ok := <-messages:
			if !ok {
				return
			}
			if assistant, ok := msg.(*claude.AssistantMessage); ok {
				for _, block := range assistant.Content {
					if text, ok := block.(*claude.TextBlock); ok {
						fmt.Printf("Claude: %s\n", text.Text)
					}
				}
			}
			if result, ok := msg.(*claude.ResultMessage); ok {
				if result.TotalCostUSD > 0 {
					fmt.Printf("\nCost: $%.4f\n", result.TotalCostUSD)
				}
			}
		case err := <-errs:
			if err != nil {
				log.Printf("Error: %v\n", err)
			}
		}
	}
}
