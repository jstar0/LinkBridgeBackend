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

func TestActivities_CreateJoinMembersRemove_Smoke(t *testing.T) {
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

	creatorID, creatorToken := register("creator")
	memberID, memberToken := register("member")

	endAtMs := time.Now().Add(2 * time.Hour).UnixMilli()
	createRes := postJSON(t, client, srv.URL+"/v1/activities", map[string]any{
		"title":   "Test Activity",
		"endAtMs": endAtMs,
	}, creatorToken)
	defer createRes.Body.Close()
	if createRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createRes.Body)
		t.Fatalf("POST /v1/activities status = %d, want %d, body=%s", createRes.StatusCode, http.StatusOK, string(b))
	}
	var created struct {
		Activity struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
			CreatorID string `json:"creatorId"`
		} `json:"activity"`
		InviteCode string `json:"inviteCode"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode create activity response error = %v", err)
	}
	if created.Activity.ID == "" || created.Activity.SessionID == "" {
		t.Fatalf("expected activity.id and activity.sessionId to be non-empty")
	}
	if created.Activity.CreatorID != creatorID {
		t.Fatalf("activity.creatorId = %q, want %q", created.Activity.CreatorID, creatorID)
	}
	if created.InviteCode == "" {
		t.Fatalf("expected inviteCode to be non-empty")
	}

	consumeRes := postJSON(t, client, srv.URL+"/v1/activities/invites/consume", map[string]any{
		"code": created.InviteCode,
	}, memberToken)
	defer consumeRes.Body.Close()
	if consumeRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(consumeRes.Body)
		t.Fatalf("POST /v1/activities/invites/consume status = %d, want %d, body=%s", consumeRes.StatusCode, http.StatusOK, string(b))
	}
	var consumed struct {
		Joined bool `json:"joined"`
	}
	if err := json.NewDecoder(consumeRes.Body).Decode(&consumed); err != nil {
		t.Fatalf("decode consume response error = %v", err)
	}
	if !consumed.Joined {
		t.Fatalf("expected joined=true on first join")
	}

	membersRes := get(t, client, srv.URL+"/v1/activities/"+created.Activity.ID+"/members", memberToken)
	defer membersRes.Body.Close()
	if membersRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(membersRes.Body)
		t.Fatalf("GET /v1/activities/{id}/members status = %d, want %d, body=%s", membersRes.StatusCode, http.StatusOK, string(b))
	}
	var membersBody struct {
		Members []struct {
			UserID string `json:"userId"`
			Status string `json:"status"`
		} `json:"members"`
	}
	if err := json.NewDecoder(membersRes.Body).Decode(&membersBody); err != nil {
		t.Fatalf("decode members response error = %v", err)
	}
	if len(membersBody.Members) < 2 {
		t.Fatalf("members = %d, want >=2", len(membersBody.Members))
	}

	removeRes := postJSON(t, client, srv.URL+"/v1/activities/"+created.Activity.ID+"/members/"+memberID+"/remove", map[string]any{}, creatorToken)
	defer removeRes.Body.Close()
	if removeRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(removeRes.Body)
		t.Fatalf("POST remove member status = %d, want %d, body=%s", removeRes.StatusCode, http.StatusOK, string(b))
	}

	// Removed member should no longer be able to send messages to the group session.
	msgRes := postJSON(t, client, srv.URL+"/v1/sessions/"+created.Activity.SessionID+"/messages", map[string]any{
		"type": "text",
		"text": "hello",
	}, memberToken)
	defer msgRes.Body.Close()
	if msgRes.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(msgRes.Body)
		t.Fatalf("POST message after removal status = %d, want %d, body=%s", msgRes.StatusCode, http.StatusForbidden, string(b))
	}
	var errEnv struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(msgRes.Body).Decode(&errEnv)
	if errEnv.Error.Code != string(ErrCodeSessionAccessDenied) {
		t.Fatalf("error.code = %q, want %q", errEnv.Error.Code, ErrCodeSessionAccessDenied)
	}

	// Creator cannot remove themselves (access denied).
	removeCreatorRes := postJSON(t, client, srv.URL+"/v1/activities/"+created.Activity.ID+"/members/"+creatorID+"/remove", map[string]any{}, creatorToken)
	defer removeCreatorRes.Body.Close()
	if removeCreatorRes.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(removeCreatorRes.Body)
		t.Fatalf("POST remove creator status = %d, want %d, body=%s", removeCreatorRes.StatusCode, http.StatusForbidden, string(b))
	}
}
