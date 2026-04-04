package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		// Allow connections with no Origin header (non-browser clients)
		// or from the localhost allowlist.
		return origin == "" || IsAllowedOrigin(origin)
	},
}

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// Hub manages all active WebSocket clients and broadcasts messages.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("Client connected (%d total)", len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("Client disconnected (%d remaining)", len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			var dead []*Client
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					dead = append(dead, client)
				}
			}
			h.mu.RUnlock()
			// Clean up slow/dead clients under a write lock
			if len(dead) > 0 {
				h.mu.Lock()
				for _, client := range dead {
					if _, ok := h.clients[client]; ok {
						close(client.send)
						delete(h.clients, client)
					}
				}
				h.mu.Unlock()
			}
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg []byte) {
	h.broadcast <- msg
}

// ServeWS upgrades an HTTP connection to WebSocket.
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
	hub.register <- client

	go client.writePump()
	go client.readPump()
}

// allowedClientMessageTypes are the only message types the server will
// accept from WebSocket clients. Anything else is dropped silently.
var allowedClientMessageTypes = map[string]bool{
	"state_update": true, // canvas state sync
	"ping":         true, // keepalive
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	// Cap incoming message size to 1MB to prevent abuse
	c.conn.SetReadLimit(1 << 20)
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		// Validate: must be JSON with an allowed "type" field.
		// Reject anything else to prevent spoofed terminal output
		// or file_changed events from untrusted clients.
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &envelope); err != nil {
			log.Printf("WS: dropping non-JSON message from client")
			continue
		}
		if !allowedClientMessageTypes[envelope.Type] {
			log.Printf("WS: dropping disallowed message type %q from client", envelope.Type)
			continue
		}
		c.hub.broadcast <- message
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			break
		}
	}
}
