package wt

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/wwweww/go-wst/internal/buffer"
	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stat"
)

var (
	// ErrConnectionLimit is returned when connection limit is reached.
	ErrConnectionLimit = errors.New("connection limit reached")
	// ErrUserNotFound is returned when user is not found.
	ErrUserNotFound = errors.New("user not found")
	// ErrTLSCertRequired is returned when TLS certificate is not provided.
	ErrTLSCertRequired = errors.New("WebTransport requires TLS certificate and key")
)

// Server is a WebTransport server that implements protocol.Server.
type Server struct {
	conf          WtConf
	handler       protocol.StatefulHandler
	authenticator protocol.Authenticator
	metrics       *stat.Metrics
	tlsConfig     *tls.Config

	// Connection management
	connections   map[string]*Connection
	connectionsMu sync.RWMutex
	userConns     map[string]map[string]*Connection
	userConnsMu   sync.RWMutex
	connCount     atomic.Int64

	// Server state
	wtServer *webtransport.Server
	done     chan struct{}
	once     sync.Once
	bufPool  *buffer.Pool
	wg       sync.WaitGroup
}

// ServerOption is a function that configures the server.
type ServerOption func(*Server)

// WithMetrics sets the metrics collector.
func WithMetrics(metrics *stat.Metrics) ServerOption {
	return func(s *Server) {
		s.metrics = metrics
	}
}

// WithTLSConfig sets the TLS configuration.
func WithTLSConfig(cfg *tls.Config) ServerOption {
	return func(s *Server) {
		s.tlsConfig = cfg
	}
}

// WithAuthenticator sets the authenticator.
func WithAuthenticator(authenticator protocol.Authenticator) ServerOption {
	return func(s *Server) {
		s.authenticator = authenticator
	}
}

// MustNewServer creates a new WebTransport server and panics on error.
func MustNewServer(conf WtConf, handler protocol.StatefulHandler, opts ...ServerOption) *Server {
	s, err := NewServer(conf, handler, opts...)
	if err != nil {
		panic(err)
	}
	return s
}

// NewServer creates a new WebTransport server.
func NewServer(conf WtConf, handler protocol.StatefulHandler, opts ...ServerOption) (*Server, error) {
	if err := conf.SetUp(); err != nil {
		return nil, err
	}

	// WebTransport requires TLS
	if conf.resolvedCertFile == "" || conf.resolvedKeyFile == "" {
		return nil, ErrTLSCertRequired
	}

	s := &Server{
		conf:        conf,
		handler:     handler,
		connections: make(map[string]*Connection),
		userConns:   make(map[string]map[string]*Connection),
		done:        make(chan struct{}),
		bufPool:     buffer.NewPool(conf.ReadBufferSize),
	}

	// Load TLS certificate
	cert, err := tls.LoadX509KeyPair(conf.resolvedCertFile, conf.resolvedKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	s.tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h3"}, // HTTP/3 is required for WebTransport
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
			name = "webtransport-server"
		}
		s.metrics = stat.NewMetrics(name)
	}

	// Create WebTransport server with http3.Server
	h3Server := &http3.Server{
		Addr:      s.conf.Addr,
		TLSConfig: s.tlsConfig,
	}
	// Must call ConfigureHTTP3Server to register WebTransport SETTINGS
	// and enable datagrams, otherwise clients get ERR_METHOD_NOT_SUPPORTED.
	webtransport.ConfigureHTTP3Server(h3Server)

	s.wtServer = &webtransport.Server{
		H3: h3Server,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins by default
		},
	}

	return s, nil
}

// Start starts the WebTransport server.
func (s *Server) Start() {
	logx.Infof("WebTransport server listening on %s", s.conf.Addr)

	// Register WebTransport handler on the http3.Server
	mux := http.NewServeMux()
	mux.HandleFunc(s.conf.Path, s.handleWebTransport)
	s.wtServer.H3.Handler = mux

	if err := s.wtServer.ListenAndServeTLS(s.conf.resolvedCertFile, s.conf.resolvedKeyFile); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			logx.Errorf("WebTransport server error: %v", err)
		}
	}
}

// HandleWebSocket handles WebTransport upgrade and connection.
// Exported so it can be used with custom HTTP mux.
func (s *Server) HandleWebTransport(w http.ResponseWriter, r *http.Request) {
	s.handleWebTransport(w, r)
}

