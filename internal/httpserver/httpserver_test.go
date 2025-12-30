package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

type stubStore struct {
	readyErr error
}

func (s stubStore) Ready(ctx context.Context) error { return s.readyErr }

func (stubStore) CreateSession(ctx context.Context, peerName, peerIdentity string, nowMs int64) (storage.SessionRow, error) {
	return storage.SessionRow{}, errors.New("not implemented")
}

func (stubStore) ListSessions(ctx context.Context, status string) ([]storage.SessionRow, error) {
	return nil, errors.New("not implemented")
}

func (stubStore) ArchiveSession(ctx context.Context, sessionID string, nowMs int64) (storage.SessionRow, error) {
	return storage.SessionRow{}, errors.New("not implemented")
}

func (stubStore) ListMessages(ctx context.Context, sessionID string) ([]storage.MessageRow, error) {
	return nil, errors.New("not implemented")
}

func (stubStore) CreateMessage(ctx context.Context, sessionID string, msgType string, text *string, meta *storage.MessageMeta, nowMs int64) (storage.MessageRow, error) {
	return storage.MessageRow{}, errors.New("not implemented")
}

func TestHealthz(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(logger, stubStore{}, ws.NewManager(logger))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
}

func TestReadyz_NotReady(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(logger, stubStore{readyErr: errors.New("db down")}, ws.NewManager(logger))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestWebSocketBroadcast_HTTPActions(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	handler := NewHandler(logger, store, ws.NewManager(logger))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer c.Close()

	httpClient := srv.Client()

	createSessionRes, err := httpClient.Post(srv.URL+"/v1/sessions", "application/json", strings.NewReader(`{"peerName":"Student_8821","peerIdentity":"anonymous"}`))
	if err != nil {
		t.Fatalf("POST /v1/sessions error = %v", err)
	}
	defer createSessionRes.Body.Close()
	if createSessionRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions status = %d, want %d", createSessionRes.StatusCode, http.StatusOK)
	}

	var createSessionBody struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(createSessionRes.Body).Decode(&createSessionBody); err != nil {
		t.Fatalf("decode create session response error = %v", err)
	}
	sessionID := createSessionBody.Session.ID
	if sessionID == "" {
		t.Fatalf("expected session.id to be non-empty")
	}

	env := readWSEvent(t, c)
	if env.Type != "session.created" {
		t.Fatalf("ws event type = %q, want %q", env.Type, "session.created")
	}
	if env.SessionID != sessionID {
		t.Fatalf("ws event sessionId = %q, want %q", env.SessionID, sessionID)
	}

	msgURL := fmt.Sprintf("%s/v1/sessions/%s/messages", srv.URL, sessionID)
	createMsgRes, err := httpClient.Post(msgURL, "application/json", strings.NewReader(`{"type":"text","text":"你好"}`))
	if err != nil {
		t.Fatalf("POST /v1/sessions/{id}/messages error = %v", err)
	}
	defer createMsgRes.Body.Close()
	if createMsgRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions/{id}/messages status = %d, want %d", createMsgRes.StatusCode, http.StatusOK)
	}

	env = readWSEvent(t, c)
	if env.Type != "message.created" {
		t.Fatalf("ws event type = %q, want %q", env.Type, "message.created")
	}
	if env.SessionID != sessionID {
		t.Fatalf("ws event sessionId = %q, want %q", env.SessionID, sessionID)
	}

	archiveURL := fmt.Sprintf("%s/v1/sessions/%s/archive", srv.URL, sessionID)
	archiveReq, err := http.NewRequest(http.MethodPost, archiveURL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	archiveRes, err := httpClient.Do(archiveReq)
	if err != nil {
		t.Fatalf("POST /v1/sessions/{id}/archive error = %v", err)
	}
	defer archiveRes.Body.Close()
	if archiveRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions/{id}/archive status = %d, want %d", archiveRes.StatusCode, http.StatusOK)
	}

	env = readWSEvent(t, c)
	if env.Type != "session.archived" {
		t.Fatalf("ws event type = %q, want %q", env.Type, "session.archived")
	}
	if env.SessionID != sessionID {
		t.Fatalf("ws event sessionId = %q, want %q", env.SessionID, sessionID)
	}
}

type wsEventEnvelope struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Payload   json.RawMessage `json:"payload"`
}

func readWSEvent(t *testing.T, c *websocket.Conn) wsEventEnvelope {
	t.Helper()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ws ReadMessage() error = %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("ws msgType = %d, want %d", msgType, websocket.TextMessage)
	}

	var env wsEventEnvelope
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if env.Type == "" {
		t.Fatalf("expected ws event type to be non-empty")
	}
	if env.SessionID == "" {
		t.Fatalf("expected ws event sessionId to be non-empty")
	}
	return env
}

