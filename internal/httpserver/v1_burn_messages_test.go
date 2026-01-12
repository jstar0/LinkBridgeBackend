package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

func TestBurnMessages_CreateReadExpire_Smoke(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	ctx := context.Background()
	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	tokenToUserID := map[string]string{}
	wsManager := ws.NewManager(logger, tokenMapValidator{tokenToUserID: tokenToUserID}, noopCallStore{})
	handler := NewHandler(logger, store, wsManager, "", HandlerOptions{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := srv.Client()

	register := func(username string) (userID, token string) {
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
		tokenToUserID[body.Token] = body.User.ID
		return body.User.ID, body.Token
	}

	aliceID, aliceToken := register("alice")
	bobID, bobToken := register("bobby")

	// Create session.
	createSessionRes := postJSON(t, client, srv.URL+"/v1/sessions", map[string]any{
		"peerUserId": bobID,
	}, aliceToken)
	defer createSessionRes.Body.Close()
	if createSessionRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createSessionRes.Body)
		t.Fatalf("POST /v1/sessions status = %d, want %d, body=%s", createSessionRes.StatusCode, http.StatusOK, string(b))
	}
	var createdSession struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(createSessionRes.Body).Decode(&createdSession); err != nil {
		t.Fatalf("decode create session response error = %v", err)
	}
	if createdSession.Session.ID == "" {
		t.Fatalf("expected non-empty session.id")
	}

	// Create burn message from Alice -> Bob.
	createMsgRes := postJSON(t, client, srv.URL+"/v1/sessions/"+createdSession.Session.ID+"/messages", map[string]any{
		"type":        "burn",
		"burnAfterMs": int64(1000),
		"metaJson": map[string]any{
			"ciphertext": "abc",
		},
	}, aliceToken)
	defer createMsgRes.Body.Close()
	if createMsgRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createMsgRes.Body)
		t.Fatalf("POST burn message status = %d, want %d, body=%s", createMsgRes.StatusCode, http.StatusOK, string(b))
	}
	var createdMessage struct {
		Message struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"message"`
	}
	if err := json.NewDecoder(createMsgRes.Body).Decode(&createdMessage); err != nil {
		t.Fatalf("decode create message response error = %v", err)
	}
	if createdMessage.Message.ID == "" || createdMessage.Message.Type != "burn" {
		t.Fatalf("message.id/type = %q/%q, want non-empty/burn", createdMessage.Message.ID, createdMessage.Message.Type)
	}

	// Sender cannot mark as read.
	senderReadRes := postJSON(t, client, srv.URL+"/v1/burn-messages/"+createdMessage.Message.ID+"/read", map[string]any{}, aliceToken)
	defer senderReadRes.Body.Close()
	if senderReadRes.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(senderReadRes.Body)
		t.Fatalf("sender read status = %d, want %d, body=%s", senderReadRes.StatusCode, http.StatusForbidden, string(b))
	}

	// Receiver reads: starts timer.
	readRes := postJSON(t, client, srv.URL+"/v1/burn-messages/"+createdMessage.Message.ID+"/read", map[string]any{}, bobToken)
	defer readRes.Body.Close()
	if readRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(readRes.Body)
		t.Fatalf("receiver read status = %d, want %d, body=%s", readRes.StatusCode, http.StatusOK, string(b))
	}
	var readBody struct {
		Burn struct {
			OpenedAtMs *int64 `json:"openedAtMs"`
			BurnAtMs   *int64 `json:"burnAtMs"`
		} `json:"burn"`
		Started bool `json:"started"`
	}
	if err := json.NewDecoder(readRes.Body).Decode(&readBody); err != nil {
		t.Fatalf("decode read response error = %v", err)
	}
	if !readBody.Started || readBody.Burn.OpenedAtMs == nil || readBody.Burn.BurnAtMs == nil {
		t.Fatalf("expected started=true and openedAtMs/burnAtMs to be set")
	}

	// Force-expire without sleeping.
	if _, err := store.ExpireBurnMessages(ctx, *readBody.Burn.BurnAtMs+1, 200); err != nil {
		t.Fatalf("ExpireBurnMessages() error = %v", err)
	}

	// Message should be gone from history for both users.
	listRes := get(t, client, srv.URL+"/v1/sessions/"+createdSession.Session.ID+"/messages", bobToken)
	defer listRes.Body.Close()
	if listRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(listRes.Body)
		t.Fatalf("list messages status = %d, want %d, body=%s", listRes.StatusCode, http.StatusOK, string(b))
	}
	var listBody struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(listRes.Body).Decode(&listBody); err != nil {
		t.Fatalf("decode list response error = %v", err)
	}
	for _, m := range listBody.Messages {
		if m.ID == createdMessage.Message.ID {
			t.Fatalf("expected burn message to be deleted from history")
		}
	}

	_ = aliceID // silence unused (future: may assert events)
}

func TestBurnMessages_NewDevice_NoHistoryForBurn(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	ctx := context.Background()
	store, err := storage.Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	tokenToUserID := map[string]string{}
	wsManager := ws.NewManager(logger, tokenMapValidator{tokenToUserID: tokenToUserID}, noopCallStore{})
	handler := NewHandler(logger, store, wsManager, "", HandlerOptions{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := srv.Client()

	register := func(username string) (userID, token string) {
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
		tokenToUserID[body.Token] = body.User.ID
		return body.User.ID, body.Token
	}

	_, aliceToken := register("alice")
	_, bobTokenOld := register("bobby")

	// Resolve Bob userID via /v1/auth/me would work, but keep it explicit:
	meRes := get(t, client, srv.URL+"/v1/auth/me", bobTokenOld)
	defer meRes.Body.Close()
	if meRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(meRes.Body)
		t.Fatalf("GET /v1/auth/me status = %d, want %d, body=%s", meRes.StatusCode, http.StatusOK, string(b))
	}
	var meBody struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(meRes.Body).Decode(&meBody); err != nil {
		t.Fatalf("decode me response error = %v", err)
	}
	bobID := meBody.User.ID
	if bobID == "" {
		t.Fatalf("expected non-empty bob user ID")
	}

	// Create session.
	createSessionRes := postJSON(t, client, srv.URL+"/v1/sessions", map[string]any{
		"peerUserId": bobID,
	}, aliceToken)
	defer createSessionRes.Body.Close()
	if createSessionRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createSessionRes.Body)
		t.Fatalf("POST /v1/sessions status = %d, want %d, body=%s", createSessionRes.StatusCode, http.StatusOK, string(b))
	}
	var createdSession struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(createSessionRes.Body).Decode(&createdSession); err != nil {
		t.Fatalf("decode create session response error = %v", err)
	}
	if createdSession.Session.ID == "" {
		t.Fatalf("expected non-empty session.id")
	}

	// Create a normal message + a burn message before the "new device" logs in.
	textRes := postJSON(t, client, srv.URL+"/v1/sessions/"+createdSession.Session.ID+"/messages", map[string]any{
		"type": "text",
		"text": "hello",
	}, aliceToken)
	defer textRes.Body.Close()
	if textRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(textRes.Body)
		t.Fatalf("POST text message status = %d, want %d, body=%s", textRes.StatusCode, http.StatusOK, string(b))
	}

	burnRes := postJSON(t, client, srv.URL+"/v1/sessions/"+createdSession.Session.ID+"/messages", map[string]any{
		"type":        "burn",
		"burnAfterMs": int64(1000),
		"metaJson": map[string]any{
			"ciphertext": "abc",
		},
	}, aliceToken)
	defer burnRes.Body.Close()
	if burnRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(burnRes.Body)
		t.Fatalf("POST burn message status = %d, want %d, body=%s", burnRes.StatusCode, http.StatusOK, string(b))
	}

	// Ensure tokenCreatedAtMs is after the burn message (avoid same-ms flake).
	time.Sleep(2 * time.Millisecond)

	// New device token (login again).
	loginRes := postJSON(t, client, srv.URL+"/v1/auth/login", map[string]any{
		"username": "bobby",
		"password": "P@ssw0rd1",
	}, "")
	defer loginRes.Body.Close()
	if loginRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(loginRes.Body)
		t.Fatalf("login status = %d, want %d, body=%s", loginRes.StatusCode, http.StatusOK, string(b))
	}
	var loginBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginRes.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login response error = %v", err)
	}
	bobTokenNew := loginBody.Token
	if bobTokenNew == "" {
		t.Fatalf("expected non-empty new token")
	}

	// New device should see normal messages, but NOT burn messages.
	listRes := get(t, client, srv.URL+"/v1/sessions/"+createdSession.Session.ID+"/messages", bobTokenNew)
	defer listRes.Body.Close()
	if listRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(listRes.Body)
		t.Fatalf("list messages status = %d, want %d, body=%s", listRes.StatusCode, http.StatusOK, string(b))
	}
	var listBody struct {
		Messages []struct {
			Type string `json:"type"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(listRes.Body).Decode(&listBody); err != nil {
		t.Fatalf("decode list response error = %v", err)
	}

	var hasText, hasBurn bool
	for _, m := range listBody.Messages {
		if m.Type == "text" {
			hasText = true
		}
		if m.Type == "burn" {
			hasBurn = true
		}
	}
	if !hasText {
		t.Fatalf("expected new device to still see non-E2EE text messages")
	}
	if hasBurn {
		t.Fatalf("expected new device to NOT see historical burn messages (Option A)")
	}
}
