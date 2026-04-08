package hybrid

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/wwweww/go-wst/internal/protocol"
)

// mockConn implements protocol.Connection for testing
type mockConn struct {
	id       string
	userID   string
	metadata map[string]any
}

func (c *mockConn) ID() string                              { return c.id }
func (c *mockConn) Protocol() protocol.Protocol             { return protocol.ProtocolWebSocket }
func (c *mockConn) RemoteAddr() net.Addr                    { return nil }
func (c *mockConn) UserID() string                          { return c.userID }
func (c *mockConn) SetUserID(id string)                     { c.userID = id }
func (c *mockConn) Metadata() map[string]any                { return c.metadata }
func (c *mockConn) SetMetadata(key string, v any)           { c.metadata[key] = v }
func (c *mockConn) Send(_ context.Context, _ []byte) error  { return nil }
func (c *mockConn) SendJSON(_ context.Context, _ any) error { return nil }
func (c *mockConn) Close() error                            { return nil }
func (c *mockConn) Done() <-chan struct{}                   { return nil }
func (c *mockConn) Err() error                              { return nil }

func newMockConn() *mockConn {
	return &mockConn{id: "test-conn", metadata: make(map[string]any)}
}

// --- EnvoyHeaderAuthenticator ---

func TestEnvoyHeaderAuthenticator_ValidHeader(t *testing.T) {
	auth := NewEnvoyHeaderAuthenticator("X-User-ID")

	req, _ := http.NewRequest("GET", "/ws", nil)
	req.Header.Set("X-User-ID", "user-123")

	userID, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebSocket,
		HTTPRequest: req,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "user-123" {
		t.Fatalf("expected 'user-123', got '%s'", userID)
	}
}

func TestEnvoyHeaderAuthenticator_EmptyHeader(t *testing.T) {
	auth := NewEnvoyHeaderAuthenticator("X-User-ID")

	req, _ := http.NewRequest("GET", "/ws", nil)
	// don't set header

	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebSocket,
		HTTPRequest: req,
	})

	if err == nil {
		t.Fatal("expected error for empty header")
	}
}

func TestEnvoyHeaderAuthenticator_NilRequest(t *testing.T) {
	auth := NewEnvoyHeaderAuthenticator("X-User-ID")

	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebSocket,
		HTTPRequest: nil,
	})

	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// --- JWTAuthenticator ---

func TestJWTAuthenticator_MissingToken(t *testing.T) {
	auth := NewJWTAuthenticator("query", "token", "secret", 0)

	req, _ := http.NewRequest("GET", "/wt", nil)
	// no token in query

	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebTransport,
		HTTPRequest: req,
	})

	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestJWTAuthenticator_InvalidToken(t *testing.T) {
	auth := NewJWTAuthenticator("query", "token", "secret", 0)

	req, _ := http.NewRequest("GET", "/wt?token=invalid.jwt.token", nil)

	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebTransport,
		HTTPRequest: req,
	})

	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestJWTAuthenticator_HeaderBearer(t *testing.T) {
	auth := NewJWTAuthenticator("header", "Authorization", "secret", 0)

	req, _ := http.NewRequest("GET", "/wt", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")

	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebTransport,
		HTTPRequest: req,
	})

	if err == nil {
		t.Fatal("expected error for invalid bearer token")
	}
}

// --- HybridAuthenticator ---

func TestHybridAuthenticator_WebSocketEnvoyHeader(t *testing.T) {
	auth := NewHybridAuthenticator(
		WsAuthConfig{Source: "envoy-header", HeaderName: "X-User-ID"},
		WtAuthConfig{TokenSource: "query", TokenName: "token", JWTSecret: "secret"},
	)

	req, _ := http.NewRequest("GET", "/ws", nil)
	req.Header.Set("X-User-ID", "ws-user-456")

	userID, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebSocket,
		HTTPRequest: req,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "ws-user-456" {
		t.Fatalf("expected 'ws-user-456', got '%s'", userID)
	}
}

func TestHybridAuthenticator_WebTransportJWT(t *testing.T) {
	auth := NewHybridAuthenticator(
		WsAuthConfig{Source: "envoy-header", HeaderName: "X-User-ID"},
		WtAuthConfig{TokenSource: "query", TokenName: "token", JWTSecret: "secret"},
	)

	// Invalid token should fail
	req, _ := http.NewRequest("GET", "/wt?token=invalid", nil)
	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    protocol.ProtocolWebTransport,
		HTTPRequest: req,
	})

	if err == nil {
		t.Fatal("expected error for invalid JWT")
	}
}

func TestHybridAuthenticator_UnsupportedProtocol(t *testing.T) {
	auth := NewHybridAuthenticator(
		WsAuthConfig{Source: "envoy-header", HeaderName: "X-User-ID"},
		WtAuthConfig{TokenSource: "query", TokenName: "token", JWTSecret: "secret"},
	)

	req, _ := http.NewRequest("GET", "/", nil)
	_, err := auth.Authenticate(context.Background(), newMockConn(), &protocol.AuthRequest{
		Protocol:    "unknown",
		HTTPRequest: req,
	})

	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}
