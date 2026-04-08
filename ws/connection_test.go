package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/wwweww/go-wst/internal/protocol"
)

var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// testConnection creates a real WebSocket connection pair for testing.
// Returns the wrapped Connection, the raw websocket.Conn (client side), and the test server.
func testConnection(t *testing.T) (*Connection, *websocket.Conn, *httptest.Server) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Keep connection alive
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect as client
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.NoError(t, err)

	// Create Connection wrapper
	conn := NewConnection("test-conn-id", wsConn, 10*time.Second)

	return conn, wsConn, server
}

func TestConnection_BasicMethods(t *testing.T) {
	conn, wsConn, server := testConnection(t)
	defer server.Close()
	defer wsConn.Close()
	defer conn.Close()

	// Test ID
	assert.Equal(t, "test-conn-id", conn.ID())

	// Test Protocol
	assert.Equal(t, protocol.ProtocolWebSocket, conn.Protocol())

	// Test RemoteAddr
	assert.NotNil(t, conn.RemoteAddr())

	// Test UserID
	assert.Equal(t, "", conn.UserID())
	conn.SetUserID("user-123")
	assert.Equal(t, "user-123", conn.UserID())
}

func TestConnection_Metadata(t *testing.T) {
	conn, wsConn, server := testConnection(t)
	defer server.Close()
	defer wsConn.Close()
	defer conn.Close()

	// Initially empty
	assert.Empty(t, conn.Metadata())

	// Set metadata
	conn.SetMetadata("key1", "value1")
	conn.SetMetadata("key2", 123)
	conn.SetMetadata("key3", true)

	metadata := conn.Metadata()
	assert.Equal(t, "value1", metadata["key1"])
	assert.Equal(t, 123, metadata["key2"])
	assert.Equal(t, true, metadata["key3"])

	// Update metadata
	conn.SetMetadata("key1", "updated")
	assert.Equal(t, "updated", conn.Metadata()["key1"])
}

func TestConnection_Done(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()

	// Done channel should not be closed initially
	select {
	case <-conn.Done():
		t.Error("Done channel should not be closed initially")
	default:
		// Expected
	}

	// After close
	conn.Close()

	select {
	case <-conn.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Done channel should be closed")
	}
}

func TestConnection_Close(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()

	// First close should work
	err := conn.Close()
	assert.NoError(t, err)

	// Verify done is closed
	select {
	case <-conn.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Done channel should be closed")
	}

	// Second close should be no-op (once ensures single execution)
	err = conn.Close()
	assert.NoError(t, err)
}

func TestConnection_CloseWithError(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()

	testErr := assert.AnError
	conn.CloseWithError(testErr)

	// Check error is stored
	assert.Equal(t, testErr, conn.Err())

	// Verify done is closed
	select {
	case <-conn.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Done channel should be closed")
	}
}

func TestConnection_SendAfterDone(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()

	// Fill the send channel to capacity so Send must wait
	for i := 0; i < 256; i++ {
		conn.send <- []byte("fill")
	}

	// Close connection
	conn.Close()

	// Wait for done channel to be closed
	select {
	case <-conn.Done():
		// Expected - done is closed
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Done channel should be closed")
	}

	// Send should fail after done is closed because channel is full
	ctx := context.Background()
	err := conn.Send(ctx, []byte("hello"))
	// Should return websocket.ErrCloseSent because done is closed and channel is full
	assert.Error(t, err)
	assert.Equal(t, websocket.ErrCloseSent, err)
}

func TestConnection_SendWithContextCancel(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()
	defer conn.Close()

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	// Send might return context canceled error OR succeed if the channel has capacity
	// This depends on Go's select statement behavior
	err := conn.Send(ctx, []byte("hello"))
	// Either the context is canceled or the message is sent successfully
	if err != nil {
		assert.Equal(t, context.Canceled, err)
	}
	// If no error, the message was sent to the channel (also valid behavior)
}

func TestConnection_ConcurrentMetadata(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()
	defer conn.Close()

	// Test concurrent metadata access
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			conn.SetMetadata("key", i)
		}(i)
		go func() {
			defer wg.Done()
			_ = conn.Metadata()
		}()
	}
	wg.Wait()
}

func TestConnection_ConcurrentUserID(t *testing.T) {
	conn, _, server := testConnection(t)
	defer server.Close()
	defer conn.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			conn.SetUserID(string(rune(i)))
		}(i)
		go func() {
			defer wg.Done()
			_ = conn.UserID()
		}()
	}
	wg.Wait()
}

// Benchmark tests
func BenchmarkConnection_Send(b *testing.B) {
	// Create a simple connection for benchmarking
	conn := &Connection{
		id:       "bench-conn",
		protocol: protocol.ProtocolWebSocket,
		metadata: make(map[string]any),
		send:     make(chan []byte, 256),
		done:     make(chan struct{}),
	}

	// Drain send channel in background
	go func() {
		for range conn.send {
		}
	}()

	ctx := context.Background()
	msg := []byte("benchmark message")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.Send(ctx, msg)
	}
}

func BenchmarkConnection_Metadata(b *testing.B) {
	conn := &Connection{
		id:       "bench-conn",
		protocol: protocol.ProtocolWebSocket,
		metadata: make(map[string]any),
		send:     make(chan []byte, 256),
		done:     make(chan struct{}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.SetMetadata("key", i)
		_ = conn.Metadata()
	}
}
