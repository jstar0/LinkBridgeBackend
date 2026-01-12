package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

func putJSON(t *testing.T, client *http.Client, url string, body any, token string) *http.Response {
	t.Helper()

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(b))
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

func get(t *testing.T, client *http.Client, url string, token string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do error = %v", err)
	}
	return res
}

func TestHomeBaseAndLocalFeedEndpoints_Smoke(t *testing.T) {
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

	res := postJSON(t, client, srv.URL+"/v1/auth/register", map[string]any{
		"username":    "alice",
		"password":    "P@ssw0rd1",
		"displayName": "Alice",
	}, "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register status = %d, want %d, body=%s", res.StatusCode, http.StatusOK, string(b))
	}
	var registerBody struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&registerBody); err != nil {
		t.Fatalf("decode register response error = %v", err)
	}
	tokenToUserID[registerBody.Token] = registerBody.User.ID

	// PUT /v1/home-base
	putRes := putJSON(t, client, srv.URL+"/v1/home-base", map[string]any{
		"lat": 31.0,
		"lng": 121.0,
	}, registerBody.Token)
	defer putRes.Body.Close()
	if putRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putRes.Body)
		t.Fatalf("PUT /v1/home-base status = %d, want %d, body=%s", putRes.StatusCode, http.StatusOK, string(b))
	}

	// GET /v1/local-feed/pins (must include the user's pin).
	pinsURL := srv.URL + "/v1/local-feed/pins?" + strings.Join([]string{
		"minLat=" + strconv.FormatFloat(30.0, 'f', -1, 64),
		"maxLat=" + strconv.FormatFloat(32.0, 'f', -1, 64),
		"minLng=" + strconv.FormatFloat(120.0, 'f', -1, 64),
		"maxLng=" + strconv.FormatFloat(122.0, 'f', -1, 64),
		"centerLat=" + strconv.FormatFloat(31.0, 'f', -1, 64),
		"centerLng=" + strconv.FormatFloat(121.0, 'f', -1, 64),
	}, "&")
	pinsRes := get(t, client, pinsURL, registerBody.Token)
	defer pinsRes.Body.Close()
	if pinsRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(pinsRes.Body)
		t.Fatalf("GET /v1/local-feed/pins status = %d, want %d, body=%s", pinsRes.StatusCode, http.StatusOK, string(b))
	}

	// POST /v1/local-feed/posts
	postRes := postJSON(t, client, srv.URL+"/v1/local-feed/posts", map[string]any{
		"text": "hello",
	}, registerBody.Token)
	defer postRes.Body.Close()
	if postRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(postRes.Body)
		t.Fatalf("POST /v1/local-feed/posts status = %d, want %d, body=%s", postRes.StatusCode, http.StatusOK, string(b))
	}
	var createBody struct {
		Post struct {
			ID string `json:"id"`
		} `json:"post"`
	}
	if err := json.NewDecoder(postRes.Body).Decode(&createBody); err != nil {
		t.Fatalf("decode create post response error = %v", err)
	}
	if createBody.Post.ID == "" {
		t.Fatalf("expected non-empty post.id")
	}

	// GET /v1/local-feed/posts (mine)
	listMineRes := get(t, client, srv.URL+"/v1/local-feed/posts", registerBody.Token)
	defer listMineRes.Body.Close()
	if listMineRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(listMineRes.Body)
		t.Fatalf("GET /v1/local-feed/posts status = %d, want %d, body=%s", listMineRes.StatusCode, http.StatusOK, string(b))
	}

	// GET /v1/local-feed/users/{id}/posts
	listUserRes := get(t, client, srv.URL+"/v1/local-feed/users/"+registerBody.User.ID+"/posts?atLat=31&atLng=121", registerBody.Token)
	defer listUserRes.Body.Close()
	if listUserRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(listUserRes.Body)
		t.Fatalf("GET /v1/local-feed/users/{id}/posts status = %d, want %d, body=%s", listUserRes.StatusCode, http.StatusOK, string(b))
	}
}
