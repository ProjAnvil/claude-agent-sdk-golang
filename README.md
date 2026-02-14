# Claude Agent SDK for Go

Go SDK for Claude Agent. This SDK provides a high-performance, idiomatic Go interface for interacting with Claude Code CLI, mirroring the functionality of the [Python SDK](https://github.com/anthropics/claude-agent-sdk-python).

## Installation

```bash
go get github.com/ProjAnvil/claude-agent-sdk-golang
```

**Prerequisites:**

- Go 1.25+
- Claude Code CLI installed (`curl -fsSL https://claude.ai/install.sh | bash`)

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

func main() {
	ctx := context.Background()

	// Simple one-shot query
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
						fmt.Println(text.Text)
					}
				}
			}
		case err := <-errs:
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}
```

## Basic Usage: Query()

`Query()` is a function for one-shot queries to Claude Code. It returns channels for messages and errors, allowing for idiomatic Go concurrency patterns.

```go
// Simple query
messages, errs := claude.Query(ctx, "Hello Claude", nil)

// With options
opts := &claude.ClaudeAgentOptions{
	SystemPrompt: "You are a helpful assistant",
	MaxTurns:     1,
}
messages, errs := claude.Query(ctx, "Tell me a joke", opts)

// Synchronous query (collects all messages)
allMessages, err := claude.QuerySync(ctx, "What is Go?", nil)
```

### Using Tools

```go
opts := &claude.ClaudeAgentOptions{
	AllowedTools: []string{"Read", "Write", "Bash"},
	PermissionMode: "acceptEdits", // auto-accept file edits
}

messages, errs := claude.Query(ctx, "Create a hello.go file", opts)
// Process messages...
```

### Working Directory

```go
opts := &claude.ClaudeAgentOptions{
	CWD: "/path/to/project",
}
```

## ClaudeSDKClient

`ClaudeSDKClient` supports bidirectional, interactive conversations with Claude Code.

Unlike `Query()`, `ClaudeSDKClient` additionally enables **custom tools** and **hooks**.

### Custom Tools (as In-Process SDK MCP Servers)

A **custom tool** is a Go function that you can offer to Claude. These run in-process, similar to the Python SDK's implementation.

```go
// Define a tool
addTool := claude.Tool("add", "Add two numbers", map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"a": map[string]interface{}{"type": "number"},
		"b": map[string]interface{}{"type": "number"},
	},
}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
	a := args["a"].(float64)
	b := args["b"].(float64)
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("%.2f + %.2f = %.2f", a, b, a+b)},
		},
	}, nil
})

// Create SDK MCP server
calculator := claude.CreateSdkMcpServer("calculator", "1.0.0", []claude.SdkMcpTool{addTool})

// Use it with Claude
opts := &claude.ClaudeAgentOptions{
	MCPServers:   map[string]interface{}{"calc": calculator},
	AllowedTools: []string{"mcp__calc__add"},
}

client := claude.NewClient(opts)
if err := client.Connect(ctx); err != nil {
	log.Fatal(err)
}
defer client.Close()

messages, _ := client.Query(ctx, "Add 10 and 20")
// Process response...
```

### Hooks

A **hook** is a function invoked at specific points of the Claude agent loop.

```go
opts := &claude.ClaudeAgentOptions{
	AllowedTools: []string{"Bash"},
	Hooks: map[claude.HookEvent][]claude.HookMatcher{
		claude.HookEventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []claude.HookCallback{
					func(input claude.HookInput, toolUseID string, ctx claude.HookContext) (claude.HookOutput, error) {
						// Check command before execution
						if strings.Contains(input.ToolInput["command"].(string), "rm -rf") {
							return claude.HookOutput{
								HookSpecificOutput: map[string]interface{}{
									"hookEventName":            "PreToolUse",
									"permissionDecision":       "deny",
									"permissionDecisionReason": "Dangerous command blocked",
								},
							}, nil
						}
						return claude.HookOutput{}, nil
					},
				},
			},
		},
	},
}
```

## Agents

Define custom agents with specific personalities and tools:

```go
opts := &claude.ClaudeAgentOptions{
	Agents: map[string]claude.AgentDefinition{
		"coder": {
			Description: "A specialized coding agent",
			Prompt:      "You are an expert Go developer.",
			Tools:       []string{"Bash", "Read", "Write"},
			Model:       "claude-3-7-sonnet-20250219",
		},
	},
}
```

## Thinking Mode

Enable extended thinking for complex tasks:

```go
opts := &claude.ClaudeAgentOptions{
	Thinking: &claude.ThinkingConfig{
		Type:         "adaptive", // or "enabled"
		BudgetTokens: 16000,
	},
	Model: "claude-3-7-sonnet-20250219", // Required for thinking
}
```

## Sandbox

Secure your environment by sandboxing bash commands:

```go
opts := &claude.ClaudeAgentOptions{
	Sandbox: &claude.SandboxSettings{
		Enabled: true,
		Network: &claude.SandboxNetworkConfig{
			AllowLocalBinding:   true,
		},
		ExcludedCommands: []string{"git", "go"},
	},
}
```

## Types

See `types.go` for complete type definitions:

- `ClaudeAgentOptions` - Configuration options
- `UserMessage`, `AssistantMessage`, `SystemMessage`, `ResultMessage` - Message types
- `TextBlock`, `ThinkingBlock`, `ToolUseBlock`, `ToolResultBlock` - Content blocks

## Error Handling

```go
messages, errs := claude.Query(ctx, "Hello", nil)

for {
	select {
	case msg, ok := <-messages:
		if !ok {
			return
		}
		// Process message
	case err := <-errs:
		if err != nil {
			switch e := err.(type) {
			case *claude.CLINotFoundError:
				fmt.Println("Please install Claude Code")
			case *claude.ProcessError:
				fmt.Printf("Process failed with exit code: %d\n", e.ExitCode)
			case *claude.CLIJSONDecodeError:
				fmt.Printf("Failed to parse response: %s\n", e.Line)
			default:
				fmt.Printf("Error: %v\n", err)
			}
		}
	}
}
```

## License

Use of this SDK is governed by Anthropic's [Commercial Terms of Service](https://www.anthropic.com/legal/commercial-terms).
