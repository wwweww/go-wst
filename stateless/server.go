package stateless

import (
	"context"

	"github.com/wwweww/go-wst/internal/protocol"
)

// Store is an alias for protocol.MessageStore for convenience.
type Store = protocol.MessageStore

// FetchRequest is an alias for protocol.FetchRequest.
type FetchRequest = protocol.FetchRequest

// SendRequest is an alias for protocol.SendRequest.
type SendRequest = protocol.SendRequest

// Message is an alias for protocol.Message.
type Message = protocol.Message

// Server handles stateless polling-based message retrieval.
type Server struct {
	store  Store
	config Config
}

// NewServer creates a new stateless server.
func NewServer(config Config, store Store) *Server {
	return &Server{
		store:  store,
		config: config,
	}
}

// Fetch retrieves messages for a user.
func (s *Server) Fetch(ctx context.Context, req FetchRequest) ([]Message, error) {
	if req.Limit <= 0 {
		req.Limit = s.config.BatchSize
	}
	return s.store.Fetch(ctx, req.UserID, req.SinceID, req.Limit)
}

// Send sends a message to a user.
func (s *Server) Send(ctx context.Context, req SendRequest) error {
	msg := Message{
		ID:        0, // Will be set by store implementation
		Timestamp: 0, // Will be set by store implementation
		Type:      "message",
		Topic:     req.Topic,
		Data:      req.Data,
	}
	return s.store.Store(ctx, req.UserID, msg)
}

// Ack acknowledges that messages have been processed.
func (s *Server) Ack(ctx context.Context, userID string, msgID int64) error {
	return s.store.Ack(ctx, userID, msgID)
}

// Handler implements protocol.StatelessHandler.
type Handler struct {
	server *Server
}

// NewHandler creates a new stateless handler.
func NewHandler(server *Server) *Handler {
	return &Handler{server: server}
}

// Fetch implements protocol.StatelessHandler.
func (h *Handler) Fetch(ctx context.Context, req protocol.FetchRequest) ([]protocol.Message, error) {
	return h.server.Fetch(ctx, FetchRequest{
		UserID:  req.UserID,
		SinceID: req.SinceID,
		Limit:   req.Limit,
		Topic:   req.Topic,
	})
}

// Send implements protocol.StatelessHandler.
func (h *Handler) Send(ctx context.Context, req protocol.SendRequest) error {
	return h.server.Send(ctx, SendRequest{
		UserID: req.UserID,
		Topic:  req.Topic,
		Data:   req.Data,
	})
}
