package stateful

import (
	"sync"
	"sync/atomic"

	"github.com/wwweww/go-wst/internal/protocol"
)

// Hub manages all connections with sharding for better concurrency.
type Hub struct {
	shards    []*hubShard
	shardMask int
}

type hubShard struct {
	mu          sync.RWMutex
	connections map[string]protocol.Connection            // connID -> Connection
	userConns   map[string]map[string]protocol.Connection // userID -> connID -> Connection
}

// NewHub creates a new Hub with the specified number of shards.
// shards should be a power of 2 for optimal performance.
func NewHub(shards int) *Hub {
	if shards <= 0 {
		shards = 32
	}
	// Ensure shards is power of 2
	shards = nextPowerOf2(shards)

	h := &Hub{
		shards:    make([]*hubShard, shards),
		shardMask: shards - 1,
	}

	for i := 0; i < shards; i++ {
		h.shards[i] = &hubShard{
			connections: make(map[string]protocol.Connection),
			userConns:   make(map[string]map[string]protocol.Connection),
		}
	}

	return h
}

// getShard returns the shard for a given key.
func (h *Hub) getShard(key string) *hubShard {
	// Simple hash function using FNV-1a
	var hash uint32
	for _, c := range key {
		hash ^= uint32(c)
		hash *= 16777619
	}
	return h.shards[hash&uint32(h.shardMask)]
}

// Register adds a connection to the hub.
func (h *Hub) Register(conn protocol.Connection) {
	shard := h.getShard(conn.ID())
	shard.mu.Lock()
	shard.connections[conn.ID()] = conn
	shard.mu.Unlock()
}

// Unregister removes a connection from the hub.
func (h *Hub) Unregister(conn protocol.Connection) {
	shard := h.getShard(conn.ID())
	shard.mu.Lock()
	delete(shard.connections, conn.ID())

	if userID := conn.UserID(); userID != "" {
		if conns, ok := shard.userConns[userID]; ok {
			delete(conns, conn.ID())
			if len(conns) == 0 {
				delete(shard.userConns, userID)
			}
		}
	}
	shard.mu.Unlock()
}

// BindUser binds a connection to a user.
func (h *Hub) BindUser(conn protocol.Connection, userID string) {
	conn.SetUserID(userID)
	shard := h.getShard(conn.ID())
	shard.mu.Lock()
	if shard.userConns[userID] == nil {
		shard.userConns[userID] = make(map[string]protocol.Connection)
	}
	shard.userConns[userID][conn.ID()] = conn
	shard.mu.Unlock()
}

// Get returns a connection by connection ID.
func (h *Hub) Get(connID string) (protocol.Connection, bool) {
	shard := h.getShard(connID)
	shard.mu.RLock()
	conn, ok := shard.connections[connID]
	shard.mu.RUnlock()
	return conn, ok
}

// GetByUser returns connections for a user.
func (h *Hub) GetByUser(userID string) []protocol.Connection {
	// We need to check all shards since user might have connections in multiple shards
	var result []protocol.Connection
	for _, shard := range h.shards {
		shard.mu.RLock()
		if conns, ok := shard.userConns[userID]; ok {
			for _, conn := range conns {
				result = append(result, conn)
			}
		}
		shard.mu.RUnlock()
	}
	return result
}

// Count returns the total number of connections.
func (h *Hub) Count() int64 {
	var count int64
	for _, shard := range h.shards {
		shard.mu.RLock()
		count += int64(len(shard.connections))
		shard.mu.RUnlock()
	}
	return count
}

// ForEach iterates over all connections.
func (h *Hub) ForEach(fn func(conn protocol.Connection) bool) {
	for _, shard := range h.shards {
		shard.mu.RLock()
		for _, conn := range shard.connections {
			if !fn(conn) {
				shard.mu.RUnlock()
				return
			}
		}
		shard.mu.RUnlock()
	}
}

// nextPowerOf2 returns the next power of 2 >= n.
func nextPowerOf2(n int) int {
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n++
	return n
}

// HubStats holds statistics about the hub.
type HubStats struct {
	TotalConnections int64
	TotalUsers       int64
	ShardStats       []ShardStats
}

// ShardStats holds statistics about a single shard.
type ShardStats struct {
	Connections int
	Users       int
}

// Stats returns statistics about the hub.
func (h *Hub) Stats() HubStats {
	var stats HubStats
	stats.ShardStats = make([]ShardStats, len(h.shards))
	stats.TotalConnections = 0
	stats.TotalUsers = 0

	for i, shard := range h.shards {
		shard.mu.RLock()
		stats.ShardStats[i] = ShardStats{
			Connections: len(shard.connections),
			Users:       len(shard.userConns),
		}
		stats.TotalConnections += int64(len(shard.connections))
		stats.TotalUsers += int64(len(shard.userConns))
		shard.mu.RUnlock()
	}

	return stats
}

// AtomicCounter is a thread-safe counter.
type AtomicCounter struct {
	value atomic.Int64
}

// Inc increments the counter.
func (c *AtomicCounter) Inc() {
	c.value.Add(1)
}

// Dec decrements the counter.
func (c *AtomicCounter) Dec() {
	c.value.Add(-1)
}

// Value returns the current value.
func (c *AtomicCounter) Value() int64 {
	return c.value.Load()
}
