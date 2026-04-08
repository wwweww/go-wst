package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"

	"github.com/wwweww/go-wst/protocol"
	"github.com/wwweww/go-wst/stateless"
	"github.com/wwweww/go-wst/ws"
	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/notification.yaml", "the config file")

// NotificationHandler handles notification WebSocket connections.
type NotificationHandler struct {
	store *stateless.MemoryStore
}

// NewNotificationHandler creates a new NotificationHandler.
func NewNotificationHandler(store *stateless.MemoryStore) *NotificationHandler {
	return &NotificationHandler{
		store: store,
	}
}

// OnConnect is called when a new connection is established.
func (h *NotificationHandler) OnConnect(conn protocol.Connection) error {
	log.Printf("User connected: %s", conn.ID())
	return nil
}

// OnMessage is called when a message is received.
func (h *NotificationHandler) OnMessage(conn protocol.Connection, msg []byte) error {
	// Parse message type
	var data struct {
		Type     string `json:"type"`
		RoomID   string `json:"roomId"`
		UserID   string `json:"userId"`
		Username string `json:"username"`
		Content  string `json:"content"`
		SinceID  int64  `json:"sinceId"`
	}

	if err := json.Unmarshal(msg, &data); err != nil {
		return err
	}

	ctx := context.Background()

	switch data.Type {
	case "join":
		conn.SetUserID(data.UserID)
		conn.SetMetadata("roomId", data.RoomID)
		conn.SetMetadata("username", data.Username)

		// Send welcome
		return conn.SendJSON(ctx, map[string]interface{}{
			"type":    "joined",
			"roomId":  data.RoomID,
			"userId":  data.UserID,
			"message": "Successfully joined",
		})

	case "fetch":
		// Get messages since last ID
		sinceID := data.SinceID
		if sinceID == 0 {
			sinceID, _ = conn.Metadata()["lastId"].(int64)
		}

		messages, err := h.store.Fetch(ctx, data.RoomID, sinceID, 100)
		if err != nil {
			log.Printf("Failed to fetch messages: %v", err)
			return nil
		}

		// Send messages
		for _, m := range messages {
			if err := conn.SendJSON(ctx, m); err != nil {
				log.Printf("Failed to send message: %v", err)
			}
		}

		// Update last ID
		if len(messages) > 0 {
			conn.SetMetadata("lastId", messages[len(messages)-1].ID)
		}

	case "message":
		// Save message
		msg := stateless.Message{
			Type:  "text",
			Topic: data.RoomID,
			Data:  []byte(data.Content),
		}

		if err := h.store.Store(ctx, data.RoomID, msg); err != nil {
			log.Printf("Failed to store message: %v", err)
			return conn.SendJSON(ctx, map[string]string{
				"type":  "error",
				"error": "Failed to store message",
			})
		}

		// Acknowledge
		return conn.SendJSON(ctx, map[string]string{
			"type":    "ack",
			"status":  "ok",
			"message": "Message stored",
		})
	}

	return nil
}

// OnDisconnect is called when a connection is closed.
func (h *NotificationHandler) OnDisconnect(conn protocol.Connection, err error) {
	log.Printf("User disconnected: %s, error: %v", conn.ID(), err)
}

func main() {
	flag.Parse()

	var c ws.WsConf
	conf.MustLoad(*configFile, &c)

	// Create in-memory store for demo
	// In production, use Redis, MySQL, etc.
	store := stateless.NewMemoryStore()

	// Create handler
	handler := NewNotificationHandler(store)

	// Create and start server
	server := ws.MustNewServer(c, handler)

	log.Println("Starting notification server on", c.Addr)
	log.Println("WebSocket URL: ws://localhost" + c.Addr + c.Path)
	server.Start()
}
