// Package claude provides a Go SDK for interacting with Claude Code CLI.
//
// The SDK supports two main usage patterns:
//
// 1. One-shot queries using the Query function:
//
//	messages, errs := claude.Query(ctx, "What is 2 + 2?", nil)
//	for msg := range messages {
//	    // Process messages
//	}
//
// 2. Bidirectional conversations using ClaudeSDKClient:
//
//	client := claude.NewClient(opts)
//	client.Connect(ctx)
//	defer client.Close()
//
//	messages, _ := client.Query(ctx, "Hello")
//	for msg := range messages {
//	    // Process messages
//	}
//
// The SDK also supports custom MCP tools and hooks for extending Claude's
// capabilities and controlling its behavior.
package claude
