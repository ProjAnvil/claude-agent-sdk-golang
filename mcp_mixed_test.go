package claude

import (
	"context"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal"
	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// TestMixedMCPServers verifies that SDK and external MCP servers can be configured together.
func TestMixedMCPServers(t *testing.T) {
	// Save original factory
	originalMakeTransport := makeTransport
	defer func() { makeTransport = originalMakeTransport }()

	// Setup mock transport
	mockTrans := newMockTransport()
	handleInitialization(mockTrans, nil)

	// Capture options passed to transport
	var capturedOpts *transport.TransportOptions

	// Override factory
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockTrans, nil
	}

	// Create an SDK server
	sdkTool := internal.MCPTool{
		Name:        "sdk_tool",
		Description: "SDK tool",
		InputSchema: map[string]interface{}{},
		Handler: func(args map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "from SDK"}, nil
		},
	}
	sdkServerInstance := &internal.MCPServer{
		Name:  "sdk-server",
		Tools: []internal.MCPTool{sdkTool},
	}

	sdkServerConfig := &MCPSdkServerConfig{
		Name:     "sdk-server",
		Instance: sdkServerInstance,
	}

	// Create an external server config
	externalServerConfig := &MCPStdioServerConfig{
		Command: "echo",
		Args:    []string{"test"},
		Env:     map[string]string{"TEST": "1"},
	}

	// Create options with both
	opts := &ClaudeAgentOptions{
		MCPServers: map[string]MCPServerConfig{
			"sdk":      sdkServerConfig,
			"external": externalServerConfig,
		},
	}

	ctx := context.Background()
	// trigger Query to run the conversion logic
	messages, errs := Query(ctx, "test", opts)

	// Wait for transport to be created (which happens in the goroutine)
	// We need to consume channels to let it proceed or just wait a bit
	// Since we mock makeTransport, it should happen quickly.
	// But Query runs in a goroutine.

	// Helper to wait for capture
	timeout := time.After(1 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for transport creation")
		case <-ticker.C:
			if capturedOpts != nil {
				goto Verified
			}
		case <-errs:
			// Just drain errors
		case <-messages:
			// Just drain messages
		}
	}

Verified:
	// Verify transport options
	if capturedOpts.MCPServers == nil {
		t.Fatal("Expected MCPServers in transport options")
	}

	// Verify SDK server is present as type "sdk"
	sdkConfig, ok := capturedOpts.MCPServers["sdk"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected sdk server config map")
	}
	if sdkConfig["type"] != "sdk" {
		t.Errorf("Expected sdk server type 'sdk', got %v", sdkConfig["type"])
	}
	if sdkConfig["name"] != "sdk-server" {
		t.Errorf("Expected sdk server name 'sdk-server', got %v", sdkConfig["name"])
	}

	// Verify External server is present as type "stdio"
	extConfig, ok := capturedOpts.MCPServers["external"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected external server config map")
	}
	if extConfig["type"] != "stdio" {
		t.Errorf("Expected external server type 'stdio', got %v", extConfig["type"])
	}
	if extConfig["command"] != "echo" {
		t.Errorf("Expected external server command 'echo', got %v", extConfig["command"])
	}

	args, ok := extConfig["args"].([]string)
	if !ok {
		// It might be []interface{} depending on how it's constructed/converted?
		// convertToTransportOptions uses concrete []string from MCPStdioServerConfig
		// But let's check.
		t.Logf("Args type: %T", extConfig["args"])
	}
	// Actually in convertToTransportOptions it assigns c.Args which is []string.

	if len(args) != 1 || args[0] != "test" {
		t.Errorf("Expected external server args ['test'], got %v", args)
	}

	// Clean up channels
	go func() {
		for range messages {
		}
	}()
	go func() {
		for range errs {
		}
	}()
}
