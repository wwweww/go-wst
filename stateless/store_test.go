package stateless

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryStore_Store(t *testing.T) {
	store := NewMemoryStore()

	err := store.Store(context.Background(), "user1", Message{
		Type: "notification",
		Data: []byte("test message"),
	})
	assert.NoError(t, err)

	err = store.Store(context.Background(), "user1", Message{
		Type: "notification",
		Data: []byte("test message 2"),
	})
	assert.NoError(t, err)

	// Check last ID was set
	assert.Equal(t, int64(2), store.lastID["user1"])
}

func TestMemoryStore_Fetch(t *testing.T) {
	store := NewMemoryStore()

	// Store some messages
	store.Store(context.Background(), "user1", Message{Type: "type1", Data: []byte("msg1")})
	store.Store(context.Background(), "user1", Message{Type: "type2", Data: []byte("msg2")})
	store.Store(context.Background(), "user1", Message{Type: "type3", Data: []byte("msg3")})

	// Fetch all messages
	msgs, err := store.Fetch(context.Background(), "user1", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, msgs, 3)

	// Verify order
	assert.Equal(t, int64(1), msgs[0].ID)
	assert.Equal(t, int64(2), msgs[1].ID)
	assert.Equal(t, int64(3), msgs[2].ID)

	// Fetch with sinceID
	msgs, err = store.Fetch(context.Background(), "user1", 1, 10)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, int64(2), msgs[0].ID)

	// Fetch with limit
	msgs, err = store.Fetch(context.Background(), "user1", 0, 2)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)
}

func TestMemoryStore_FetchNonExistentUser(t *testing.T) {
	store := NewMemoryStore()

	msgs, err := store.Fetch(context.Background(), "nonexistent", 0, 10)
	assert.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestMemoryStore_Ack(t *testing.T) {
	store := NewMemoryStore()

	// Ack is a no-op for memory store
	err := store.Ack(context.Background(), "user1", 1)
	assert.NoError(t, err)
}

func TestMemoryStore_Clear(t *testing.T) {
	store := NewMemoryStore()

	// Store some messages
	store.Store(context.Background(), "user1", Message{Type: "type1", Data: []byte("msg1")})
	store.Store(context.Background(), "user2", Message{Type: "type2", Data: []byte("msg2")})

	// Clear user1
	store.Clear("user1")

	// User1 should be empty
	msgs, err := store.Fetch(context.Background(), "user1", 0, 10)
	assert.NoError(t, err)
	assert.Nil(t, msgs)

	// User2 should still have messages
	msgs, err = store.Fetch(context.Background(), "user2", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
}

func TestMemoryStore_ClearAll(t *testing.T) {
	store := NewMemoryStore()

	// Store some messages
	store.Store(context.Background(), "user1", Message{Type: "type1", Data: []byte("msg1")})
	store.Store(context.Background(), "user2", Message{Type: "type2", Data: []byte("msg2")})

	// Clear all
	store.ClearAll()

	// All users should be empty
	msgs, err := store.Fetch(context.Background(), "user1", 0, 10)
	assert.NoError(t, err)
	assert.Nil(t, msgs)

	msgs, err = store.Fetch(context.Background(), "user2", 0, 10)
	assert.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestServer_Fetch(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(Config{
		PollInterval: 100,
		BatchSize:    100,
	}, store)

	// Store some messages
	store.Store(context.Background(), "user1", Message{Type: "type1", Data: []byte("msg1")})
	store.Store(context.Background(), "user1", Message{Type: "type2", Data: []byte("msg2")})

	// Fetch messages
	msgs, err := server.Fetch(context.Background(), FetchRequest{
		UserID:  "user1",
		SinceID: 0,
		Limit:   10,
	})
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)
}

func TestServer_Send(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(Config{
		PollInterval: 100,
		BatchSize:    100,
	}, store)

	// Send message
	err := server.Send(context.Background(), SendRequest{
		UserID: "user1",
		Topic:  "test",
		Data:   []byte("hello"),
	})
	assert.NoError(t, err)

	// Verify message was stored
	msgs, err := store.Fetch(context.Background(), "user1", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "test", msgs[0].Topic)
	assert.Equal(t, []byte("hello"), msgs[0].Data)
}

func TestServer_Ack(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(Config{
		PollInterval: 100,
		BatchSize:    100,
	}, store)

	err := server.Ack(context.Background(), "user1", 1)
	assert.NoError(t, err)
}

func TestHandler_Fetch(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(Config{}, store)
	handler := NewHandler(server)

	// Store message
	store.Store(context.Background(), "user1", Message{Type: "test", Data: []byte("msg")})

	// Fetch via handler
	msgs, err := handler.Fetch(context.Background(), FetchRequest{
		UserID:  "user1",
		SinceID: 0,
		Limit:   10,
	})
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
}

func TestHandler_Send(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(Config{}, store)
	handler := NewHandler(server)

	// Send via handler
	err := handler.Send(context.Background(), SendRequest{
		UserID: "user1",
		Data:   []byte("test"),
	})
	assert.NoError(t, err)

	// Verify
	msgs, err := store.Fetch(context.Background(), "user1", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
}
