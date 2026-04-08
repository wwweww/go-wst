package stateless

import (
	"time"
)

// Config holds configuration for stateless connection management.
type Config struct {
	// Polling configuration
	PollInterval time.Duration `json:",default=100ms"` // Polling interval
	BatchSize    int           `json:",default=100"`   // Messages per batch

	// Acknowledgment
	AckTimeout time.Duration `json:",default=30s"` // Ack timeout
}
