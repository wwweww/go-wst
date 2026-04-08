package hybrid

import (
	"context"
	"sync"
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/wwweww/go-wst/ws"
	"github.com/wwweww/go-wst/wt"
	"github.com/zeromicro/go-zero/core/logx"
)

// FallbackManager manages protocol fallback decisions.
type FallbackManager struct {
	conf     HybridConf
	wsServer *ws.Server
	wtServer *wt.Server

	// Connection tracking
	fallbackCount   map[string]int // clientID -> fallback count
	fallbackCountMu sync.RWMutex

	// Statistics
	stats   FallbackStats
	statsMu sync.RWMutex
}

// FallbackStats contains fallback statistics.
type FallbackStats struct {
	TotalConnections    int64
	WebSocketOnly       int64
	WebTransportOnly    int64
	WebTransportSuccess int64
	WebTransportFailed  int64
	FallbackToWS        int64
}

// NewFallbackManager creates a new FallbackManager.
func NewFallbackManager(conf HybridConf, wsServer *ws.Server, wtServer *wt.Server) *FallbackManager {
	return &FallbackManager{
		conf:          conf,
		wsServer:      wsServer,
		wtServer:      wtServer,
		fallbackCount: make(map[string]int),
	}
}

// SelectProtocol selects the best protocol for a client.
func (m *FallbackManager) SelectProtocol(hello ClientHello) protocol.Protocol {
	switch m.conf.Protocol {
	case ProtocolWebSocket:
		return protocol.ProtocolWebSocket

	case ProtocolWebTransport:
		// Check if client supports WebTransport
		if !hello.SupportsWT {
			logx.Infof("client %s does not support WebTransport, using WebSocket", hello.ClientID)
			return protocol.ProtocolWebSocket
		}
		return protocol.ProtocolWebTransport

	case ProtocolAuto:
		return m.selectAutoProtocol(hello)

	default:
		return protocol.ProtocolWebSocket
	}
}

// selectAutoProtocol automatically selects the best protocol.
func (m *FallbackManager) selectAutoProtocol(hello ClientHello) protocol.Protocol {
	// Check if client supports WebTransport
	if !hello.SupportsWT {
		logx.Debugf("client %s does not support WebTransport", hello.ClientID)
		return protocol.ProtocolWebSocket
	}

	// Check fallback history
	m.fallbackCountMu.RLock()
	fallbackCount := m.fallbackCount[hello.ClientID]
	m.fallbackCountMu.RUnlock()

	// If client has failed WebTransport too many times, use WebSocket directly
	if fallbackCount >= m.conf.MaxRetries {
		logx.Infof("client %s has failed WebTransport %d times, using WebSocket", hello.ClientID, fallbackCount)
		return protocol.ProtocolWebSocket
	}

	// Prefer WebTransport if available and client supports it
	if m.wtServer != nil {
		return protocol.ProtocolWebTransport
	}

	return protocol.ProtocolWebSocket
}

// RecordFallback records a fallback event.
func (m *FallbackManager) RecordFallback(clientID string, from, to protocol.Protocol) {
	m.fallbackCountMu.Lock()
	m.fallbackCount[clientID]++
	m.fallbackCountMu.Unlock()

	m.statsMu.Lock()
	m.stats.FallbackToWS++
	if from == protocol.ProtocolWebTransport {
		m.stats.WebTransportFailed++
	}
	m.statsMu.Unlock()

	logx.Infof("client %s fell back from %s to %s", clientID, from, to)
}

// RecordConnection records a successful connection.
func (m *FallbackManager) RecordConnection(proto protocol.Protocol) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()

	m.stats.TotalConnections++
	switch proto {
	case protocol.ProtocolWebSocket:
		m.stats.WebSocketOnly++
	case protocol.ProtocolWebTransport:
		m.stats.WebTransportSuccess++
	}
}

// GetStats returns the current fallback statistics.
func (m *FallbackManager) GetStats() FallbackStats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()
	return m.stats
}

// ShouldFallback determines if a connection should fallback.
func (m *FallbackManager) ShouldFallback(ctx context.Context, clientID string, err error) bool {
	if m.conf.FallbackStrategy == FallbackNone {
		return false
	}

	// Check context
	if ctx.Err() != nil {
		return false
	}

	switch m.conf.FallbackStrategy {
	case FallbackAuto, FallbackError:
		// Fallback on any error
		return err != nil

	case FallbackTimeout:
		// Fallback after timeout
		select {
		case <-ctx.Done():
			return true
		case <-time.After(m.conf.FallbackTimeout):
			return true
		}
	}

	return false
}

// ResetFallbackCount resets the fallback count for a client.
func (m *FallbackManager) ResetFallbackCount(clientID string) {
	m.fallbackCountMu.Lock()
	delete(m.fallbackCount, clientID)
	m.fallbackCountMu.Unlock()
}

// CleanupStaleClients cleans up stale client entries.
func (m *FallbackManager) CleanupStaleClients(maxAge time.Duration) {
	// This would typically be called periodically to clean up old entries
	// For simplicity, we just clear all entries older than maxAge
	m.fallbackCountMu.Lock()
	defer m.fallbackCountMu.Unlock()

	// In a real implementation, we would track timestamps and clean up old entries
	// For now, we just reset the map if it gets too large
	if len(m.fallbackCount) > 10000 {
		m.fallbackCount = make(map[string]int)
	}
}

// FallbackHandler handles the fallback process.
type FallbackHandler struct {
	manager *FallbackManager
	handler protocol.StatefulHandler
}

// NewFallbackHandler creates a new FallbackHandler.
func NewFallbackHandler(manager *FallbackManager, handler protocol.StatefulHandler) *FallbackHandler {
	return &FallbackHandler{
		manager: manager,
		handler: handler,
	}
}

// OnConnect handles connection establishment.
func (h *FallbackHandler) OnConnect(conn protocol.Connection) error {
	h.manager.RecordConnection(conn.Protocol())
	return h.handler.OnConnect(conn)
}

// OnMessage handles incoming messages.
func (h *FallbackHandler) OnMessage(conn protocol.Connection, msg []byte) error {
	return h.handler.OnMessage(conn, msg)
}

// OnDisconnect handles connection closure.
func (h *FallbackHandler) OnDisconnect(conn protocol.Connection, err error) {
	// Record fallback if connection failed
	if err != nil && conn.Protocol() == protocol.ProtocolWebTransport {
		if userID := conn.UserID(); userID != "" {
			h.manager.RecordFallback(userID, protocol.ProtocolWebTransport, protocol.ProtocolWebSocket)
		}
	}
	h.handler.OnDisconnect(conn, err)
}
