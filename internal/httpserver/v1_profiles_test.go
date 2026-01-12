package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

func TestProfiles_CardAndMap_PatchSemantics(t *testing.T) {
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

	registerRes := postJSON(t, client, srv.URL+"/v1/auth/register", map[string]any{
		"username":    "alice",
		"password":    "P@ssw0rd1",
		"displayName": "Alice",
	}, "")
	defer registerRes.Body.Close()
	if registerRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(registerRes.Body)
		t.Fatalf("register status = %d, want %d, body=%s", registerRes.StatusCode, http.StatusOK, string(b))
	}
	var registerBody struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(registerRes.Body).Decode(&registerBody); err != nil {
		t.Fatalf("decode register response error = %v", err)
	}
	tokenToUserID[registerBody.Token] = registerBody.User.ID

	// Initial GET: no overrides, resolved nickname should equal core displayName.
	getCardRes := get(t, client, srv.URL+"/v1/profiles/card", registerBody.Token)
	defer getCardRes.Body.Close()
	if getCardRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getCardRes.Body)
		t.Fatalf("GET /v1/profiles/card status = %d, want %d, body=%s", getCardRes.StatusCode, http.StatusOK, string(b))
	}
	var card1 struct {
		Core struct {
			DisplayName string  `json:"displayName"`
			AvatarURL   *string `json:"avatarUrl"`
		} `json:"core"`
		Profile struct {
			Nickname         string  `json:"nickname"`
			NicknameOverride *string `json:"nicknameOverride"`
			AvatarURL        *string `json:"avatarUrl"`
			AvatarOverride   *string `json:"avatarUrlOverride"`
		} `json:"profile"`
	}
	if err := json.NewDecoder(getCardRes.Body).Decode(&card1); err != nil {
		t.Fatalf("decode card profile error = %v", err)
	}
	if card1.Core.DisplayName != "Alice" {
		t.Fatalf("core.displayName = %q, want %q", card1.Core.DisplayName, "Alice")
	}
	if card1.Profile.Nickname != "Alice" {
		t.Fatalf("profile.nickname = %q, want %q", card1.Profile.Nickname, "Alice")
	}
	if card1.Profile.NicknameOverride != nil {
		t.Fatalf("profile.nicknameOverride expected nil")
	}
	if card1.Profile.AvatarURL != nil || card1.Core.AvatarURL != nil {
		t.Fatalf("expected initial avatarUrl to be nil")
	}

	// Set card nickname + fields.
	putCardRes := putJSON(t, client, srv.URL+"/v1/profiles/card", map[string]any{
		"nicknameOverride": "CardNick",
		"fields": map[string]any{
			"bio": "x",
		},
	}, registerBody.Token)
	defer putCardRes.Body.Close()
	if putCardRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putCardRes.Body)
		t.Fatalf("PUT /v1/profiles/card status = %d, want %d, body=%s", putCardRes.StatusCode, http.StatusOK, string(b))
	}

	// Update core avatar via /v1/users/me
	putMeRes := putJSON(t, client, srv.URL+"/v1/users/me", map[string]any{
		"avatarUrl": "/uploads/core.png",
	}, registerBody.Token)
	defer putMeRes.Body.Close()
	if putMeRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putMeRes.Body)
		t.Fatalf("PUT /v1/users/me status = %d, want %d, body=%s", putMeRes.StatusCode, http.StatusOK, string(b))
	}

	// Card should resolve avatar from core.
	getCard2Res := get(t, client, srv.URL+"/v1/profiles/card", registerBody.Token)
	defer getCard2Res.Body.Close()
	if getCard2Res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getCard2Res.Body)
		t.Fatalf("GET /v1/profiles/card (2) status = %d, want %d, body=%s", getCard2Res.StatusCode, http.StatusOK, string(b))
	}
	var card2 struct {
		Profile struct {
			Nickname         string  `json:"nickname"`
			AvatarURL        *string `json:"avatarUrl"`
			NicknameOverride *string `json:"nicknameOverride"`
			AvatarOverride   *string `json:"avatarUrlOverride"`
		} `json:"profile"`
	}
	if err := json.NewDecoder(getCard2Res.Body).Decode(&card2); err != nil {
		t.Fatalf("decode card profile (2) error = %v", err)
	}
	if card2.Profile.Nickname != "CardNick" {
		t.Fatalf("profile.nickname = %q, want %q", card2.Profile.Nickname, "CardNick")
	}
	if card2.Profile.AvatarURL == nil || *card2.Profile.AvatarURL != "/uploads/core.png" {
		t.Fatalf("profile.avatarUrl = %v, want %q", card2.Profile.AvatarURL, "/uploads/core.png")
	}

	// Set card avatar override; then resolved avatar should use override.
	putCard3Res := putJSON(t, client, srv.URL+"/v1/profiles/card", map[string]any{
		"avatarUrlOverride": "/uploads/card.png",
	}, registerBody.Token)
	defer putCard3Res.Body.Close()
	if putCard3Res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putCard3Res.Body)
		t.Fatalf("PUT /v1/profiles/card (3) status = %d, want %d, body=%s", putCard3Res.StatusCode, http.StatusOK, string(b))
	}

	getCard3Res := get(t, client, srv.URL+"/v1/profiles/card", registerBody.Token)
	defer getCard3Res.Body.Close()
	var card3 struct {
		Profile struct {
			Nickname  string  `json:"nickname"`
			AvatarURL *string `json:"avatarUrl"`
		} `json:"profile"`
	}
	if err := json.NewDecoder(getCard3Res.Body).Decode(&card3); err != nil {
		t.Fatalf("decode card profile (3) error = %v", err)
	}
	if card3.Profile.AvatarURL == nil || *card3.Profile.AvatarURL != "/uploads/card.png" {
		t.Fatalf("profile.avatarUrl = %v, want %q", card3.Profile.AvatarURL, "/uploads/card.png")
	}

	// Patch fields only; nickname/avatar overrides should remain unchanged.
	putCard4Res := putJSON(t, client, srv.URL+"/v1/profiles/card", map[string]any{
		"fields": map[string]any{
			"bio": "y",
		},
	}, registerBody.Token)
	defer putCard4Res.Body.Close()
	if putCard4Res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putCard4Res.Body)
		t.Fatalf("PUT /v1/profiles/card (4) status = %d, want %d, body=%s", putCard4Res.StatusCode, http.StatusOK, string(b))
	}

	// Clear nickname override.
	putCard5Res := putJSON(t, client, srv.URL+"/v1/profiles/card", map[string]any{
		"nicknameOverride": "",
	}, registerBody.Token)
	defer putCard5Res.Body.Close()
	if putCard5Res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putCard5Res.Body)
		t.Fatalf("PUT /v1/profiles/card (5) status = %d, want %d, body=%s", putCard5Res.StatusCode, http.StatusOK, string(b))
	}

	getCard5Res := get(t, client, srv.URL+"/v1/profiles/card", registerBody.Token)
	defer getCard5Res.Body.Close()
	var card5 struct {
		Core struct {
			DisplayName string `json:"displayName"`
		} `json:"core"`
		Profile struct {
			Nickname         string  `json:"nickname"`
			NicknameOverride *string `json:"nicknameOverride"`
		} `json:"profile"`
	}
	if err := json.NewDecoder(getCard5Res.Body).Decode(&card5); err != nil {
		t.Fatalf("decode card profile (5) error = %v", err)
	}
	if card5.Profile.Nickname != card5.Core.DisplayName {
		t.Fatalf("profile.nickname = %q, want core displayName %q", card5.Profile.Nickname, card5.Core.DisplayName)
	}
	if card5.Profile.NicknameOverride != nil {
		t.Fatalf("profile.nicknameOverride expected nil after clear")
	}

	// Map profile is separate: set map nickname override should not affect card nickname.
	putMapRes := putJSON(t, client, srv.URL+"/v1/profiles/map", map[string]any{
		"nicknameOverride": "MapNick",
	}, registerBody.Token)
	defer putMapRes.Body.Close()
	if putMapRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putMapRes.Body)
		t.Fatalf("PUT /v1/profiles/map status = %d, want %d, body=%s", putMapRes.StatusCode, http.StatusOK, string(b))
	}

	getMapRes := get(t, client, srv.URL+"/v1/profiles/map", registerBody.Token)
	defer getMapRes.Body.Close()
	var mapProfile struct {
		Profile struct {
			Nickname string `json:"nickname"`
		} `json:"profile"`
	}
	if err := json.NewDecoder(getMapRes.Body).Decode(&mapProfile); err != nil {
		t.Fatalf("decode map profile error = %v", err)
	}
	if mapProfile.Profile.Nickname != "MapNick" {
		t.Fatalf("map profile.nickname = %q, want %q", mapProfile.Profile.Nickname, "MapNick")
	}
}
