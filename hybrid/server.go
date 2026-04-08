package hybrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/wwweww/go-wst/ws"
	"github.com/wwweww/go-wst/wt"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/core/stat"
)

var (
	// ErrProtocolNotSupported is returned when protocol is not supported.
	ErrProtocolNotSupported = errors.New("protocol not supported")
)

// Server is a hybrid server that supports both WebSocket and WebTransport.
type Server struct {
	conf          HybridConf
	handler       protocol.StatefulHandler
	authenticator protocol.Authenticator
	metrics       *stat.Metrics
	fallback      *FallbackManager

	// WebSocket server
	wsServer *ws.Server

	// WebTransport server
	wtServer *wt.Server

	// Connection management
	connections   map[string]protocol.Connection
	connectionsMu sync.RWMutex
	userConns     map[string]map[string]protocol.Connection
	userConnsMu   sync.RWMutex
	connCount     int64

	// Server state
	httpServer *http.Server
	done       chan struct{}
	once       sync.Once
	group      *service.ServiceGroup
}

// ServerOption is a function that configures the server.
type ServerOption func(*Server)

// WithMetrics sets the metrics collector.
func WithMetrics(metrics *stat.Metrics) ServerOption {
	return func(s *Server) {
		s.metrics = metrics
	}
}

// WithAuthenticator sets the authenticator.
func WithAuthenticator(authenticator protocol.Authenticator) ServerOption {
	return func(s *Server) {
		s.authenticator = authenticator
	}
}

// MustNewServer creates a new hybrid server and panics on error.
func MustNewServer(conf HybridConf, handler protocol.StatefulHandler, opts ...ServerOption) *Server {
	s, err := NewServer(conf, handler, opts...)
	if err != nil {
		log.Fatal(err)
	}
	return s
}

