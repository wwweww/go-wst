package ws

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wwweww/go-wst/internal/protocol"
)

// Connection wraps a WebSocket connection with additional metadata.
type Connection struct {
	id         string
	conn       *websocket.Conn
	protocol   protocol.Protocol
	userID     atomic.Value // string
	metadata   map[string]any
	metadataMu sync.RWMutex

	// Channels for message handling
	send     chan []byte
	done     chan struct{}
	closeErr atomic.Value // error

	// Configuration
	writeTimeout time.Duration
	once         sync.Once
}

// NewConnection creates a new WebSocket connection wrapper.
func NewConnection(id string, conn *websocket.Conn, writeTimeout time.Duration) *Connection {
	c := &Connection{
		id:           id,
		conn:         conn,
		protocol:     protocol.ProtocolWebSocket,
		metadata:     make(map[string]any),
		send:         make(chan []byte, 256),
		done:         make(chan struct{}),
		writeTimeout: writeTimeout,
	}
	return c
}

// ID returns the unique connection identifier.
func (c *Connection) ID() string {
	return c.id
}

// Protocol returns the connection protocol type.
func (c *Connection) Protocol() protocol.Protocol {
	return c.protocol
}

// RemoteAddr returns the remote address of the connection.
func (c *Connection) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// UserID returns the user identifier.
func (c *Connection) UserID() string {
	if v := c.userID.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// SetUserID sets the user identifier.
func (c *Connection) SetUserID(userID string) {
	c.userID.Store(userID)
}

// Metadata returns all metadata.
func (c *Connection) Metadata() map[string]any {
	c.metadataMu.RLock()
	defer c.metadataMu.RUnlock()

	result := make(map[string]any, len(c.metadata))
	for k, v := range c.metadata {
		result[k] = v
	}
	return result
}

// SetMetadata sets a metadata key-value pair.
func (c *Connection) SetMetadata(key string, v any) {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	c.metadata[key] = v
}

// Send sends a raw message to the connection.
func (c *Connection) Send(ctx context.Context, msg []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return websocket.ErrCloseSent
	case c.send <- msg:
		return nil
	}
}

// SendJSON sends a JSON-encoded message to the connection.
func (c *Connection) SendJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Send(ctx, data)
}

// Close closes the connection.
func (c *Connection) Close() error {
	var err error
	c.once.Do(func() {
		close(c.done)
		// Send close message
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err = c.conn.Close()
	})
	return err
}

// CloseWithError closes the connection with an error.
func (c *Connection) CloseWithError(err error) {
	c.closeErr.Store(err)
	c.Close()
}

// Done returns a channel that's closed when the connection is closed.
func (c *Connection) Done() <-chan struct{} {
	return c.done
}

// Err returns the error that caused the connection to close.
func (c *Connection) Err() error {
	if v := c.closeErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// SendChannel returns the send channel for the connection.
// This is used by the server to write messages.
func (c *Connection) SendChannel() chan<- []byte {
	return c.send
}

// WriteMessage writes a message to the WebSocket connection.
// This is called by the server's write pump.
func (c *Connection) WriteMessage(messageType int, data []byte) error {
	if c.writeTimeout > 0 {
		c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	return c.conn.WriteMessage(messageType, data)
}

// ReadMessage reads a message from the WebSocket connection.
func (c *Connection) ReadMessage() (int, []byte, error) {
	return c.conn.ReadMessage()
}

// SetReadDeadline sets the read deadline.
func (c *Connection) SetReadDeadline(t time.Time) {
	c.conn.SetReadDeadline(t)
}

// SetPongHandler sets the pong handler.
func (c *Connection) SetPongHandler(h func(string) error) {
	c.conn.SetPongHandler(h)
}

// Underlying returns the underlying WebSocket connection.
// Use with caution - direct manipulation may bypass safety checks.
func (c *Connection) Underlying() *websocket.Conn {
	return c.conn
}
