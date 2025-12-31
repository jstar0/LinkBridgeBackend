package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
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

type staticCallStore struct{}

func (staticCallStore) GetCallByID(ctx context.Context, callID string) (callerID, calleeID, status string, err error) {
	return "", "", "", errors.New("not found")
}

func TestManager_Broadcast(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	manager := NewManager(logger, staticValidator{}, staticCallStore{})

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

// Audio frame relay tests

type mockTokenValidator struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (m *mockTokenValidator) ValidateToken(_ context.Context, token string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if userID, ok := m.tokens[token]; ok {
		return userID, nil
	}
	return "", errors.New("invalid token")
}

type mockCallStore struct {
	mu    sync.Mutex
	calls map[string]struct {
		callerID string
		calleeID string
		status   string
	}
}

func (m *mockCallStore) GetCallByID(_ context.Context, callID string) (callerID, calleeID, status string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if call, ok := m.calls[callID]; ok {
		return call.callerID, call.calleeID, call.status, nil
	}
	return "", "", "", errors.New("call not found")
}

func (m *mockCallStore) SetCall(callID, callerID, calleeID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls == nil {
		m.calls = make(map[string]struct {
			callerID string
			calleeID string
			status   string
		})
	}
	m.calls[callID] = struct {
		callerID string
		calleeID string
		status   string
	}{callerID, calleeID, status}
}

func setupTestManager() (*Manager, *mockTokenValidator, *mockCallStore) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tv := &mockTokenValidator{tokens: make(map[string]string)}
	cs := &mockCallStore{}
	m := NewManager(logger, tv, cs)
	return m, tv, cs
}

func connectWS(t *testing.T, server *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	return conn
}

func TestAudioFrameRelay_Success(t *testing.T) {
	m, tv, cs := setupTestManager()

	tv.tokens["tokenA"] = "userA"
	tv.tokens["tokenB"] = "userB"
	cs.SetCall("call1", "userA", "userB", "accepted")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	connA := connectWS(t, server, "tokenA")
	defer connA.Close()

	connB := connectWS(t, server, "tokenB")
	defer connB.Close()

	time.Sleep(50 * time.Millisecond)

	msg := `{"type":"audio.frame","callId":"call1","data":"dGVzdGRhdGE="}`
	if err := connA.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if env.Type != "audio.frame" {
		t.Errorf("expected type audio.frame, got %s", env.Type)
	}

	payload, ok := env.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload is not map")
	}
	if payload["callId"] != "call1" {
		t.Errorf("expected callId call1, got %v", payload["callId"])
	}
	if payload["data"] != "dGVzdGRhdGE=" {
		t.Errorf("expected data dGVzdGRhdGE=, got %v", payload["data"])
	}
}

func TestAudioFrameRelay_CallNotFound(t *testing.T) {
	m, tv, _ := setupTestManager()

	tv.tokens["tokenA"] = "userA"
	tv.tokens["tokenB"] = "userB"

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	connA := connectWS(t, server, "tokenA")
	defer connA.Close()

	connB := connectWS(t, server, "tokenB")
	defer connB.Close()

	time.Sleep(50 * time.Millisecond)

	msg := `{"type":"audio.frame","callId":"nonexistent","data":"dGVzdA=="}`
	if err := connA.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	connB.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := connB.ReadMessage()
	if err == nil {
		t.Error("expected timeout, got message")
	}
}

func TestAudioFrameRelay_CallNotAccepted(t *testing.T) {
	m, tv, cs := setupTestManager()

	tv.tokens["tokenA"] = "userA"
	tv.tokens["tokenB"] = "userB"
	cs.SetCall("call1", "userA", "userB", "inviting")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	connA := connectWS(t, server, "tokenA")
	defer connA.Close()

	connB := connectWS(t, server, "tokenB")
	defer connB.Close()

	time.Sleep(50 * time.Millisecond)

	msg := `{"type":"audio.frame","callId":"call1","data":"dGVzdA=="}`
	if err := connA.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	connB.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := connB.ReadMessage()
	if err == nil {
		t.Error("expected timeout, got message")
	}
}

func TestAudioFrameRelay_NotParticipant(t *testing.T) {
	m, tv, cs := setupTestManager()

	tv.tokens["tokenA"] = "userA"
	tv.tokens["tokenB"] = "userB"
	tv.tokens["tokenC"] = "userC"
	cs.SetCall("call1", "userA", "userB", "accepted")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	connC := connectWS(t, server, "tokenC")
	defer connC.Close()

	connB := connectWS(t, server, "tokenB")
	defer connB.Close()

	time.Sleep(50 * time.Millisecond)

	msg := `{"type":"audio.frame","callId":"call1","data":"dGVzdA=="}`
	if err := connC.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	connB.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := connB.ReadMessage()
	if err == nil {
		t.Error("expected timeout, got message")
	}
}

func TestAudioFrameRelay_ReceiverOffline(t *testing.T) {
	m, tv, cs := setupTestManager()

	tv.tokens["tokenA"] = "userA"
	tv.tokens["tokenB"] = "userB"
	cs.SetCall("call1", "userA", "userB", "accepted")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	connA := connectWS(t, server, "tokenA")
	defer connA.Close()

	time.Sleep(50 * time.Millisecond)

	msg := `{"type":"audio.frame","callId":"call1","data":"dGVzdA=="}`
	if err := connA.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}

func TestAudioFrameRelay_HighFrequency(t *testing.T) {
	m, tv, cs := setupTestManager()

	tv.tokens["tokenA"] = "userA"
	tv.tokens["tokenB"] = "userB"
	cs.SetCall("call1", "userA", "userB", "accepted")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	connA := connectWS(t, server, "tokenA")
	defer connA.Close()

	connB := connectWS(t, server, "tokenB")
	defer connB.Close()

	time.Sleep(50 * time.Millisecond)

	const frameCount = 50
	var wg sync.WaitGroup
	received := make(chan struct{}, frameCount)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < frameCount; i++ {
			connB.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, err := connB.ReadMessage()
			if err != nil {
				return
			}
			received <- struct{}{}
		}
	}()

	for i := 0; i < frameCount; i++ {
		msg := `{"type":"audio.frame","callId":"call1","data":"dGVzdA=="}`
		if err := connA.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("write failed at frame %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()
	close(received)

	count := 0
	for range received {
		count++
	}

	if count < frameCount*9/10 {
		t.Errorf("expected at least %d frames, got %d", frameCount*9/10, count)
	}
}
