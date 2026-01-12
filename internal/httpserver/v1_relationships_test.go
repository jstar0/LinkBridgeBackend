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

func TestRelationshipGroupsAndSessionRelationship_Smoke(t *testing.T) {
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

	_, token1 := register("alice")
	user2ID, _ := register("bobby")

	// Create a session.
	createSessionRes := postJSON(t, client, srv.URL+"/v1/sessions", map[string]any{
		"peerUserId": user2ID,
	}, token1)
	defer createSessionRes.Body.Close()
	if createSessionRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createSessionRes.Body)
		t.Fatalf("POST /v1/sessions status = %d, want %d, body=%s", createSessionRes.StatusCode, http.StatusOK, string(b))
	}
	var createSessionBody struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(createSessionRes.Body).Decode(&createSessionBody); err != nil {
		t.Fatalf("decode create session response error = %v", err)
	}
	if createSessionBody.Session.ID == "" {
		t.Fatalf("expected session.id to be non-empty")
	}
	sessionID := createSessionBody.Session.ID

	// Create a group.
	createGroupRes := postJSON(t, client, srv.URL+"/v1/relationship-groups", map[string]any{
		"name": "Group1",
	}, token1)
	defer createGroupRes.Body.Close()
	if createGroupRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(createGroupRes.Body)
		t.Fatalf("POST /v1/relationship-groups status = %d, want %d, body=%s", createGroupRes.StatusCode, http.StatusOK, string(b))
	}
	var createGroupBody struct {
		Group struct {
			ID string `json:"id"`
		} `json:"group"`
	}
	if err := json.NewDecoder(createGroupRes.Body).Decode(&createGroupBody); err != nil {
		t.Fatalf("decode create group response error = %v", err)
	}
	if createGroupBody.Group.ID == "" {
		t.Fatalf("expected group.id to be non-empty")
	}
	groupID := createGroupBody.Group.ID

	// Assign relationship meta.
	updateRelRes := putJSON(t, client, srv.URL+"/v1/sessions/"+sessionID+"/relationship", map[string]any{
		"note":    "hello",
		"groupId": groupID,
		"tags":    []string{"t2", "t1", "t1"},
	}, token1)
	defer updateRelRes.Body.Close()
	if updateRelRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(updateRelRes.Body)
		t.Fatalf("PUT /v1/sessions/{id}/relationship status = %d, want %d, body=%s", updateRelRes.StatusCode, http.StatusOK, string(b))
	}

	getRelRes := get(t, client, srv.URL+"/v1/sessions/"+sessionID+"/relationship", token1)
	defer getRelRes.Body.Close()
	if getRelRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getRelRes.Body)
		t.Fatalf("GET /v1/sessions/{id}/relationship status = %d, want %d, body=%s", getRelRes.StatusCode, http.StatusOK, string(b))
	}
	var relBody struct {
		Relationship struct {
			SessionID string   `json:"sessionId"`
			Source    string   `json:"source"`
			Note      *string  `json:"note"`
			GroupID   *string  `json:"groupId"`
			GroupName *string  `json:"groupName"`
			Tags      []string `json:"tags"`
		} `json:"relationship"`
	}
	if err := json.NewDecoder(getRelRes.Body).Decode(&relBody); err != nil {
		t.Fatalf("decode relationship response error = %v", err)
	}
	if relBody.Relationship.SessionID != sessionID {
		t.Fatalf("relationship.sessionId = %q, want %q", relBody.Relationship.SessionID, sessionID)
	}
	if relBody.Relationship.Source == "" {
		t.Fatalf("expected relationship.source to be non-empty")
	}
	if relBody.Relationship.Note == nil || *relBody.Relationship.Note != "hello" {
		t.Fatalf("relationship.note = %v, want %q", relBody.Relationship.Note, "hello")
	}
	if relBody.Relationship.GroupID == nil || *relBody.Relationship.GroupID != groupID {
		t.Fatalf("relationship.groupId = %v, want %q", relBody.Relationship.GroupID, groupID)
	}
	if relBody.Relationship.GroupName == nil || *relBody.Relationship.GroupName != "Group1" {
		t.Fatalf("relationship.groupName = %v, want %q", relBody.Relationship.GroupName, "Group1")
	}
	if len(relBody.Relationship.Tags) != 2 || relBody.Relationship.Tags[0] != "t1" || relBody.Relationship.Tags[1] != "t2" {
		t.Fatalf("relationship.tags = %#v, want %v", relBody.Relationship.Tags, []string{"t1", "t2"})
	}

	// Rename group and verify relationship resolves new groupName.
	renameRes := postJSON(t, client, srv.URL+"/v1/relationship-groups/"+groupID+"/rename", map[string]any{
		"name": "G2",
	}, token1)
	defer renameRes.Body.Close()
	if renameRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(renameRes.Body)
		t.Fatalf("POST /v1/relationship-groups/{id}/rename status = %d, want %d, body=%s", renameRes.StatusCode, http.StatusOK, string(b))
	}

	getRel2Res := get(t, client, srv.URL+"/v1/sessions/"+sessionID+"/relationship", token1)
	defer getRel2Res.Body.Close()
	if getRel2Res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getRel2Res.Body)
		t.Fatalf("GET relationship (2) status = %d, want %d, body=%s", getRel2Res.StatusCode, http.StatusOK, string(b))
	}
	var rel2 struct {
		Relationship struct {
			GroupName *string `json:"groupName"`
		} `json:"relationship"`
	}
	if err := json.NewDecoder(getRel2Res.Body).Decode(&rel2); err != nil {
		t.Fatalf("decode relationship (2) error = %v", err)
	}
	if rel2.Relationship.GroupName == nil || *rel2.Relationship.GroupName != "G2" {
		t.Fatalf("relationship.groupName = %v, want %q", rel2.Relationship.GroupName, "G2")
	}

	// Delete group and verify meta group clears (FK ON DELETE SET NULL).
	deleteRes := postJSON(t, client, srv.URL+"/v1/relationship-groups/"+groupID+"/delete", map[string]any{}, token1)
	defer deleteRes.Body.Close()
	if deleteRes.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(deleteRes.Body)
		t.Fatalf("POST /v1/relationship-groups/{id}/delete status = %d, want %d, body=%s", deleteRes.StatusCode, http.StatusOK, string(b))
	}

	getRel3Res := get(t, client, srv.URL+"/v1/sessions/"+sessionID+"/relationship", token1)
	defer getRel3Res.Body.Close()
	if getRel3Res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getRel3Res.Body)
		t.Fatalf("GET relationship (3) status = %d, want %d, body=%s", getRel3Res.StatusCode, http.StatusOK, string(b))
	}
	var rel3 struct {
		Relationship struct {
			GroupID   *string `json:"groupId"`
			GroupName *string `json:"groupName"`
		} `json:"relationship"`
	}
	if err := json.NewDecoder(getRel3Res.Body).Decode(&rel3); err != nil {
		t.Fatalf("decode relationship (3) error = %v", err)
	}
	if rel3.Relationship.GroupID != nil || rel3.Relationship.GroupName != nil {
		t.Fatalf("expected relationship group to be cleared after delete, got groupId=%v groupName=%v", rel3.Relationship.GroupID, rel3.Relationship.GroupName)
	}

}