func TestSessionsAndMessages_HappyPath_ArchiveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	handler := NewHandler(logger, store, ws.NewManager(logger))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := srv.Client()

	createSessionRes, err := client.Post(srv.URL+"/v1/sessions", "application/json", strings.NewReader(`{"peerName":"Student_8821","peerIdentity":"anonymous"}`))
	if err != nil {
		t.Fatalf("POST /v1/sessions error = %v", err)
	}
	defer createSessionRes.Body.Close()
	if createSessionRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions status = %d, want %d", createSessionRes.StatusCode, http.StatusOK)
	}

	var createSessionBody struct {
		Session struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			PeerName     string `json:"peerName"`
			PeerIdentity string `json:"peerIdentity"`
			UpdatedAtMs  int64  `json:"updatedAtMs"`
		} `json:"session"`
	}
	if err := json.NewDecoder(createSessionRes.Body).Decode(&createSessionBody); err != nil {
		t.Fatalf("decode create session response error = %v", err)
	}
	sessionID := createSessionBody.Session.ID
	if sessionID == "" {
		t.Fatalf("expected session.id to be non-empty")
	}
	if createSessionBody.Session.Status != storage.SessionStatusActive {
		t.Fatalf("session.status = %q, want %q", createSessionBody.Session.Status, storage.SessionStatusActive)
	}

	msgURL := fmt.Sprintf("%s/v1/sessions/%s/messages", srv.URL, sessionID)
	createMsgRes, err := client.Post(msgURL, "application/json", strings.NewReader(`{"type":"text","text":"你好"}`))
	if err != nil {
		t.Fatalf("POST /v1/sessions/{id}/messages error = %v", err)
	}
	defer createMsgRes.Body.Close()
	if createMsgRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions/{id}/messages status = %d, want %d", createMsgRes.StatusCode, http.StatusOK)
	}

	var createMsgBody struct {
		Message struct {
			ID          string `json:"id"`
			SessionID   string `json:"sessionId"`
			Sender      string `json:"sender"`
			Type        string `json:"type"`
			Text        string `json:"text"`
			CreatedAtMs int64  `json:"createdAtMs"`
		} `json:"message"`
	}
	if err := json.NewDecoder(createMsgRes.Body).Decode(&createMsgBody); err != nil {
		t.Fatalf("decode create message response error = %v", err)
	}
	if createMsgBody.Message.ID == "" {
		t.Fatalf("expected message.id to be non-empty")
	}
	if createMsgBody.Message.SessionID != sessionID {
		t.Fatalf("message.sessionId = %q, want %q", createMsgBody.Message.SessionID, sessionID)
	}
	if createMsgBody.Message.Sender != storage.MessageSenderMe {
		t.Fatalf("message.sender = %q, want %q", createMsgBody.Message.Sender, storage.MessageSenderMe)
	}
	if createMsgBody.Message.Text != "你好" {
		t.Fatalf("message.text = %q, want %q", createMsgBody.Message.Text, "你好")
	}

	listActiveRes, err := client.Get(srv.URL + "/v1/sessions?status=active")
	if err != nil {
		t.Fatalf("GET /v1/sessions?status=active error = %v", err)
	}
	defer listActiveRes.Body.Close()
	if listActiveRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/sessions?status=active status = %d, want %d", listActiveRes.StatusCode, http.StatusOK)
	}

	var listActiveBody struct {
		Sessions []struct {
			ID              string  `json:"id"`
			Status          string  `json:"status"`
			LastMessageText *string `json:"lastMessageText,omitempty"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(listActiveRes.Body).Decode(&listActiveBody); err != nil {
		t.Fatalf("decode list sessions response error = %v", err)
	}
	if len(listActiveBody.Sessions) != 1 {
		t.Fatalf("sessions length = %d, want %d", len(listActiveBody.Sessions), 1)
	}
	if listActiveBody.Sessions[0].ID != sessionID {
		t.Fatalf("sessions[0].id = %q, want %q", listActiveBody.Sessions[0].ID, sessionID)
	}
	if listActiveBody.Sessions[0].LastMessageText == nil || *listActiveBody.Sessions[0].LastMessageText != "你好" {
		t.Fatalf("sessions[0].lastMessageText = %v, want %q", listActiveBody.Sessions[0].LastMessageText, "你好")
	}

	archiveURL := fmt.Sprintf("%s/v1/sessions/%s/archive", srv.URL, sessionID)
	archiveReq, err := http.NewRequest(http.MethodPost, archiveURL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	archiveRes, err := client.Do(archiveReq)
	if err != nil {
		t.Fatalf("POST /v1/sessions/{id}/archive error = %v", err)
	}
	defer archiveRes.Body.Close()
	if archiveRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions/{id}/archive status = %d, want %d", archiveRes.StatusCode, http.StatusOK)
	}

	var archiveBody struct {
		Session struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			UpdatedAtMs int64  `json:"updatedAtMs"`
		} `json:"session"`
	}
	if err := json.NewDecoder(archiveRes.Body).Decode(&archiveBody); err != nil {
		t.Fatalf("decode archive response error = %v", err)
	}
	if archiveBody.Session.ID != sessionID {
		t.Fatalf("archive session.id = %q, want %q", archiveBody.Session.ID, sessionID)
	}
	if archiveBody.Session.Status != storage.SessionStatusArchived {
		t.Fatalf("archive session.status = %q, want %q", archiveBody.Session.Status, storage.SessionStatusArchived)
	}

	archiveReq2, err := http.NewRequest(http.MethodPost, archiveURL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	archiveRes2, err := client.Do(archiveReq2)
	if err != nil {
		t.Fatalf("POST /v1/sessions/{id}/archive (2nd) error = %v", err)
	}
	defer archiveRes2.Body.Close()
	if archiveRes2.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions/{id}/archive (2nd) status = %d, want %d", archiveRes2.StatusCode, http.StatusOK)
	}

	var archiveBody2 struct {
		Session struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			UpdatedAtMs int64  `json:"updatedAtMs"`
		} `json:"session"`
	}
	if err := json.NewDecoder(archiveRes2.Body).Decode(&archiveBody2); err != nil {
		t.Fatalf("decode archive response (2nd) error = %v", err)
	}
	if archiveBody2.Session.UpdatedAtMs != archiveBody.Session.UpdatedAtMs {
		t.Fatalf("archive updatedAtMs changed: first=%d second=%d", archiveBody.Session.UpdatedAtMs, archiveBody2.Session.UpdatedAtMs)
	}

	listArchivedRes, err := client.Get(srv.URL + "/v1/sessions?status=archived")
	if err != nil {
		t.Fatalf("GET /v1/sessions?status=archived error = %v", err)
	}
	defer listArchivedRes.Body.Close()
	if listArchivedRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/sessions?status=archived status = %d, want %d", listArchivedRes.StatusCode, http.StatusOK)
	}

	var listArchivedBody struct {
		Sessions []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(listArchivedRes.Body).Decode(&listArchivedBody); err != nil {
		t.Fatalf("decode list archived sessions response error = %v", err)
	}
	if len(listArchivedBody.Sessions) != 1 {
		t.Fatalf("archived sessions length = %d, want %d", len(listArchivedBody.Sessions), 1)
	}
	if listArchivedBody.Sessions[0].ID != sessionID {
		t.Fatalf("archived sessions[0].id = %q, want %q", listArchivedBody.Sessions[0].ID, sessionID)
	}
	if listArchivedBody.Sessions[0].Status != storage.SessionStatusArchived {
		t.Fatalf("archived sessions[0].status = %q, want %q", listArchivedBody.Sessions[0].Status, storage.SessionStatusArchived)
	}

	listMessagesRes, err := client.Get(msgURL)
	if err != nil {
		t.Fatalf("GET /v1/sessions/{id}/messages error = %v", err)
	}
	defer listMessagesRes.Body.Close()
	if listMessagesRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/sessions/{id}/messages status = %d, want %d", listMessagesRes.StatusCode, http.StatusOK)
	}

	var listMessagesBody struct {
		Messages []struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
			Text      string `json:"text"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(listMessagesRes.Body).Decode(&listMessagesBody); err != nil {
		t.Fatalf("decode list messages response error = %v", err)
	}
	if len(listMessagesBody.Messages) != 1 {
		t.Fatalf("messages length = %d, want %d", len(listMessagesBody.Messages), 1)
	}
	if listMessagesBody.Messages[0].SessionID != sessionID {
		t.Fatalf("messages[0].sessionId = %q, want %q", listMessagesBody.Messages[0].SessionID, sessionID)
	}
	if listMessagesBody.Messages[0].Text != "你好" {
		t.Fatalf("messages[0].text = %q, want %q", listMessagesBody.Messages[0].Text, "你好")
	}
}
