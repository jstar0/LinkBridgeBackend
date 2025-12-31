package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type staticValidator struct{}

func (staticValidator) ValidateToken(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", errors.New("missing token")
	}
	return "test-user", nil
}

func TestManager_Broadcast(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	manager := NewManager(logger, staticValidator{})

	srv := httptest.NewServer(manager.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?token=test"

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer c.Close()

	manager.Broadcast(Envelope{
		Type:      "session.created",
		SessionID: "test-session",
		Payload: map[string]any{
			"session": map[string]any{
				"id": "test-session",
			},
		},
	})

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("msgType = %d, want %d", msgType, websocket.TextMessage)
	}

	var env Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if env.Type != "session.created" {
		t.Fatalf("type = %q, want %q", env.Type, "session.created")
	}
	if env.SessionID != "test-session" {
		t.Fatalf("sessionId = %q, want %q", env.SessionID, "test-session")
	}
}
