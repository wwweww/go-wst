package wt

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/webtransport-go"
	"github.com/wwweww/go-wst/internal/protocol"
)

// Error definitions
var (
	ErrConnectionClosed = errors.New("connection closed")
	ErrStreamClosed     = errors.New("stream closed")
	ErrStreamNotFound   = errors.New("stream not found")
)

// ConnectionError represents a connection error.
type ConnectionError struct {
	msg string
}

// NewConnectionError creates a new ConnectionError.
func NewConnectionError(msg string) *ConnectionError {
	return &ConnectionError{msg: msg}
}

// Error implements the error interface.
func (e *ConnectionError) Error() string {
	return e.msg
}

// Is implements errors.Is interface for comparison.
func (e *ConnectionError) Is(target error) bool {
	t, ok := target.(*ConnectionError)
	if !ok {
		return false
	}
	return e.msg == t.msg
}

// Connection wraps a WebTransport session with additional metadata.
type Connection struct {
	id         string
	session    *webtransport.Session
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

	// The client's bidirectional stream, used to write responses back.
	// Set by handleStream when the first stream is accepted.
	responseStream *webtransport.Stream
	streamMu       sync.Mutex
	streamReady    chan struct{} // closed when responseStream is first set
}

// NewConnection creates a new WebTransport connection wrapper.
func NewConnection(id string, session *webtransport.Session, writeTimeout time.Duration) *Connection {
	c := &Connection{
		id:           id,
		session:      session,
		protocol:     protocol.ProtocolWebTransport,
		metadata:     make(map[string]any),
		send:         make(chan []byte, 256),
		done:         make(chan struct{}),
		writeTimeout: writeTimeout,
		streamReady:  make(chan struct{}),
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
	return c.session.RemoteAddr()
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

// Send sends a raw message to the connection via a unidirectional stream.
func (c *Connection) Send(ctx context.Context, msg []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrConnectionClosed
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

// Close closes the connection and all associated streams.
func (c *Connection) Close() error {
	var err error
	c.once.Do(func() {
		close(c.done)
		// Close the session
		err = c.session.CloseWithError(0, "")
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

// SendChannel returns the send channel for internal use.
func (c *Connection) SendChannel() chan<- []byte {
	return c.send
}

// ReceiveChannel returns the underlying send channel for reading (internal use only).
func (c *Connection) ReceiveChannel() <-chan []byte {
	return c.send
}

// Session returns the underlying WebTransport session.
func (c *Connection) Session() *webtransport.Session {
	return c.session
}

// AcceptStream accepts a new bidirectional stream on this connection.
func (c *Connection) AcceptStream(ctx context.Context) (*webtransport.Stream, error) {
	return c.session.AcceptStream(ctx)
}

// SetResponseStream sets the bidirectional stream used to write responses.
func (c *Connection) SetResponseStream(stream *webtransport.Stream) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if c.responseStream == nil {
		c.responseStream = stream
		close(c.streamReady) // signal writePump that the stream is ready
	}
}

// ResponseStream returns the stream for writing responses.
func (c *Connection) ResponseStream() *webtransport.Stream {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	return c.responseStream
}

// StreamReady returns a channel that is closed when the response stream is first set.
func (c *Connection) StreamReady() <-chan struct{} {
	return c.streamReady
}

// OpenUniStream opens a new unidirectional send stream.
func (c *Connection) OpenUniStream(ctx context.Context) (*webtransport.SendStream, error) {
	return c.session.OpenUniStreamSync(ctx)
}
