package protocol

import (
	"context"
	"net"
)

// Protocol represents the connection protocol type.
type Protocol string

const (
	ProtocolWebSocket    Protocol = "websocket"
	ProtocolWebTransport Protocol = "webtransport"
)

// Connection represents a long-lived connection.
// It provides basic information, data sending/receiving, and lifecycle management.
type Connection interface {
	// Basic information
	ID() string              // Unique connection identifier
	Protocol() Protocol      // Connection protocol type
	RemoteAddr() net.Addr    // Remote address
	UserID() string          // User identifier (may be empty if not authenticated)
	SetUserID(userID string) // Set user identifier after authentication

	// Metadata management
	Metadata() map[string]any      // Get all metadata
	SetMetadata(key string, v any) // Set metadata

	// Data transmission
	Send(ctx context.Context, msg []byte) error
	SendJSON(ctx context.Context, v any) error

	// Lifecycle
	Close() error
	Done() <-chan struct{} // Returns a channel that's closed when connection is closed
	Err() error            // Returns the error that caused the connection to close
}

// Server represents a long connection server.
// It extends service.Service from go-zero and provides broadcast/push capabilities.
type Server interface {
	// Broadcast sends a message to all connections.
	Broadcast(msg []byte) error

	// BroadcastTo sends a message to specified user connections.
	BroadcastTo(userIDs []string, msg []byte) error

	// SendTo sends a message to a specific user.
	SendTo(userID string, msg []byte) error

	// Connection queries
	GetConnection(userID string) (Connection, bool)
	GetConnections(userIDs []string) []Connection
	ConnectionCount() int64
	ForEachConnection(fn func(conn Connection) bool)

	// Server information
	Protocol() Protocol
}

// StatefulHandler handles stateful connection events.
// Implement this interface to process WebSocket/WebTransport messages with connection state.
type StatefulHandler interface {
	// OnConnect is called when a new connection is established.
	// Return error to reject the connection.
	OnConnect(conn Connection) error

	// OnMessage is called when a message is received.
	OnMessage(conn Connection, msg []byte) error

	// OnDisconnect is called when a connection is closed.
	OnDisconnect(conn Connection, err error)
}

// Message represents a message with sequence number for stateless mode.
type Message struct {
	ID        int64  `json:"id"`        // Message sequence number (monotonically increasing)
	Timestamp int64  `json:"timestamp"` // Unix timestamp in milliseconds
	Type      string `json:"type"`      // Message type
	Topic     string `json:"topic"`     // Message topic (optional)
	Data      []byte `json:"data"`      // Message content
}

// FetchRequest represents a request to fetch messages in stateless mode.
type FetchRequest struct {
	UserID  string `json:"userId"`
	SinceID int64  `json:"sinceId"` // Fetch messages after this ID
	Limit   int    `json:"limit"`   // Maximum number of messages to return
	Topic   string `json:"topic"`   // Filter by topic (optional)
}

// SendRequest represents a request to send a message in stateless mode.
type SendRequest struct {
	UserID string `json:"userId"`
	Topic  string `json:"topic"` // Message topic (optional)
	Data   []byte `json:"data"`
}

// MessageStore defines the interface for message storage in stateless mode.
// Users must implement this interface to provide their own storage backend.
type MessageStore interface {
	// Fetch retrieves messages for a user after the specified ID.
	Fetch(ctx context.Context, userID string, sinceID int64, limit int) ([]Message, error)

	// Ack acknowledges that messages have been processed by the client.
	Ack(ctx context.Context, userID string, msgID int64) error

	// Store stores a message for a user.
	Store(ctx context.Context, userID string, msg Message) error
}

// StatelessHandler handles stateless message operations.
// Implement this interface to process polling-based message retrieval.
type StatelessHandler interface {
	// Fetch retrieves messages for a user.
	Fetch(ctx context.Context, req FetchRequest) ([]Message, error)

	// Send sends a message to a user.
	Send(ctx context.Context, req SendRequest) error
}

// HybridHandler combines both stateful and stateless handling capabilities.
type HybridHandler interface {
	StatefulHandler
	StatelessHandler
}
