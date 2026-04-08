package ws

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wwweww/go-wst/internal/buffer"
	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stat"
	"github.com/zeromicro/go-zero/core/threading"
)

var (
	// ErrConnectionLimit is returned when connection limit is reached.
	ErrConnectionLimit = errors.New("connection limit reached")
	// ErrUserNotFound is returned when user is not found.
	ErrUserNotFound = errors.New("user not found")
)

// Server is a WebSocket server that implements protocol.Server.
type Server struct {
	conf          WsConf
	handler       protocol.StatefulHandler
	authenticator protocol.Authenticator
	upgrader      websocket.Upgrader
	metrics       *stat.Metrics

	// Connection management
	connections   map[string]*Connection // connID -> Connection
	connectionsMu sync.RWMutex
	userConns     map[string]map[string]*Connection // userID -> connID -> Connection
	userConnsMu   sync.RWMutex
	connCount     atomic.Int64

	// Server state
	server  *http.Server
	done    chan struct{}
	once    sync.Once
	bufPool *buffer.Pool
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

// MustNewServer creates a new WebSocket server and panics on error.
func MustNewServer(conf WsConf, handler protocol.StatefulHandler, opts ...ServerOption) *Server {
	s, err := NewServer(conf, handler, opts...)
	if err != nil {
		log.Fatal(err)
	}
	return s
}

// NewServer creates a new WebSocket server.
func NewServer(conf WsConf, handler protocol.StatefulHandler, opts ...ServerOption) (*Server, error) {
	if err := conf.SetUp(); err != nil {
		return nil, err
	}

	s := &Server{
		conf:        conf,
		handler:     handler,
		connections: make(map[string]*Connection),
		userConns:   make(map[string]map[string]*Connection),
		done:        make(chan struct{}),
		bufPool:     buffer.NewPool(conf.ReadBufferSize),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  conf.ReadBufferSize,
			WriteBufferSize: conf.WriteBufferSize,
			// Check origin in production
			CheckOrigin: func(r *http.Request) bool {
				return true // TODO: Implement proper origin check
			},
		},
	}

	for _, opt := range opts {
		opt(s)
	}

	// Default to NoOp authenticator if none provided
	if s.authenticator == nil {
		s.authenticator = &protocol.NoOpAuthenticator{}
	}

	if s.metrics == nil {
		name := conf.Name
		if name == "" {
			name = "websocket-server"
		}
		s.metrics = stat.NewMetrics(name)
	}

	return s, nil
}

// Start starts the WebSocket server.
func (s *Server) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc(s.conf.Path, s.HandleWebSocket)

	s.server = &http.Server{
		Addr:    s.conf.Addr,
		Handler: mux,
	}

	var err error
	if s.conf.CertFile != "" && s.conf.KeyFile != "" {
		cfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		s.server.TLSConfig = cfg
		err = s.server.ListenAndServeTLS(s.conf.CertFile, s.conf.KeyFile)
	} else {
		err = s.server.ListenAndServe()
	}

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logx.Error("server error:", err)
	}
}

// Stop stops the WebSocket server.
func (s *Server) Stop() {
	s.once.Do(func() {
		close(s.done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	})
}

// Protocol returns the server protocol.
func (s *Server) Protocol() protocol.Protocol {
	return protocol.ProtocolWebSocket
}

// HandleWebSocket handles WebSocket upgrade and connection.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Check connection limit
	if s.conf.MaxConnections > 0 && s.connCount.Load() >= s.conf.MaxConnections {
		http.Error(w, ErrConnectionLimit.Error(), http.StatusServiceUnavailable)
		return
	}

	// Upgrade HTTP to WebSocket
	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logx.Errorf("upgrade error: %v", err)
		return
	}

	connID := generateConnID()
	conn := NewConnection(connID, wsConn, s.conf.WriteTimeout)
	s.connCount.Add(1)

	// Authentication
	if s.conf.Auth.Enabled {
		authReq := &protocol.AuthRequest{
			Protocol:    protocol.ProtocolWebSocket,
			HTTPRequest: r,
		}

		userID, err := s.authenticator.Authenticate(context.Background(), conn, authReq)
		if err != nil {
			logx.Errorf("authentication failed: %v", err)
			// Send close frame with policy violation status
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
			conn.Close()
			s.connCount.Add(-1)
			return
		}

		// Bind user to connection
		if userID != "" {
			conn.SetUserID(userID)
			s.BindUser(conn, userID)
		}
	}

	// Register connection
	s.registerConnection(conn)
	defer s.unregisterConnection(conn)

	// Notify handler of new connection
	if err := s.handler.OnConnect(conn); err != nil {
		logx.Errorf("on connect error: %v", err)
		conn.Close()
		return
	}

	// Start read and write pumps
	wg := threading.NewRoutineGroup()
	wg.Run(func() {
		s.writePump(conn)
	})
	wg.Run(func() {
		s.readPump(conn)
	})
	wg.Wait()
}

// readPump pumps messages from the WebSocket connection to the handler.
func (s *Server) readPump(conn *Connection) {
	defer conn.Close()

	// Set read deadline and pong handler
	conn.SetReadDeadline(time.Now().Add(s.conf.PongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(s.conf.PongWait))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logx.Errorf("read error: %v", err)
			}
			s.handler.OnDisconnect(conn, err)
			return
		}

		// Update metrics
		s.metrics.Add(stat.Task{Duration: 0})

		// Handle message
		if err := s.handler.OnMessage(conn, message); err != nil {
			logx.Errorf("message handler error: %v", err)
		}
	}
}

// writePump pumps messages from the send channel to the WebSocket connection.
func (s *Server) writePump(conn *Connection) {
	ticker := time.NewTicker(s.conf.PingInterval)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()

	for {
		select {
		case <-s.done:
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		case <-conn.Done():
			return
		case message, ok := <-conn.send:
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}

			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				logx.Errorf("write error: %v", err)
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// registerConnection registers a connection.
func (s *Server) registerConnection(conn *Connection) {
	s.connectionsMu.Lock()
	s.connections[conn.ID()] = conn
	s.connectionsMu.Unlock()
}

// unregisterConnection unregisters a connection.
func (s *Server) unregisterConnection(conn *Connection) {
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

	s.connCount.Add(-1)
}

// BindUser binds a connection to a user.
func (s *Server) BindUser(conn *Connection, userID string) {
	conn.SetUserID(userID)

	s.userConnsMu.Lock()
	if s.userConns[userID] == nil {
		s.userConns[userID] = make(map[string]*Connection)
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
		return ErrUserNotFound
	}

	// Copy connections to avoid lock during send
	connList := make([]*Connection, 0, len(conns))
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

	// Return first connection
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
	return s.connCount.Load()
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

// generateConnID generates a unique connection ID.
func generateConnID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