// handleWebTransport handles WebTransport upgrade and connection.
func (s *Server) handleWebTransport(w http.ResponseWriter, r *http.Request) {
	// Check connection limit
	if s.conf.MaxConnections > 0 && s.connCount.Load() >= s.conf.MaxConnections {
		http.Error(w, ErrConnectionLimit.Error(), http.StatusServiceUnavailable)
		return
	}

	// Upgrade to WebTransport session
	session, err := s.wtServer.Upgrade(w, r)
	if err != nil {
		logx.Errorf("WebTransport upgrade error: %v", err)
		return
	}

	connID := generateConnID()
	wtConn := NewConnection(connID, session, s.conf.WriteTimeout)

	// Authentication
	if s.conf.Auth.Enabled {
		authReq := &protocol.AuthRequest{
			Protocol:    protocol.ProtocolWebTransport,
			HTTPRequest: r,
		}

		userID, err := s.authenticator.Authenticate(context.Background(), wtConn, authReq)
		if err != nil {
			logx.Errorf("authentication failed: %v", err)
			session.CloseWithError(0, "authentication failed")
			return
		}

		if userID != "" {
			wtConn.SetUserID(userID)
			s.BindUser(wtConn, userID)
		}
	}

	s.connCount.Add(1)
	s.registerConnection(wtConn)
	defer s.unregisterConnection(wtConn)

	// Notify handler
	if err := s.handler.OnConnect(wtConn); err != nil {
		logx.Errorf("on connect error: %v", err)
		wtConn.Close()
		return
	}

	s.wg.Add(1)
	defer s.wg.Done()

	// Start write pump: consumes messages from conn.send channel
	// and writes them to the client via new unidirectional streams.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.writePump(wtConn)
	}()

	// Read loop: accept streams and read messages
	for {
		select {
		case <-s.done:
			return
		case <-wtConn.Done():
			return
		default:
			stream, err := session.AcceptStream(context.Background())
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					logx.Debugf("accept stream error: %v", err)
				}
				return
			}

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handleStream(wtConn, stream)
			}()
		}
	}
}

// handleStream reads messages from a WebTransport bidirectional stream.
// The stream is kept alive for writePump to send responses back.
func (s *Server) handleStream(conn *Connection, stream *webtransport.Stream) {
	// Store as response stream so writePump can write back on it.
	conn.SetResponseStream(stream)

	buf := make([]byte, s.conf.ReadBufferSize)
	for {
		select {
		case <-s.done:
			return
		case <-conn.Done():
			return
		default:
			if s.conf.IdleTimeout > 0 {
				(*stream).SetReadDeadline(time.Now().Add(s.conf.IdleTimeout))
			}

			n, err := (*stream).Read(buf)
			if err != nil {
				return
			}

			msg := make([]byte, n)
			copy(msg, buf[:n])

			if err := s.handler.OnMessage(conn, msg); err != nil {
				logx.Errorf("message handler error: %v", err)
			}
		}
	}
}

// writePump drains the connection's send channel and writes each message
// back to the client on the bidirectional stream.
func (s *Server) writePump(conn *Connection) {
	// Wait for the first bidirectional stream to be accepted and set.
	select {
	case <-s.done:
		return
	case <-conn.Done():
		return
	case <-conn.StreamReady():
	}

	for {
		select {
		case <-s.done:
			return
		case <-conn.Done():
			return
		case msg := <-conn.ReceiveChannel():
			stream := conn.ResponseStream()
			if stream == nil {
				logx.Errorf("writePump: no response stream")
				return
			}

			if s.conf.WriteTimeout > 0 {
				(*stream).SetWriteDeadline(time.Now().Add(s.conf.WriteTimeout))
			}

			if _, err := (*stream).Write(msg); err != nil {
				logx.Errorf("writePump: write error: %v", err)
				return
			}
		}
	}
}

// Stop stops the WebTransport server gracefully.
func (s *Server) Stop() {
	s.once.Do(func() {
		close(s.done)

		// Close all connections
		s.connectionsMu.Lock()
		for _, conn := range s.connections {
			conn.Close()
		}
		s.connectionsMu.Unlock()

		// Wait for all goroutines to finish
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			logx.Info("timeout waiting for connections to close")
		}

		// Shutdown WebTransport server
		if s.wtServer != nil {
			s.wtServer.Close()
		}

		// Cleanup temp files created from environment variables
		s.conf.Cleanup()
	})
}

// Protocol returns the server protocol.
func (s *Server) Protocol() protocol.Protocol {
	return protocol.ProtocolWebTransport
}

// registerConnection registers a connection.
func (s *Server) registerConnection(conn *Connection) {
	s.connectionsMu.Lock()
	s.connections[conn.ID()] = conn
	s.connectionsMu.Unlock()
	s.connCount.Add(1)
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
