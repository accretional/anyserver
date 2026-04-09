package wormhole

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"golang.org/x/net/websocket"
)

// Message is the JSON envelope for command wormhole WebSocket messages.
type Message struct {
	Type    string          `json:"type"`
	Token   string          `json:"token,omitempty"`
	OK      *bool           `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// CommandHandler returns an http.Handler for the /wormhole/command WebSocket endpoint.
func CommandHandler(cw *CommandWormhole) http.Handler {
	wsServer := websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			// Accept any origin -- auth is via token, not origin
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			handleCommandWS(cw, ws)
		},
	}
	return wsServer
}

func handleCommandWS(cw *CommandWormhole, ws *websocket.Conn) {
	defer ws.Close()

	// Check state: if not awaiting auth, reject immediately
	state := cw.State()
	if state != stateAwaitingAuth {
		sendMsg(ws, Message{Type: "auth_result", OK: boolPtr(false)})
		return
	}

	// Wait for auth message with a per-connection timeout (30s)
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))

	var msg Message
	if err := websocket.JSON.Receive(ws, &msg); err != nil {
		return
	}

	if msg.Type != "auth" || msg.Token == "" {
		sendMsg(ws, Message{Type: "auth_result", OK: boolPtr(false)})
		return
	}

	if !cw.Authenticate(msg.Token) {
		sendMsg(ws, Message{Type: "auth_result", OK: boolPtr(false)})
		return
	}

	// Auth succeeded
	sendMsg(ws, Message{Type: "auth_result", OK: boolPtr(true)})
	log.Printf("command wormhole: client authenticated")

	// Clear deadline for command phase
	ws.SetReadDeadline(time.Time{})

	// Enter bidirectional command loop
	commandLoop(cw, ws)

	// Client disconnected
	log.Printf("command wormhole: client disconnected")
	cw.OnClientDisconnect()
}

func commandLoop(cw *CommandWormhole, ws *websocket.Conn) {
	wh := cw.Wormhole()

	// Subscribe to wormhole for server->client events
	ch, unsub := wh.Subscribe()
	defer unsub()

	// Read from client in a goroutine
	clientMsg := make(chan Message, 16)
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			var msg Message
			if err := websocket.JSON.Receive(ws, &msg); err != nil {
				return
			}
			select {
			case clientMsg <- msg:
			default:
			}
		}
	}()

	// Heartbeat ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-clientDone:
			return

		case line := <-ch:
			// Server->client: wormhole broadcast event
			sendMsg(ws, Message{
				Type:    "event",
				Payload: json.RawMessage(`"` + jsonEscape(string(line)) + `"`),
			})

		case msg := <-clientMsg:
			switch msg.Type {
			case "pong":
				// Heartbeat response, nothing to do
			case "command":
				// Write command payload into the wormhole for visibility
				if msg.Payload != nil {
					var payload string
					if json.Unmarshal(msg.Payload, &payload) == nil {
						wh.Write([]byte("cmd: " + payload + "\n"))
					}
				}
			}

		case <-ticker.C:
			sendMsg(ws, Message{Type: "ping"})
		}
	}
}

func sendMsg(ws *websocket.Conn, msg Message) error {
	ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return websocket.JSON.Send(ws, msg)
}

func boolPtr(b bool) *bool { return &b }

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// Strip surrounding quotes from json.Marshal result
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
