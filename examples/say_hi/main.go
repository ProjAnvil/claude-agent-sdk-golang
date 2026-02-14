// Package main demonstrates saying hi to Claude.
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
		SettingSources: []claude.SettingSource{
			claude.SettingSourceUser, // 使用 ~/.claude 的设置
		},
	}

	messages, errs := claude.Query(ctx, "Hi!", opts)

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
