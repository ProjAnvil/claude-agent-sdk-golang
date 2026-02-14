// Package main demonstrates custom MCP tools with the Claude Agent SDK.
package main

import (
	"context"
	"fmt"
	"log"
	"math"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

func main() {
	ctx := context.Background()

	// Define calculator tools
	addTool := claude.Tool("add", "Add two numbers", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]interface{}{"type": "number", "description": "First number"},
			"b": map[string]interface{}{"type": "number", "description": "Second number"},
		},
		"required": []string{"a", "b"},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		a := args["a"].(float64)
		b := args["b"].(float64)
		return claude.ToolResponse(fmt.Sprintf("%.2f + %.2f = %.2f", a, b, a+b)), nil
	})

	subtractTool := claude.Tool("subtract", "Subtract one number from another", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]interface{}{"type": "number", "description": "Number to subtract from"},
			"b": map[string]interface{}{"type": "number", "description": "Number to subtract"},
		},
		"required": []string{"a", "b"},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		a := args["a"].(float64)
		b := args["b"].(float64)
		return claude.ToolResponse(fmt.Sprintf("%.2f - %.2f = %.2f", a, b, a-b)), nil
	})

	multiplyTool := claude.Tool("multiply", "Multiply two numbers", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]interface{}{"type": "number"},
			"b": map[string]interface{}{"type": "number"},
		},
		"required": []string{"a", "b"},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		a := args["a"].(float64)
		b := args["b"].(float64)
		return claude.ToolResponse(fmt.Sprintf("%.2f × %.2f = %.2f", a, b, a*b)), nil
	})

	divideTool := claude.Tool("divide", "Divide one number by another", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]interface{}{"type": "number", "description": "Dividend"},
			"b": map[string]interface{}{"type": "number", "description": "Divisor"},
		},
		"required": []string{"a", "b"},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		a := args["a"].(float64)
		b := args["b"].(float64)
		if b == 0 {
			return claude.ToolErrorResponse("Error: Division by zero is not allowed"), nil
		}
		return claude.ToolResponse(fmt.Sprintf("%.2f ÷ %.2f = %.2f", a, b, a/b)), nil
	})

	sqrtTool := claude.Tool("sqrt", "Calculate square root", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"n": map[string]interface{}{"type": "number", "description": "Number to find square root of"},
		},
		"required": []string{"n"},
	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
		n := args["n"].(float64)
		if n < 0 {
			return claude.ToolErrorResponse(fmt.Sprintf("Error: Cannot calculate square root of negative number %.2f", n)), nil
		}
		return claude.ToolResponse(fmt.Sprintf("√%.2f = %.2f", n, math.Sqrt(n))), nil
	})

	// Create the calculator server
	calculator := claude.CreateSdkMcpServer("calculator", "1.0.0", []claude.SdkMcpTool{
		addTool,
		subtractTool,
		multiplyTool,
		divideTool,
		sqrtTool,
	})

	// Configure Claude to use the calculator
	opts := &claude.ClaudeAgentOptions{
		MCPServers: map[string]claude.MCPServerConfig{
			"calc": calculator,
		},
		AllowedTools: []string{
			"mcp__calc__add",
			"mcp__calc__subtract",
			"mcp__calc__multiply",
			"mcp__calc__divide",
			"mcp__calc__sqrt",
		},
	}

	// Example calculations
	prompts := []string{
		"Calculate 15 + 27",
		"What is 100 divided by 7?",
		"Calculate the square root of 144",
	}

	for _, prompt := range prompts {
		fmt.Printf("\n=== %s ===\n", prompt)

		messages, errs := claude.Query(ctx, prompt, opts)

		for {
			select {
			case msg, ok := <-messages:
				if !ok {
					goto nextPrompt
				}
				displayMessage(msg)
			case err := <-errs:
				if err != nil {
					log.Printf("Error: %v\n", err)
				}
			}
		}
	nextPrompt:
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
				fmt.Printf("Using tool: %s with input: %v\n", b.Name, b.Input)
			}
		}
	case *claude.ResultMessage:
		if m.TotalCostUSD > 0 {
			fmt.Printf("Cost: $%.6f\n", m.TotalCostUSD)
		}
	}
}
