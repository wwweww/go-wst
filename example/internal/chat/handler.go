package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/wwweww/go-wst/internal/protocol"
)

// Message represents a chat message.
type Message struct {
	Type    string `json:"type"`
	UserID  string `json:"userId,omitempty"`
	RoomID  string `json:"roomId,omitempty"`
	Content string `json:"content,omitempty"`
}

// Handler handles chat connections with room management.
type Handler struct {
	mu         sync.RWMutex
	rooms      map[string]map[string]protocol.Connection // roomID -> userID -> conn
	userToRoom map[string]string                         // userID -> roomID
}

// NewHandler creates a new chat handler.
func NewHandler() *Handler {
	return &Handler{
		rooms:      make(map[string]map[string]protocol.Connection),
		userToRoom: make(map[string]string),
	}
}

// OnConnect is called when a new connection is established.
func (h *Handler) OnConnect(conn protocol.Connection) error {
	log.Printf("[%s] connected: %s userID=%s", conn.Protocol(), conn.ID(), conn.UserID())
	return conn.SendJSON(context.Background(), Message{
		Type:    "system",
		Content: "Welcome to the chat!",
	})
}

// OnMessage is called when a message is received.
func (h *Handler) OnMessage(conn protocol.Connection, raw []byte) error {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}

	// For unauthenticated connections, require userID in join message
	if msg.UserID == "" {
		msg.UserID = conn.UserID()
	}

	switch msg.Type {
	case "join":
		h.handleJoin(conn, &msg)
	case "message":
		h.handleMessage(conn, &msg)
	case "leave":
		h.handleLeave(conn, &msg)
	default:
		conn.SendJSON(context.Background(), Message{
			Type:    "error",
			Content: fmt.Sprintf("unknown message type: %s", msg.Type),
		})
	}
	return nil
}

// OnDisconnect is called when a connection is closed.
func (h *Handler) OnDisconnect(conn protocol.Connection, err error) {
	userID := conn.UserID()
	if userID == "" {
		log.Printf("[%s] disconnected: %v", conn.ID(), err)
		return
	}

	log.Printf("[%s] disconnected: userID=%s err=%v", conn.Protocol(), userID, err)

	h.mu.Lock()
	roomID, ok := h.userToRoom[userID]
	if ok {
		if room, exists := h.rooms[roomID]; exists {
			delete(room, userID)
			if len(room) == 0 {
				delete(h.rooms, roomID)
			}
		}
		delete(h.userToRoom, userID)
	}
	h.mu.Unlock()

	if roomID != "" {
		h.broadcastToRoom(roomID, Message{
			Type:    "leave",
			UserID:  userID,
			Content: fmt.Sprintf("%s left the room", userID),
		})
	}
}

func (h *Handler) handleJoin(conn protocol.Connection, msg *Message) {
	// Bind userID if authenticated (from JWT/Envoy) or use message-provided ID
	userID := conn.UserID()
	if userID == "" {
		userID = msg.UserID
		conn.SetUserID(userID)
	}

	conn.SetMetadata("roomId", msg.RoomID)
	conn.SetMetadata("username", msg.Content) // reuse content as username for simplicity

	h.mu.Lock()
	if h.rooms[msg.RoomID] == nil {
		h.rooms[msg.RoomID] = make(map[string]protocol.Connection)
	}
	h.rooms[msg.RoomID][userID] = conn
	h.userToRoom[userID] = msg.RoomID
	h.mu.Unlock()

	conn.SendJSON(context.Background(), map[string]interface{}{
		"type":    "joined",
		"roomId":  msg.RoomID,
		"userId":  userID,
		"content": "Successfully joined the room",
	})

	h.broadcastToRoom(msg.RoomID, Message{
		Type:    "join",
		UserID:  userID,
		Content: fmt.Sprintf("%s joined the room", userID),
	})

	log.Printf("user %s joined room %s", userID, msg.RoomID)
}

func (h *Handler) handleMessage(conn protocol.Connection, msg *Message) {
	roomID, _ := conn.Metadata()["roomId"].(string)
	if roomID == "" {
		conn.SendJSON(context.Background(), Message{
			Type:    "error",
			Content: "you are not in a room, send {\"type\":\"join\",\"roomId\":\"room1\"} first",
		})
		return
	}

	userID := conn.UserID()
	if userID == "" {
		userID = msg.UserID
	}

	h.broadcastToRoom(roomID, Message{
		Type:    "message",
		UserID:  userID,
		RoomID:  roomID,
		Content: msg.Content,
	})
}

func (h *Handler) handleLeave(conn protocol.Connection, msg *Message) {
	roomID, _ := conn.Metadata()["roomId"].(string)
	if roomID == "" {
		return
	}

	userID := conn.UserID()
	if userID == "" {
		userID = msg.UserID
	}

	h.mu.Lock()
	if room, exists := h.rooms[roomID]; exists {
		delete(room, userID)
		if len(room) == 0 {
			delete(h.rooms, roomID)
		}
	}
	delete(h.userToRoom, userID)
	h.mu.Unlock()

	h.broadcastToRoom(roomID, Message{
		Type:    "leave",
		UserID:  userID,
		Content: fmt.Sprintf("%s left the room", userID),
	})
}

func (h *Handler) broadcastToRoom(roomID string, msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}

	h.mu.RLock()
	room, exists := h.rooms[roomID]
	if !exists {
		h.mu.RUnlock()
		return
	}

	conns := make([]protocol.Connection, 0, len(room))
	for _, c := range room {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		if err := c.Send(context.Background(), data); err != nil {
			log.Printf("send to %s failed: %v", c.UserID(), err)
		}
	}
}
