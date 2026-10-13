// Package testutil provides test utilities for the Claude Agent SDK.
package testutil

import (
	"context"
	"sync"
)

// MockTransport implements transport.Transport for testing.
type MockTransport struct {
	Messages     []map[string]interface{}
	WriteHistory []string
	Connected    bool
	Ready        bool

	// EndInputFunc, when set, is invoked by EndInput after the close is
	// recorded; tests can trigger shutdown side effects from it.
	EndInputFunc func() error

	messagesCh chan map[string]interface{}
	errorsCh   chan error
	mu         sync.Mutex
	msgIndex   int
	endInputs  int
}

// NewMockTransport creates a new MockTransport with predefined messages.
func NewMockTransport(messages []map[string]interface{}) *MockTransport {
	return &MockTransport{
		Messages:     messages,
		WriteHistory: make([]string, 0),
		messagesCh:   make(chan map[string]interface{}, 100),
		errorsCh:     make(chan error, 10),
	}
}

// Connect simulates connecting to the CLI.
func (m *MockTransport) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Connected = true
	m.Ready = true

	// Send predefined messages
	go func() {
		for _, msg := range m.Messages {
			m.messagesCh <- msg
		}
		close(m.messagesCh)
		close(m.errorsCh)
	}()

	return nil
}

// Write records data written to the transport.
func (m *MockTransport) Write(data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.WriteHistory = append(m.WriteHistory, data)
	return nil
}

// ReadMessages returns the channel of messages.
func (m *MockTransport) ReadMessages() <-chan map[string]interface{} {
	return m.messagesCh
}

// Errors returns the error channel.
func (m *MockTransport) Errors() <-chan error {
	return m.errorsCh
}

// EndInput simulates closing stdin, recording the close event so tests can
// assert when stdin closed (see internal.Query's run-end state machine).
func (m *MockTransport) EndInput() error {
	m.mu.Lock()
	m.endInputs++
	m.mu.Unlock()
	if m.EndInputFunc != nil {
		return m.EndInputFunc()
	}
	return nil
}

// EndInputCalls returns the number of recorded EndInput (stdin close) events.
func (m *MockTransport) EndInputCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.endInputs
}

// Close simulates closing the transport.
func (m *MockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Connected = false
	m.Ready = false
	return nil
}

// IsReady returns whether the transport is ready.
func (m *MockTransport) IsReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Ready
}

// SendError sends an error through the error channel.
func (m *MockTransport) SendError(err error) {
	m.errorsCh <- err
}
