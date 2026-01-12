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

func TestWeChatCode_SessionInviteSettings_ExpiryAndGeoFence(t *testing.T) {
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

	_, inviterToken := register("inviter")
	_, consumerToken := register("consumer")

	// GET /v1/wechat/code/session/invite
	getInviteRes := get(t, client, srv.URL+"/v1/wechat/code/session/invite", inviterToken)
	defer getInviteRes.Body.Close()
	if getInviteRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getInviteRes.Body)
		t.Fatalf("GET session invite status = %d, want %d, body=%s", getInviteRes.StatusCode, http.StatusOK, string(b))
	}
	var inviteBody struct {
		Invite struct {
			Code string `json:"code"`
		} `json:"invite"`
	}
	if err := json.NewDecoder(getInviteRes.Body).Decode(&inviteBody); err != nil {
		t.Fatalf("decode session invite response error = %v", err)
	}
	if inviteBody.Invite.Code == "" {
		t.Fatalf("expected non-empty invite.code")
	}

	// PUT: set a short expiry + geofence
	shortExpiry := time.Now().Add(30 * time.Millisecond).UnixMilli()
	putInviteRes := putJSON(t, client, srv.URL+"/v1/wechat/code/session/invite", map[string]any{
		"expiresAtMs": shortExpiry,
		"geoFence": map[string]any{
			"lat":     31.0,
			"lng":     121.0,
			"radiusM": 100,
		},
	}, inviterToken)
	defer putInviteRes.Body.Close()
	if putInviteRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putInviteRes.Body)
		t.Fatalf("PUT session invite status = %d, want %d, body=%s", putInviteRes.StatusCode, http.StatusOK, string(b))
	}

	// Wait until expiry passes.
	time.Sleep(60 * time.Millisecond)

	// Consumer attempts to consume: should fail with INVITE_EXPIRED.
	expiredConsumeRes := postJSON(t, client, srv.URL+"/v1/session-requests/invites/consume", map[string]any{
		"code":  inviteBody.Invite.Code,
		"atLat": 31.0,
		"atLng": 121.0,
	}, consumerToken)
	defer expiredConsumeRes.Body.Close()
	if expiredConsumeRes.StatusCode != http.StatusGone {
		b, _ := io.ReadAll(expiredConsumeRes.Body)
		t.Fatalf("consume expired invite status = %d, want %d, body=%s", expiredConsumeRes.StatusCode, http.StatusGone, string(b))
	}
	var expiredErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(expiredConsumeRes.Body).Decode(&expiredErr)
	if expiredErr.Error.Code != string(ErrCodeInviteExpired) {
		t.Fatalf("error.code = %q, want %q", expiredErr.Error.Code, ErrCodeInviteExpired)
	}

	// Extend expiry so invite becomes usable again (keep geoFence as-is via patch).
	longExpiry := time.Now().Add(24 * time.Hour).UnixMilli()
	extendRes := putJSON(t, client, srv.URL+"/v1/wechat/code/session/invite", map[string]any{
		"expiresAtMs": longExpiry,
	}, inviterToken)
	defer extendRes.Body.Close()
	if extendRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(extendRes.Body)
		t.Fatalf("extend invite status = %d, want %d, body=%s", extendRes.StatusCode, http.StatusOK, string(b))
	}

	// Missing location should be rejected when geoFence is enabled.
	missingLocRes := postJSON(t, client, srv.URL+"/v1/session-requests/invites/consume", map[string]any{
		"code": inviteBody.Invite.Code,
	}, consumerToken)
	defer missingLocRes.Body.Close()
	if missingLocRes.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(missingLocRes.Body)
		t.Fatalf("consume missing location status = %d, want %d, body=%s", missingLocRes.StatusCode, http.StatusBadRequest, string(b))
	}
	var locErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(missingLocRes.Body).Decode(&locErr)
	if locErr.Error.Code != string(ErrCodeGeoFenceRequired) {
		t.Fatalf("error.code = %q, want %q", locErr.Error.Code, ErrCodeGeoFenceRequired)
	}

	// Provide location within the fence: should succeed and create a session request.
	okConsumeRes := postJSON(t, client, srv.URL+"/v1/session-requests/invites/consume", map[string]any{
		"code":  inviteBody.Invite.Code,
		"atLat": 31.0,
		"atLng": 121.0,
	}, consumerToken)
	defer okConsumeRes.Body.Close()
	if okConsumeRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(okConsumeRes.Body)
		t.Fatalf("consume ok status = %d, want %d, body=%s", okConsumeRes.StatusCode, http.StatusOK, string(b))
	}
}

func TestWeChatCode_ActivityInviteSettings_GeoFence(t *testing.T) {
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
	_, memberToken := register("member")

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
			CreatorID string `json:"creatorId"`
		} `json:"activity"`
		InviteCode string `json:"inviteCode"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode create activity response error = %v", err)
	}
	if created.Activity.ID == "" || created.InviteCode == "" {
		t.Fatalf("expected activity.id and inviteCode to be non-empty")
	}
	if created.Activity.CreatorID != creatorID {
		t.Fatalf("activity.creatorId = %q, want %q", created.Activity.CreatorID, creatorID)
	}

	// PUT /v1/wechat/code/activity/invite?activityId=... (enable geoFence)
	putRes := putJSON(t, client, srv.URL+"/v1/wechat/code/activity/invite?activityId="+created.Activity.ID, map[string]any{
		"geoFence": map[string]any{
			"lat":     31.0,
			"lng":     121.0,
			"radiusM": 100,
		},
	}, creatorToken)
	defer putRes.Body.Close()
	if putRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putRes.Body)
		t.Fatalf("PUT activity invite status = %d, want %d, body=%s", putRes.StatusCode, http.StatusOK, string(b))
	}

	// Member consume invite without location -> GEOFENCE_REQUIRED.
	missingLocRes := postJSON(t, client, srv.URL+"/v1/activities/invites/consume", map[string]any{
		"code": created.InviteCode,
	}, memberToken)
	defer missingLocRes.Body.Close()
	if missingLocRes.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(missingLocRes.Body)
		t.Fatalf("consume missing location status = %d, want %d, body=%s", missingLocRes.StatusCode, http.StatusBadRequest, string(b))
	}
	var locErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(missingLocRes.Body).Decode(&locErr)
	if locErr.Error.Code != string(ErrCodeGeoFenceRequired) {
		t.Fatalf("error.code = %q, want %q", locErr.Error.Code, ErrCodeGeoFenceRequired)
	}

	// Consume with location within the fence -> OK.
	okRes := postJSON(t, client, srv.URL+"/v1/activities/invites/consume", map[string]any{
		"code":  created.InviteCode,
		"atLat": 31.0,
		"atLng": 121.0,
	}, memberToken)
	defer okRes.Body.Close()
	if okRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(okRes.Body)
		t.Fatalf("consume ok status = %d, want %d, body=%s", okRes.StatusCode, http.StatusOK, string(b))
	}
}