// NewServer creates a new hybrid server.
func NewServer(conf HybridConf, handler protocol.StatefulHandler, opts ...ServerOption) (*Server, error) {
	if err := conf.SetUp(); err != nil {
		return nil, err
	}

	s := &Server{
		conf:        conf,
		handler:     handler,
		connections: make(map[string]protocol.Connection),
		userConns:   make(map[string]map[string]protocol.Connection),
		done:        make(chan struct{}),
		group:       service.NewServiceGroup(),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.metrics == nil {
		name := conf.Name
		if name == "" {
			name = conf.Ws.Name
		}
		s.metrics = stat.NewMetrics(name)
	}

	// Initialize authenticator if not provided
	if s.authenticator == nil && conf.Auth.Enabled {
		s.authenticator = NewHybridAuthenticator(
			WsAuthConfig{
				Source:     "envoy-header",
				HeaderName: conf.Auth.WsHeaderName,
			},
			WtAuthConfig{
				TokenSource: conf.Auth.WtTokenSource,
				TokenName:   conf.Auth.WtTokenName,
				JWTSecret:   conf.Auth.WtJWTSecret,
			},
		)
	}

	// Create fallback manager
	s.fallback = NewFallbackManager(conf, nil, nil)

	// Create WebSocket server with authenticator
	wsHandler := &hybridWsHandler{server: s}
	var wsOpts []ws.ServerOption
	if s.authenticator != nil {
		wsOpts = append(wsOpts, ws.WithAuthenticator(s.authenticator))
	}
	if s.metrics != nil {
		wsOpts = append(wsOpts, ws.WithMetrics(s.metrics))
	}
	s.wsServer = ws.MustNewServer(conf.Ws, wsHandler, wsOpts...)
	s.fallback.wsServer = s.wsServer

	// Create WebTransport server if configured
	if conf.Protocol != ProtocolWebSocket && (conf.Wt.CertFile != "" || conf.Wt.CertEnvVar != "") {
		wtHandler := &hybridWtHandler{server: s}
		var wtOpts []wt.ServerOption
		if s.authenticator != nil {
			wtOpts = append(wtOpts, wt.WithAuthenticator(s.authenticator))
		}
		if s.metrics != nil {
			wtOpts = append(wtOpts, wt.WithMetrics(s.metrics))
		}
		wtServer, err := wt.NewServer(conf.Wt, wtHandler, wtOpts...)
		if err != nil {
			log.Printf("WARN: failed to create WebTransport server: %v", err)
		} else {
			s.wtServer = wtServer
			s.fallback.wtServer = wtServer
		}
	}

	return s, nil
}

// Start starts the hybrid server.
func (s *Server) Start() {
	// Start WebSocket server
	s.group.Add(s.wsServer)

	// Start WebTransport server if available
	if s.wtServer != nil {
		s.group.Add(s.wtServer)
	}

	s.group.Start()
}

// Stop stops the hybrid server.
func (s *Server) Stop() {
	s.once.Do(func() {
		close(s.done)
		if s.httpServer != nil {
			s.httpServer.Close()
		}
		s.group.Stop()
	})
}

// Protocol returns the server protocol.
func (s *Server) Protocol() protocol.Protocol {
	return protocol.ProtocolWebSocket // Default for hybrid
}

// GetProtocolInfo returns available protocols.
func (s *Server) GetProtocolInfo() []ProtocolInfo {
	var infos []ProtocolInfo

	infos = append(infos, ProtocolInfo{
		Protocol:  protocol.ProtocolWebSocket,
		Available: true,
		Addr:      s.conf.Ws.Addr,
		Path:      s.conf.Ws.Path,
		Priority:  1,
	})

	if s.wtServer != nil {
		infos = append(infos, ProtocolInfo{
			Protocol:  protocol.ProtocolWebTransport,
			Available: true,
			Addr:      s.conf.Wt.Addr,
			Path:      s.conf.Wt.Path,
			Priority:  2, // Higher priority
		})
	}

	return infos
}

// handleNegotiate handles protocol negotiation.
func (s *Server) handleNegotiate(w http.ResponseWriter, r *http.Request) {
	// Parse client hello
	var hello ClientHello
	if err := json.NewDecoder(r.Body).Decode(&hello); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Select protocol
	selected := s.fallback.SelectProtocol(hello)

	// Build response
	resp := ServerHello{
		Protocol:    string(selected),
		Version:     "1.0",
		ServerID:    fmt.Sprintf("hybrid-%d", time.Now().Unix()),
		SessionID:   generateSessionID(),
		FallbackURL: fmt.Sprintf("ws://%s%s", r.Host, s.conf.Ws.Path),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// RegisterConnection registers a connection.
func (s *Server) RegisterConnection(conn protocol.Connection) {
	s.connectionsMu.Lock()
	s.connections[conn.ID()] = conn
	s.connectionsMu.Unlock()
	s.connCount++
}

// UnregisterConnection unregisters a connection.
func (s *Server) UnregisterConnection(conn protocol.Connection) {
	s.connectionsMu.Lock()
	delete(s.connections, conn.ID())
	s.connectionsMu.Unlock()

	if userID := conn.UserID(); userID != "" {
		s.userConnsMu.Lock()
		if conns, ok := s.userConns[userID]; ok {
			delete(conns, conn.ID())
			if len(conns) == 0 {
				delete(s.userConns, userID)
			}
		}
		s.userConnsMu.Unlock()
	}

	s.connCount--
}

// BindUser binds a connection to a user.
func (s *Server) BindUser(conn protocol.Connection, userID string) {
	conn.SetUserID(userID)

	s.userConnsMu.Lock()
	if s.userConns[userID] == nil {
		s.userConns[userID] = make(map[string]protocol.Connection)
	}
	s.userConns[userID][conn.ID()] = conn
	s.userConnsMu.Unlock()
}

// Broadcast sends a message to all connections.
func (s *Server) Broadcast(msg []byte) error {
	s.connectionsMu.RLock()
	defer s.connectionsMu.RUnlock()

	var lastErr error
	for _, conn := range s.connections {
		if err := conn.Send(context.Background(), msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// BroadcastTo sends a message to specified user connections.
func (s *Server) BroadcastTo(userIDs []string, msg []byte) error {
	s.userConnsMu.RLock()
	defer s.userConnsMu.RUnlock()

	var lastErr error
	for _, uid := range userIDs {
		if conns, ok := s.userConns[uid]; ok {
			for _, conn := range conns {
				if err := conn.Send(context.Background(), msg); err != nil {
					lastErr = err
				}
			}
		}
	}
	return lastErr
}

// SendTo sends a message to a specific user.
func (s *Server) SendTo(userID string, msg []byte) error {
	s.userConnsMu.RLock()
	conns, ok := s.userConns[userID]
	if !ok {
		s.userConnsMu.RUnlock()
		return errors.New("user not found")
	}

	connList := make([]protocol.Connection, 0, len(conns))
	for _, conn := range conns {
		connList = append(connList, conn)
	}
	s.userConnsMu.RUnlock()

	var lastErr error
	for _, conn := range connList {
		if err := conn.Send(context.Background(), msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// GetConnection returns a connection by user ID.
func (s *Server) GetConnection(userID string) (protocol.Connection, bool) {
	s.userConnsMu.RLock()
	defer s.userConnsMu.RUnlock()

	conns, ok := s.userConns[userID]
	if !ok || len(conns) == 0 {
		return nil, false
	}

	for _, conn := range conns {
		return conn, true
	}
	return nil, false
}

// GetConnections returns connections for specified users.
func (s *Server) GetConnections(userIDs []string) []protocol.Connection {
	s.userConnsMu.RLock()
	defer s.userConnsMu.RUnlock()

	var result []protocol.Connection
	for _, uid := range userIDs {
		if conns, ok := s.userConns[uid]; ok {
			for _, conn := range conns {
				result = append(result, conn)
			}
		}
	}
	return result
}

// ConnectionCount returns the current connection count.
func (s *Server) ConnectionCount() int64 {
	return s.connCount
}

// ForEachConnection iterates over all connections.
func (s *Server) ForEachConnection(fn func(conn protocol.Connection) bool) {
	s.connectionsMu.RLock()
	defer s.connectionsMu.RUnlock()

	for _, conn := range s.connections {
		if !fn(conn) {
			return
		}
	}
}

// GetStats returns fallback statistics.
func (s *Server) GetStats() FallbackStats {
	return s.fallback.GetStats()
}

// generateSessionID generates a unique session ID.
func generateSessionID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// hybridWsHandler wraps WebSocket connections for the hybrid server.
type hybridWsHandler struct {
	server *Server
}

func (h *hybridWsHandler) OnConnect(conn protocol.Connection) error {
	h.server.RegisterConnection(conn)
	h.server.fallback.RecordConnection(protocol.ProtocolWebSocket)
	return h.server.handler.OnConnect(conn)
}

func (h *hybridWsHandler) OnMessage(conn protocol.Connection, msg []byte) error {
	return h.server.handler.OnMessage(conn, msg)
}

func (h *hybridWsHandler) OnDisconnect(conn protocol.Connection, err error) {
	h.server.UnregisterConnection(conn)
	h.server.handler.OnDisconnect(conn, err)
}

// hybridWtHandler wraps WebTransport connections for the hybrid server.
type hybridWtHandler struct {
	server *Server
}

func (h *hybridWtHandler) OnConnect(conn protocol.Connection) error {
	h.server.RegisterConnection(conn)
	h.server.fallback.RecordConnection(protocol.ProtocolWebTransport)
	return h.server.handler.OnConnect(conn)
}

func (h *hybridWtHandler) OnMessage(conn protocol.Connection, msg []byte) error {
	return h.server.handler.OnMessage(conn, msg)
}

func (h *hybridWtHandler) OnDisconnect(conn protocol.Connection, err error) {
	h.server.UnregisterConnection(conn)
	h.server.handler.OnDisconnect(conn, err)
}
