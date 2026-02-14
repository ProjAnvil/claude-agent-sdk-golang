package claude

import (
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
)

// SdkMcpTool defines a custom MCP tool.
type SdkMcpTool struct {
	Name        string
	Description string
	InputSchema interface{}
	Handler     func(args map[string]interface{}) (map[string]interface{}, error)
}

// ToolBuilder provides a fluent interface for building tools.
type ToolBuilder struct {
	name        string
	description string
	inputSchema interface{}
}

// Tool creates a new ToolBuilder for defining an MCP tool.
//
// Example:
//
//	addTool := claude.Tool("add", "Add two numbers", map[string]interface{}{
//	    "type": "object",
//	    "properties": map[string]interface{}{
//	        "a": map[string]interface{}{"type": "number"},
//	        "b": map[string]interface{}{"type": "number"},
//	    },
//	}).Handler(func(args map[string]interface{}) (map[string]interface{}, error) {
//	    a := args["a"].(float64)
//	    b := args["b"].(float64)
//	    return map[string]interface{}{
//	        "content": []map[string]interface{}{
//	            {"type": "text", "text": fmt.Sprintf("%.2f + %.2f = %.2f", a, b, a+b)},
//	        },
//	    }, nil
//	})
func Tool(name, description string, inputSchema interface{}) *ToolBuilder {
	return &ToolBuilder{
		name:        name,
		description: description,
		inputSchema: inputSchema,
	}
}

// Handler sets the handler function for the tool and returns the completed SdkMcpTool.
func (b *ToolBuilder) Handler(fn func(args map[string]interface{}) (map[string]interface{}, error)) SdkMcpTool {
	return SdkMcpTool{
		Name:        b.name,
		Description: b.description,
		InputSchema: b.inputSchema,
		Handler:     fn,
	}
}

// CreateSdkMcpServer creates an in-process MCP server.
//
// Example:
//
//	calculator := claude.CreateSdkMcpServer("calculator", "1.0.0", []claude.SdkMcpTool{
//	    addTool,
//	    subtractTool,
//	})
//
//	opts := &claude.ClaudeAgentOptions{
//	    MCPServers: map[string]claude.MCPServerConfig{
//	        "calc": calculator,
//	    },
//	    AllowedTools: []string{"mcp__calc__add", "mcp__calc__subtract"},
//	}
func CreateSdkMcpServer(name, version string, tools []SdkMcpTool) *MCPSdkServerConfig {
	// Convert to internal MCP server
	internalTools := make([]internal.MCPTool, len(tools))
	for i, tool := range tools {
		tool := tool // capture
		internalTools[i] = internal.MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			Handler:     tool.Handler,
		}
	}

	server := &internal.MCPServer{
		Name:    name,
		Version: version,
		Tools:   internalTools,
	}

	return &MCPSdkServerConfig{
		Type:     "sdk",
		Name:     name,
		Instance: server,
	}
}

// TextContent creates a text content block for tool responses.
func TextContent(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": text,
	}
}

// ToolResponse creates a standard tool response with text content.
func ToolResponse(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			TextContent(text),
		},
	}
}

// ToolErrorResponse creates an error tool response.
func ToolErrorResponse(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			TextContent(text),
		},
		"is_error": true,
	}
}
