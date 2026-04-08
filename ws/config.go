package ws

import (
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/zeromicro/go-zero/core/service"
)

// Mode represents the server mode.
type Mode string

const (
	ModeStateful  Mode = "stateful"  // Stateful mode with connection management
	ModeStateless Mode = "stateless" // Stateless mode with polling
)

// WsConf is the configuration for WebSocket server.
type WsConf struct {
	service.ServiceConf

	// Network configuration
	Addr string `json:",default=:8080"` // Listen address
	Path string `json:",default=/ws"`   // WebSocket path

	// Buffer configuration
	ReadBufferSize  int `json:",default=4096"` // Read buffer size
	WriteBufferSize int `json:",default=4096"` // Write buffer size

	// Connection limits
	MaxConnections int64 `json:",default=10000"` // Maximum concurrent connections

	// Heartbeat configuration
	PingInterval time.Duration `json:",default=30s"` // Ping interval
	PongWait     time.Duration `json:",default=60s"` // Time to wait for pong response

	// Message limits
	MaxMessageSize int64         `json:",default=65536"` // Maximum message size (64KB)
	WriteTimeout   time.Duration `json:",default=10s"`   // Write timeout

	// TLS configuration
	CertFile string `json:",optional"`
	KeyFile  string `json:",optional"`

	// Authentication configuration
	Auth protocol.AuthConfig `json:",optional"`

	// Stateless mode configuration
	Stateless StatelessConf `json:",optional"`
}

// StatelessConf is the configuration for stateless mode.
type StatelessConf struct {
	// Polling configuration
	PollInterval time.Duration `json:",default=100ms"` // Polling interval
	BatchSize    int           `json:",default=100"`   // Messages per batch

	// Acknowledgment
	AckTimeout time.Duration `json:",default=30s"` // Ack timeout
}

// SetUp initializes the configuration.
func (c *WsConf) SetUp() error {
	return c.ServiceConf.SetUp()
}
