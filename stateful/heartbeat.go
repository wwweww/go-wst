package stateful

import (
	"context"
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
	"github.com/zeromicro/go-zero/core/logx"
)

// HeartbeatManager manages heartbeat for connections.
type HeartbeatManager struct {
	interval time.Duration
	timeout  time.Duration
	hub      *Hub
}

// NewHeartbeatManager creates a new HeartbeatManager.
func NewHeartbeatManager(hub *Hub, interval, timeout time.Duration) *HeartbeatManager {
	return &HeartbeatManager{
		interval: interval,
		timeout:  timeout,
		hub:      hub,
	}
}

// Start starts the heartbeat check loop.
func (m *HeartbeatManager) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkConnections()
		}
	}
}

// checkConnections checks all connections for heartbeat timeout.
func (m *HeartbeatManager) checkConnections() {
	now := time.Now()
	m.hub.ForEach(func(conn protocol.Connection) bool {
		// Check if connection has metadata with last activity
		meta := conn.Metadata()
		if lastActivity, ok := meta["lastActivity"].(time.Time); ok {
			if now.Sub(lastActivity) > m.timeout {
				logx.Infof("connection %s timed out, closing", conn.ID())
				conn.Close()
			}
		}
		return true
	})
}

// UpdateActivity updates the last activity time for a connection.
func UpdateActivity(conn protocol.Connection) {
	conn.SetMetadata("lastActivity", time.Now())
}
