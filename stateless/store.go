package stateless

import (
	"context"
	"sync"
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
)

// MemoryStore is an in-memory implementation of Store for testing.
// NOT recommended for production use.
type MemoryStore struct {
	mu       sync.RWMutex
	messages map[string][]Message // userID -> messages
	lastID   map[string]int64     // userID -> last message ID
}

// NewMemoryStore creates a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		messages: make(map[string][]Message),
		lastID:   make(map[string]int64),
	}
}

// Fetch retrieves messages for a user after the specified ID.
func (s *MemoryStore) Fetch(ctx context.Context, userID string, sinceID int64, limit int) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgs, ok := s.messages[userID]
	if !ok {
		return nil, nil
	}

	var result []Message
	for _, msg := range msgs {
		if msg.ID > sinceID {
			result = append(result, msg)
			if len(result) >= limit {
				break
			}
		}
	}

	return result, nil
}

// Ack acknowledges that messages have been processed.
// In memory store, this is a no-op as we don't track acknowledgments.
func (s *MemoryStore) Ack(ctx context.Context, userID string, msgID int64) error {
	// No-op for memory store
	return nil
}

// Store stores a message for a user.
func (s *MemoryStore) Store(ctx context.Context, userID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID and timestamp
	lastID := s.lastID[userID]
	msg.ID = lastID + 1
	msg.Timestamp = time.Now().UnixMilli()
	s.lastID[userID] = msg.ID

	// Store message
	s.messages[userID] = append(s.messages[userID], msg)

	return nil
}

// Clear clears all messages for a user.
func (s *MemoryStore) Clear(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, userID)
	delete(s.lastID, userID)
}

// ClearAll clears all messages.
func (s *MemoryStore) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = make(map[string][]Message)
	s.lastID = make(map[string]int64)
}

// Ensure MemoryStore implements Store interface.
var _ protocol.MessageStore = (*MemoryStore)(nil)
