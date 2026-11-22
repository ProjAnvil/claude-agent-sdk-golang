package claude

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// gapTransport is a minimal Transport for the custom-transport tests. It
// answers the initialize handshake and then closes its message stream.
type gapTransport struct {
	mu        sync.Mutex
	connected bool
	readCh    chan map[string]interface{}
	errCh     chan error
	closeOnce sync.Once
}

func newGapTransport() *gapTransport {
	return &gapTransport{
		readCh: make(chan map[string]interface{}, 10),
		errCh:  make(chan error, 10),
	}
}

func (g *gapTransport) Connect(context.Context) error {
	g.mu.Lock()
	g.connected = true
	g.mu.Unlock()
	return nil
}

func (g *gapTransport) WasConnected() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.connected
}

func (g *gapTransport) Write(data string) error {
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return err
	}
	if msg["type"] != "control_request" {
		return nil
	}
	reqID, _ := msg["request_id"].(string)
	req, _ := msg["request"].(map[string]interface{})
	if req["subtype"] != "initialize" {
		return nil
	}
	g.readCh <- map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "success",
			"request_id": reqID,
			"response":   map[string]interface{}{"version": "0.1.0"},
		},
	}
	g.readCh <- map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": []interface{}{},
			"model":   "test-model",
		},
	}
	g.closeOnce.Do(func() { close(g.readCh); close(g.errCh) })
	return nil
}

func (g *gapTransport) ReadMessages() <-chan map[string]interface{} { return g.readCh }
func (g *gapTransport) Errors() <-chan error                        { return g.errCh }
func (g *gapTransport) EndInput() error                             { return nil }
func (g *gapTransport) Close() error {
	g.closeOnce.Do(func() { close(g.readCh); close(g.errCh) })
	return nil
}
func (g *gapTransport) IsReady() bool { return true }

// TestQueryWithTransportBypassesSubprocessFactory proves the public
// Transport option and QueryWithTransport replace the built-in factory.
func TestQueryWithTransportBypassesSubprocessFactory(t *testing.T) {
	custom := newGapTransport()
	orig := makeTransport
	factoryCalls := 0
	makeTransport = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		factoryCalls++
		return nil, nil
	}
	t.Cleanup(func() { makeTransport = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msgs, errs := QueryWithTransport(ctx, "hi", nil, custom)

	got := 0
	for msgs != nil || errs != nil {
		select {
		case _, ok := <-msgs:
			if !ok {
				msgs = nil
				continue
			}
			got++
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				t.Errorf("QueryWithTransport returned an error: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("QueryWithTransport did not finish")
		}
	}
	if factoryCalls != 0 {
		t.Errorf("Custom transport must bypass the subprocess factory, got %d calls", factoryCalls)
	}
	if !custom.WasConnected() {
		t.Error("Expected Connect on the custom transport")
	}
	if got == 0 {
		t.Error("Expected at least one message from the custom transport")
	}
}

// TestNewClientWithCustomTransport proves ClaudeSDKClient accepts a custom
// transport and does not call its subprocess factory.
func TestNewClientWithCustomTransport(t *testing.T) {
	custom := newGapTransport()
	client := NewClient(nil, custom)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		t.Error("NewClient must not call the subprocess factory with a custom transport")
		return nil, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !custom.WasConnected() {
		t.Error("Expected Connect on the custom transport")
	}
	_ = client.Close()
}

// TestReceiveMessagesDoesNotStopAtResult mirrors Python's
// receive_messages(): the iterator keeps going past a ResultMessage.
func TestReceiveMessagesDoesNotStopAtResult(t *testing.T) {
	mockT := newMockTransport()
	handleInitialization(mockT, nil)
	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch, err := client.ReceiveMessages(ctx)
	if err != nil {
		t.Fatalf("ReceiveMessages failed: %v", err)
	}

	go func() {
		mockT.readCh <- gapAssistantFrame("a")
		mockT.readCh <- gapResultFrame()
		mockT.readCh <- gapAssistantFrame("b")
	}()

	want := []string{"assistant", "result", "assistant"}
	for i, kind := range want {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed after %d messages, want %d", i, len(want))
			}
			if got := messageKind(msg); got != kind {
				t.Errorf("message %d: got %s, want %s", i, got, kind)
			}
		case <-ctx.Done():
			t.Fatalf("timed out after %d messages", i)
		}
	}
}

func gapAssistantFrame(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": text}},
			"model":   "test-model",
		},
	}
}

func gapResultFrame() map[string]interface{} {
	return map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1),
		"duration_api_ms": float64(1),
		"is_error":        false,
		"num_turns":       float64(1),
		"session_id":      "s1",
	}
}

func messageKind(msg Message) string {
	switch msg.(type) {
	case *AssistantMessage:
		return "assistant"
	case *ResultMessage:
		return "result"
	default:
		return "other"
	}
}

// TestReceiveMessagesNotConnected keeps the error contract.
func TestReceiveMessagesNotConnected(t *testing.T) {
	client := NewClient(nil)
	if _, err := client.ReceiveMessages(context.Background()); err == nil {
		t.Error("Expected an error when not connected")
	}
}
