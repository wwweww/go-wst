// Package protocol exposes public types for connection handling.
package protocol

import (
	"github.com/wwweww/go-wst/internal/protocol"
)

// Protocol represents the connection protocol type.
type Protocol = protocol.Protocol

const (
	ProtocolWebSocket    Protocol = protocol.ProtocolWebSocket
	ProtocolWebTransport Protocol = protocol.ProtocolWebTransport
)

// Connection represents a long-lived connection.
type Connection = protocol.Connection

// StatefulHandler handles stateful connection events.
type StatefulHandler = protocol.StatefulHandler

// StatelessHandler handles stateless message operations.
type StatelessHandler = protocol.StatelessHandler

// Message represents a message with sequence number for stateless mode.
type Message = protocol.Message

// FetchRequest represents a request to fetch messages.
type FetchRequest = protocol.FetchRequest

// SendRequest represents a request to send a message.
type SendRequest = protocol.SendRequest

// MessageStore defines the interface for message storage.
type MessageStore = protocol.MessageStore

// Server represents a long connection server.
type Server = protocol.Server
