package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"log/slog"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	maxMessage = 1 << 20
)

const sendBuffer = 128

type Envelope struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Payload   any    `json:"payload"`
}

type AudioFrame struct {
	CallID string `json:"callId"`
	Data   string `json:"data"` // base64 encoded audio
}

type IncomingMessage struct {
	Type    string     `json:"type"`
	Audio   AudioFrame `json:"audio,omitempty"`
	Payload any        `json:"payload,omitempty"`
}

type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (userID string, err error)
}

type CallStore interface {
	GetCallByID(ctx context.Context, callID string) (callerID, calleeID, status string, err error)
}

type client struct {
	conn      *websocket.Conn
	userID    string
	send      chan []byte
	closeOnce sync.Once
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}

type Manager struct {
	logger         *slog.Logger
	tokenValidator TokenValidator
	callStore      CallStore

	mu      sync.Mutex
	clients map[*client]struct{}
}

func NewManager(logger *slog.Logger, tokenValidator TokenValidator, callStore CallStore) *Manager {
	return &Manager{
		logger:         logger.With("component", "ws"),
		tokenValidator: tokenValidator,
		callStore:      callStore,
		clients:        make(map[*client]struct{}),
	}
}

func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(m.handle)
}

func (m *Manager) CloseAll() {
	clients := m.snapshotClients()
	for _, c := range clients {
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server shutdown"),
			time.Now().Add(writeWait),
		)
		c.close()
	}
}

func (m *Manager) Broadcast(env Envelope) {
	b, err := encodeJSON(env)
	if err != nil {
		m.logger.Error("ws broadcast marshal failed", "error", err, "type", env.Type)
		return
	}

	clients := m.snapshotClients()
	for _, c := range clients {
		select {
		case c.send <- b:
		default:
			m.logger.Warn("ws slow client dropped")
			m.untrack(c)
			c.close()
		}
	}
}

func (m *Manager) SendToUser(userID string, env Envelope) {
	b, err := encodeJSON(env)
	if err != nil {
		m.logger.Error("ws send to user marshal failed", "error", err, "type", env.Type, "userID", userID)
		return
	}

	clients := m.snapshotClients()
	for _, c := range clients {
		if c.userID != userID {
			continue
		}
		select {
		case c.send <- b:
		default:
			m.logger.Warn("ws slow client dropped", "userID", userID)
			m.untrack(c)
			c.close()
		}
	}
}

func (m *Manager) SendToUsers(userIDs []string, env Envelope) {
	if len(userIDs) == 0 {
		return
	}

	b, err := encodeJSON(env)
	if err != nil {
		m.logger.Error("ws send to users marshal failed", "error", err, "type", env.Type)
		return
	}

	userSet := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		userSet[id] = struct{}{}
	}

	clients := m.snapshotClients()
	for _, c := range clients {
		if _, ok := userSet[c.userID]; !ok {
			continue
		}
		select {
		case c.send <- b:
		default:
			m.logger.Warn("ws slow client dropped", "userID", c.userID)
			m.untrack(c)
			c.close()
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (m *Manager) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token := extractToken(r)
	if token == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	userID, err := m.tokenValidator.ValidateToken(r.Context(), token)
	if err != nil {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		m.logger.Warn("ws upgrade failed", "error", err)
		return
	}

	c := &client{
		conn:   conn,
		userID: userID,
		send:   make(chan []byte, sendBuffer),
	}
	m.track(c)
	defer m.untrack(c)
	defer c.close()

	m.logger.Info("ws connected", "remoteAddr", r.RemoteAddr, "userID", userID)

	conn.SetReadLimit(maxMessage)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	go m.writePump(c, r.RemoteAddr)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			m.logger.Info("ws disconnected", "remoteAddr", r.RemoteAddr, "userID", userID, "error", err)
			return
		}
		m.handleClientMessage(c, msg)
	}
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}

	return ""
}

func (m *Manager) writePump(c *client, remoteAddr string) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				m.logger.Info("ws write failed", "remoteAddr", remoteAddr, "error", err)
				c.close()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}

func (m *Manager) snapshotClients() []*client {
	m.mu.Lock()
	defer m.mu.Unlock()

	clients := make([]*client, 0, len(m.clients))
	for c := range m.clients {
		clients = append(clients, c)
	}
	return clients
}

func (m *Manager) track(c *client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[c] = struct{}{}
}

func (m *Manager) untrack(c *client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, c)
}

func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

type clientMessage struct {
	Type     string `json:"type"`
	CallID   string `json:"callId"`
	Data     string `json:"data"`
	Seq      int64  `json:"seq,omitempty"`
	SentAtMs int64  `json:"sentAtMs,omitempty"`
}

func (m *Manager) handleClientMessage(c *client, msg []byte) {
	var cm clientMessage
	if err := json.Unmarshal(msg, &cm); err != nil {
		return
	}

	if cm.Type != "audio.frame" && cm.Type != "video.frame" {
		return
	}

	if cm.CallID == "" || cm.Data == "" {
		return
	}

	callerID, calleeID, status, err := m.callStore.GetCallByID(context.Background(), cm.CallID)
	if err != nil {
		return
	}

	if status != "accepted" {
		return
	}

	var peerID string
	if c.userID == callerID {
		peerID = calleeID
	} else if c.userID == calleeID {
		peerID = callerID
	} else {
		return
	}

	payload := map[string]any{
		"callId": cm.CallID,
		"data":   cm.Data,
	}
	if cm.Seq != 0 {
		payload["seq"] = cm.Seq
	}
	if cm.SentAtMs != 0 {
		payload["sentAtMs"] = cm.SentAtMs
	}

	env := Envelope{
		Type:    cm.Type,
		Payload: payload,
	}

	b, err := encodeJSON(env)
	if err != nil {
		return
	}

	clients := m.snapshotClients()
	for _, peer := range clients {
		if peer.userID != peerID {
			continue
		}
		select {
		case peer.send <- b:
		default:
		}
	}
}
