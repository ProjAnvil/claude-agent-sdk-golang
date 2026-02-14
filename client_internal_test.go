package claude

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/claude-agent-sdk-golang/internal/transport"
)

// MockTransport implements transport.Transport for testing.
type MockTransport struct {
	ConnectFunc      func(ctx context.Context) error
	WriteFunc        func(data string) error
	ReadMessagesFunc func() <-chan map[string]interface{}
	ErrorsFunc       func() <-chan error
	EndInputFunc     func() error
	CloseFunc        func() error
	IsReadyFunc      func() bool

	// Channels
	readCh chan map[string]interface{}
	errCh  chan error

	closeOnce sync.Once
}

func (m *MockTransport) Connect(ctx context.Context) error {
	if m.ConnectFunc != nil {
		return m.ConnectFunc(ctx)
	}
	return nil
}

func (m *MockTransport) Write(data string) error {
	if m.WriteFunc != nil {
		return m.WriteFunc(data)
	}
	return nil
}

func (m *MockTransport) ReadMessages() <-chan map[string]interface{} {
	if m.ReadMessagesFunc != nil {
		return m.ReadMessagesFunc()
	}
	return m.readCh
}

func (m *MockTransport) Errors() <-chan error {
	if m.ErrorsFunc != nil {
		return m.ErrorsFunc()
	}
	return m.errCh
}

func (m *MockTransport) EndInput() error {
	if m.EndInputFunc != nil {
		return m.EndInputFunc()
	}
	return nil
}

func (m *MockTransport) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	m.closeOnce.Do(func() {
		close(m.readCh)
		close(m.errCh)
	})
	return nil
}

func (m *MockTransport) IsReady() bool {
	if m.IsReadyFunc != nil {
		return m.IsReadyFunc()
	}
	return true
}

// newMockTransport creates a MockTransport with initialized channels.
func newMockTransport() *MockTransport {
	return &MockTransport{
		readCh: make(chan map[string]interface{}, 100),
		errCh:  make(chan error, 100),
	}
}

// Helper to handle initialization handshake automatically
func handleInitialization(t *MockTransport, writeCapture *[]string) {
	// Simple auto-responder for initialization
	originalWrite := t.WriteFunc
	t.WriteFunc = func(data string) error {
		if writeCapture != nil {
			*writeCapture = append(*writeCapture, data)
		}

		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return err
		}

		if msg["type"] == "control_request" {
			reqID, _ := msg["request_id"].(string)
			req, _ := msg["request"].(map[string]interface{})
			subtype, _ := req["subtype"].(string)

			if subtype == "initialize" {
				// Send success response
				resp := map[string]interface{}{
					"type": "control_response",
					"response": map[string]interface{}{
						"subtype":    "success",
						"request_id": reqID,
						"response": map[string]interface{}{
							"version": "0.1.0",
						},
					},
				}
				t.readCh <- resp
			} else if subtype == "interrupt" {
				// Send interrupt success response
				resp := map[string]interface{}{
					"type": "control_response",
					"response": map[string]interface{}{
						"subtype":    "success",
						"request_id": reqID,
					},
				}
				t.readCh <- resp
			}
		}

		if originalWrite != nil {
			return originalWrite(data)
		}
		return nil
	}
}

func TestQuerySinglePrompt(t *testing.T) {
	mockT := newMockTransport()
	var writtenData []string

	handleInitialization(mockT, &writtenData)

	client := NewClient(nil)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)

		mockT.readCh <- map[string]interface{}{
			"type": "assistant",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "4"},
				},
				"model": "claude-opus-4-1-20250805",
			},
		}
	}()

	msgChan, err := client.Query(ctx, "What is 2+2?")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	var messages []Message
	select {
	case msg := <-msgChan:
		messages = append(messages, msg)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for message")
	}

	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	}

	assistantMsg, ok := messages[0].(*AssistantMessage)
	if !ok {
		t.Errorf("Expected AssistantMessage, got %T", messages[0])
	} else {
		if len(assistantMsg.Content) == 0 {
			t.Error("Expected content")
		} else {
			textBlock, ok := assistantMsg.Content[0].(*TextBlock)
			if !ok {
				t.Errorf("Expected TextBlock, got %T", assistantMsg.Content[0])
			} else if textBlock.Text != "4" {
				t.Errorf("Expected '4', got '%s'", textBlock.Text)
			}
		}
	}
}

func TestClientQueryWithOptions(t *testing.T) {
	mockT := newMockTransport()

	var capturedOpts *transport.TransportOptions

	handleInitialization(mockT, nil)

	opts := &ClaudeAgentOptions{
		AllowedTools:   []string{"Read", "Write"},
		SystemPrompt:   "You are helpful",
		PermissionMode: PermissionModeAcceptEdits,
		MaxTurns:       5,
	}

	client := NewClient(opts)
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}

	if capturedOpts.SystemPrompt != "You are helpful" {
		t.Errorf("Expected SystemPrompt 'You are helpful', got '%s'", capturedOpts.SystemPrompt)
	}

	if len(capturedOpts.AllowedTools) != 2 {
		t.Errorf("Expected 2 AllowedTools, got %d", len(capturedOpts.AllowedTools))
	} else {
		if capturedOpts.AllowedTools[0] != "Read" || capturedOpts.AllowedTools[1] != "Write" {
			t.Errorf("Expected [Read, Write], got %v", capturedOpts.AllowedTools)
		}
	}

	if capturedOpts.PermissionMode != string(PermissionModeAcceptEdits) {
		t.Errorf("Expected PermissionMode '%s', got '%s'", PermissionModeAcceptEdits, capturedOpts.PermissionMode)
	}

	if capturedOpts.MaxTurns != 5 {
		t.Errorf("Expected MaxTurns 5, got %d", capturedOpts.MaxTurns)
	}
}

func TestClientQueryWithCWD(t *testing.T) {
	mockT := newMockTransport()

	var capturedOpts *transport.TransportOptions

	client := NewClient(&ClaudeAgentOptions{CWD: "/custom/path"})
	client.transportFactory = func(prompt interface{}, opts *transport.TransportOptions) (transport.Transport, error) {
		capturedOpts = opts
		return mockT, nil
	}

	mockT.ConnectFunc = func(ctx context.Context) error { return nil }

	handleInitialization(mockT, nil)

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if capturedOpts == nil {
		t.Fatal("Transport options not captured")
	}

	if capturedOpts.CWD != "/custom/path" {
		t.Errorf("Expected CWD '/custom/path', got '%s'", capturedOpts.CWD)
	}
}
