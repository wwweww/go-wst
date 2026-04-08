package stateful

import "time"

// Config holds configuration for stateful connection management.
type Config struct {
	// Sharding
	HubShards int `json:",default=32"` // Number of hub shards (must be power of 2)

	// Heartbeat
	HeartbeatInterval time.Duration `json:",default=30s"` // Heartbeat interval
	HeartbeatTimeout  time.Duration `json:",default=90s"` // Time to wait for heartbeat response

	// Connection limits
	MaxConnectionsPerUser int `json:",default=5"` // Max connections per user
}
