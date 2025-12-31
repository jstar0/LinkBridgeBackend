package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type tokenMapValidator struct {
	tokenToUserID map[string]string
}

func (v tokenMapValidator) ValidateToken(ctx context.Context, token string) (string, error) {
	userID, ok := v.tokenToUserID[token]
	if !ok {
		return "", errors.New("invalid token")
	}
	return userID, nil
}

func TestHealthz(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	ctx := context.Background()
	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	wsManager := ws.NewManager(logger, tokenMapValidator{tokenToUserID: map[string]string{}})
	handler := NewHandler(logger, store, wsManager, "", HandlerOptions{})
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

type readyErrStore struct {
	Store
	readyErr error
}

func (s readyErrStore) Ready(ctx context.Context) error {
	return s.readyErr
}

func TestReadyz_NotReady(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	ctx := context.Background()
	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	wsManager := ws.NewManager(logger, tokenMapValidator{tokenToUserID: map[string]string{}})
	handler := NewHandler(logger, readyErrStore{Store: store, readyErr: errors.New("db down")}, wsManager, "", HandlerOptions{})
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
	if env.SessionID == "" && strings.HasPrefix(env.Type, "session.") {
		t.Fatalf("expected ws event sessionId to be non-empty for %q", env.Type)
	}
	return env
}

func postJSON(t *testing.T, client *http.Client, url string, body any, token string) *http.Response {
	t.Helper()

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do error = %v", err)
	}
	return res
}

func TestWebSocketBroadcast_SessionsAndMessages(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	ctx := context.Background()
	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	tokenToUserID := map[string]string{}
	wsManager := ws.NewManager(logger, tokenMapValidator{tokenToUserID: tokenToUserID})
	handler := NewHandler(logger, store, wsManager, "", HandlerOptions{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := srv.Client()

	register := func(username string) (userID string, token string) {
		res := postJSON(t, client, srv.URL+"/v1/auth/register", map[string]any{
			"username":    username,
			"password":    "P@ssw0rd1",
			"displayName": username,
		}, "")
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("register status = %d, want %d, body=%s", res.StatusCode, http.StatusOK, string(b))
		}
		var body struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Token string `json:"token"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("decode register response error = %v", err)
		}
		if body.User.ID == "" || body.Token == "" {
			t.Fatalf("expected non-empty user.id and token")
		}
		tokenToUserID[body.Token] = body.User.ID
		return body.User.ID, body.Token
	}

	user1ID, token1 := register("alice")
	user2ID, _ := register("bobby")

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws?token=" + token1
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer c.Close()

	createSessionRes := postJSON(t, client, srv.URL+"/v1/sessions", map[string]any{
		"peerUserId": user2ID,
	}, token1)
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

	createMsgRes := postJSON(t, client, srv.URL+"/v1/sessions/"+sessionID+"/messages", map[string]any{
		"type": "text",
		"text": "hello",
	}, token1)
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

	archiveReq, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/sessions/"+sessionID+"/archive", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	archiveReq.Header.Set("Authorization", "Bearer "+token1)

	archiveRes, err := client.Do(archiveReq)
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

	_ = user1ID
}
