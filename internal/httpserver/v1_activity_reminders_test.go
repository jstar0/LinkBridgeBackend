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

func TestActivities_Reminders_UpsertRequiresParticipant(t *testing.T) {
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
		tokenToUserID[body.Token] = body.User.ID
		return body.User.ID, body.Token
	}

	_, creatorToken := register("creator")
	_, outsiderToken := register("outsider")

	endAtMs := time.Now().Add(2 * time.Hour).UnixMilli()
	createRes := postJSON(t, client, srv.URL+"/v1/activities", map[string]any{
		"title":     "Test Activity",
		"startAtMs": time.Now().Add(30 * time.Minute).UnixMilli(),
		"endAtMs":   endAtMs,
	}, creatorToken)
	defer createRes.Body.Close()
	if createRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createRes.Body)
		t.Fatalf("POST /v1/activities status = %d, want %d, body=%s", createRes.StatusCode, http.StatusOK, string(b))
	}
	var created struct {
		Activity struct {
			ID string `json:"id"`
		} `json:"activity"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode create activity response error = %v", err)
	}

	remindAtMs := time.Now().Add(45 * time.Minute).UnixMilli()
	outsiderRes := postJSON(t, client, srv.URL+"/v1/activities/"+created.Activity.ID+"/reminders", map[string]any{
		"remindAtMs": remindAtMs,
	}, outsiderToken)
	defer outsiderRes.Body.Close()
	if outsiderRes.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(outsiderRes.Body)
		t.Fatalf("outsider reminder status = %d, want %d, body=%s", outsiderRes.StatusCode, http.StatusForbidden, string(b))
	}

	okRes := postJSON(t, client, srv.URL+"/v1/activities/"+created.Activity.ID+"/reminders", map[string]any{
		"remindAtMs": remindAtMs,
	}, creatorToken)
	defer okRes.Body.Close()
	if okRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(okRes.Body)
		t.Fatalf("creator reminder status = %d, want %d, body=%s", okRes.StatusCode, http.StatusOK, string(b))
	}
	var okBody struct {
		Reminder struct {
			ActivityID string `json:"activityId"`
			Status     string `json:"status"`
			RemindAtMs int64  `json:"remindAtMs"`
		} `json:"reminder"`
	}
	if err := json.NewDecoder(okRes.Body).Decode(&okBody); err != nil {
		t.Fatalf("decode reminder response error = %v", err)
	}
	if okBody.Reminder.ActivityID == "" || okBody.Reminder.RemindAtMs == 0 {
		t.Fatalf("expected reminder fields to be set")
	}
	if okBody.Reminder.Status != storage.ActivityReminderStatusPending {
		t.Fatalf("status = %q, want %q", okBody.Reminder.Status, storage.ActivityReminderStatusPending)
	}
}
