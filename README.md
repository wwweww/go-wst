# go-wst

WebSocket and WebTransport library for go-zero framework with automatic fallback.

## Features

- **Multiple Protocols**: WebSocket and WebTransport support
- **Automatic Fallback**: WebTransport → WebSocket automatic downgrade
- **Dual Modes**: Stateful (connection management) and Stateless (polling)
- **High Performance**: Sharded hub for connection management
- **go-zero Integration**: Seamless integration with logx, stat, and other components

## Installation

```bash
go get github.com/wwweww/go-wst
```

## Quick Start

### WebSocket Server

```go
package main

import (
    "context"
    "log"

    "github.com/wwweww/go-wst/internal/protocol"
    "github.com/wwweww/go-wst/ws"
    "github.com/zeromicro/go-zero/core/conf"
)

type MyHandler struct{}

func (h *MyHandler) OnConnect(conn protocol.Connection) error {
    log.Printf("User connected: %s", conn.ID())
    return nil
}

func (h *MyHandler) OnMessage(conn protocol.Connection, msg []byte) error {
    log.Printf("Received: %s", string(msg))
    return conn.Send(context.Background(), msg)
}

func (h *MyHandler) OnDisconnect(conn protocol.Connection, err error) {
    log.Printf("User disconnected: %s", conn.ID())
}

func main() {
    var c ws.WsConf
    conf.MustLoad("config.yaml", &c)

    server := ws.MustNewServer(c, &MyHandler{})
    defer server.Stop()
    server.Start()
}
```

### Hybrid Server (WebSocket + WebTransport)

```go
package main

import (
    "context"
    "log"

    "github.com/wwweww/go-wst/hybrid"
    "github.com/wwweww/go-wst/internal/protocol"
    "github.com/wwweww/go-wst/wt"
    "github.com/wwweww/go-wst/ws"
    "github.com/zeromicro/go-zero/core/conf"
)

type MyHandler struct{}

func (h *MyHandler) OnConnect(conn protocol.Connection) error {
    log.Printf("[%s] User connected: %s", conn.Protocol(), conn.ID())
    return nil
}

func (h *MyHandler) OnMessage(conn protocol.Connection, msg []byte) error {
    return conn.Send(context.Background(), msg)
}

func (h *MyHandler) OnDisconnect(conn protocol.Connection, err error) {
    log.Printf("User disconnected: %s", conn.ID())
}

func main() {
    var c hybrid.HybridConf
    conf.MustLoad("config.yaml", &c)

    server := hybrid.MustNewServer(c, &MyHandler{})
    defer server.Stop()
    server.Start()
}
```

## Configuration

### WebSocket Configuration

```yaml
Name: my-server
Addr: :8080
Path: /ws

# Connection settings
MaxConnections: 10000
ReadBufferSize: 4096
WriteBufferSize: 4096

# Heartbeat
PingInterval: 30s
PongWait: 60s

# Message limits
MaxMessageSize: 65536
WriteTimeout: 10s

# Mode: stateful or stateless
Mode: stateful
```

### WebTransport Configuration

```yaml
Name: webtransport-server
Addr: :443
Path: /wt

# TLS (required for WebTransport)
CertFile: /path/to/cert.pem
KeyFile: /path/to/key.pem

# Connection settings
MaxConnections: 10000
MaxStreamsPerConn: 100
IdleTimeout: 120s
KeepAliveInterval: 30s
```

### Hybrid Configuration

```yaml
Name: hybrid-server
Protocol: auto  # auto, websocket, webtransport

# WebSocket config
Ws:
  Addr: :8080
  Path: /ws
  MaxConnections: 10000

# WebTransport config
Wt:
  Addr: :443
  Path: /wt
  CertFile: /path/to/cert.pem
  KeyFile: /path/to/key.pem

# Fallback settings
FallbackStrategy: auto  # none, auto, timeout, error
FallbackDelay: 1s
FallbackTimeout: 5s
MaxRetries: 3
```

## Architecture

```
go-wst/
├── ws/          # WebSocket implementation
├── wt/          # WebTransport implementation
├── hybrid/      # Hybrid mode with automatic fallback
├── stateful/    # Stateful connection management
├── stateless/   # Stateless polling mode
├── protocol/    # Public protocol interfaces
└── internal/    # Shared components
    ├── protocol/  # Core interfaces
    └── buffer/    # Buffer pool
```

## Core Concepts

### Stateful Mode

Best for real-time applications like chat, gaming, collaborative editing.

- Connection is maintained and managed
- Server can push messages to clients
- User can have multiple connections (multiple devices)
- Built-in heartbeat and connection tracking

### Stateless Mode

Best for notifications, updates, where clients poll for messages.

- No persistent connection state
- Clients fetch messages using timestamp/ID
- Messages stored in external store (Redis, DB)
- Better for load balancing

### Hybrid Mode

Best for modern applications supporting both WebSocket and WebTransport.

- Automatic protocol negotiation
- WebTransport as primary, WebSocket as fallback
- Client capability detection
- Seamless fallback on connection failure

## Protocol Negotiation

### Client Hello

```json
{
    "protocol": "auto",
    "version": "1.0",
    "supportsWt": true,
    "fallbackOnly": false,
    "clientId": "client-123",
    "userAgent": "Mozilla/5.0..."
}
```

### Server Hello

```json
{
    "protocol": "webtransport",
    "version": "1.0",
    "serverId": "hybrid-1234567890",
    "sessionId": "session-9876543210",
    "fallbackUrl": "ws://example.com/ws"
}
```

## Fallback Strategies

| Strategy | Description |
|----------|-------------|
| `none` | Never fallback, use selected protocol only |
| `auto` | Automatic fallback based on client support and history |
| `timeout` | Fallback if WebTransport connection takes too long |
| `error` | Fallback on any WebTransport error |

## API Reference

### Connection Interface

```go
type Connection interface {
    ID() string
    Protocol() Protocol
    RemoteAddr() net.Addr
    UserID() string
    SetUserID(userID string)
    
    Metadata() map[string]any
    SetMetadata(key string, v any)
    
    Send(ctx context.Context, msg []byte) error
    SendJSON(ctx context.Context, v any) error
    
    Close() error
    Done() <-chan struct{}
    Err() error
}
```

### Server Interface

```go
type Server interface {
    Broadcast(msg []byte) error
    BroadcastTo(userIDs []string, msg []byte) error
    SendTo(userID string, msg []byte) error
    
    GetConnection(userID string) (Connection, bool)
    GetConnections(userIDs []string) []Connection
    ConnectionCount() int64
    ForEachConnection(fn func(conn Connection) bool)
}
```

### Message Store Interface (Stateless Mode)

```go
type MessageStore interface {
    Fetch(ctx context.Context, userID string, sinceID int64, limit int) ([]Message, error)
    Ack(ctx context.Context, userID string, msgID int64) error
    Store(ctx context.Context, userID string, msg Message) error
}
```

## Running Tests

```bash
# Run all tests
cd go-wst && go test ./... -v

# Run WebSocket tests
go test ./ws/... -v

# Run stateless tests
go test ./stateless/... -v

# Run benchmarks
go test ./ws/... -bench=.
```

## Examples

See `example/` directory for complete examples:

- `chat/` - Chat room with stateful connections
- `notification/` - Notification system with stateless polling

## License

MIT
