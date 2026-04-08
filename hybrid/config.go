package hybrid

import (
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/wwweww/go-wst/ws"
	"github.com/wwweww/go-wst/wt"
)

// Protocol represents the preferred protocol.
type Protocol string

const (
	// ProtocolAuto automatically selects the best protocol.
	ProtocolAuto Protocol = "auto"
	// ProtocolWebSocket forces WebSocket.
	ProtocolWebSocket Protocol = "websocket"
	// ProtocolWebTransport forces WebTransport.
	ProtocolWebTransport Protocol = "webtransport"
)

// FallbackStrategy defines when to fallback to WebSocket.
type FallbackStrategy string

const (
	// FallbackNone never falls back.
	FallbackNone FallbackStrategy = "none"
	// FallbackAuto automatically falls back when WebTransport fails.
	FallbackAuto FallbackStrategy = "auto"
	// FallbackTimeout falls back after timeout.
	FallbackTimeout FallbackStrategy = "timeout"
	// FallbackError falls back on any error.
	FallbackError FallbackStrategy = "error"
)

// AuthHybridConf hybrid authentication configuration
type AuthHybridConf struct {
	// Enabled enable authentication
	Enabled bool `json:",default=false"`

	// WebSocket authentication - read from Envoy header
	WsHeaderName string `json:",default=X-User-ID"`

	// WebTransport authentication - JWT validation
	WtTokenSource string `json:",default=query"` // query | header | cookie
	WtTokenName   string `json:",default=token"`
	WtJWTSecret   string `json:",optional"`
}

// HybridConf is the configuration for hybrid mode server.
type HybridConf struct {
	Name string `json:",optional"`

	// Protocol selection
	Protocol Protocol `json:",options=auto|websocket|webtransport,default=auto"`

	// WebSocket configuration
	Ws ws.WsConf `json:",optional"`

	// WebTransport configuration
	Wt wt.WtConf `json:",optional"`

	// Fallback configuration
	FallbackStrategy FallbackStrategy `json:",options=none|auto|timeout|error,default=auto"`
	FallbackDelay    time.Duration    `json:",default=1s"` // Delay before fallback
	FallbackTimeout  time.Duration    `json:",default=5s"` // Timeout for WebTransport connection
	MaxRetries       int              `json:",default=3"`  // Max retry attempts before fallback

	// Connection management
	MaxConnections int64 `json:",default=10000"`

	// Authentication - WebSocket uses Envoy header, WebTransport uses JWT
	// WebSocket: Envoy ext_authz validates and sets X-User-ID header
	// WebTransport: Direct connection, validates JWT itself
	Auth AuthHybridConf `json:",optional"`
}

// SetUp initializes the configuration.
func (c *HybridConf) SetUp() error {
	// Set default WebSocket config if not provided
	if c.Ws.Addr == "" {
		c.Ws.Addr = ":8080"
	}
	if c.Ws.Path == "" {
		c.Ws.Path = "/ws"
	}

	// Set default WebTransport config if not provided
	if c.Wt.Addr == "" {
		c.Wt.Addr = ":443"
	}
	if c.Wt.Path == "" {
		c.Wt.Path = "/wt"
	}

	return nil
}

// ClientHello is sent by the client to negotiate protocol.
type ClientHello struct {
	Protocol     string `json:"protocol"`     // "websocket" or "webtransport"
	Version      string `json:"version"`      // Protocol version
	SupportsWT   bool   `json:"supportsWt"`   // Client supports WebTransport
	FallbackOnly bool   `json:"fallbackOnly"` // Only use as fallback
	ClientID     string `json:"clientId"`     // Client identifier
	UserAgent    string `json:"userAgent"`    // User agent string
}

// ServerHello is sent by the server to confirm protocol.
type ServerHello struct {
	Protocol    string `json:"protocol"`    // Selected protocol
	Version     string `json:"version"`     // Protocol version
	ServerID    string `json:"serverId"`    // Server identifier
	SessionID   string `json:"sessionId"`   // Session ID
	FallbackURL string `json:"fallbackUrl"` // WebSocket fallback URL
}

// ProtocolInfo contains information about a protocol.
type ProtocolInfo struct {
	Protocol  protocol.Protocol
	Available bool
	Addr      string
	Path      string
	Priority  int // Higher priority is preferred
}
