// Package transport provides the transport layer for CLI communication.
package transport

import (
	"context"
)

// Transport defines the interface for CLI communication.
type Transport interface {
	// Connect establishes the connection to the CLI.
	Connect(ctx context.Context) error

	// Write sends data to the CLI stdin.
	Write(data string) error

	// ReadMessages returns a channel of parsed JSON messages from CLI stdout.
	// The channel is closed when the CLI process terminates or an error occurs.
	ReadMessages() <-chan map[string]interface{}

	// Errors returns a channel for transport errors.
	// Errors are sent when the CLI process fails or JSON parsing fails.
	Errors() <-chan error

	// EndInput closes the input stream (stdin).
	EndInput() error

	// Close terminates the connection and cleans up resources.
	Close() error

	// IsReady returns true if the transport is ready for communication.
	IsReady() bool
}
