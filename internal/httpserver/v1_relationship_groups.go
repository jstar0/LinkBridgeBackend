package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type relationshipGroupItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CreatedAtMs int64  `json:"createdAtMs"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
}

type listRelationshipGroupsResponse struct {
	Groups []relationshipGroupItem `json:"groups"`
}

type createRelationshipGroupRequest struct {
	Name string `json:"name"`
}

type createRelationshipGroupResponse struct {
	Group   relationshipGroupItem `json:"group"`
	Created bool                  `json:"created"`
}

type renameRelationshipGroupRequest struct {
	Name string `json:"name"`
}

func (api *v1API) handleRelationshipGroups(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/v1/relationship-groups")
	if rest == "" || rest == "/" {
		switch r.Method {
		case http.MethodGet:
			api.handleListRelationshipGroups(w, r, userID)
		case http.MethodPost:
			api.handleCreateRelationshipGroup(w, r, userID)
		default:
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		}
		return
	}

	rest = strings.TrimPrefix(rest, "/")
	parts := splitPath(rest)
	if len(parts) != 2 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	groupID := strings.TrimSpace(parts[0])
	action := parts[1]
	if r.Method != http.MethodPost {
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	switch action {
	case "rename":
		api.handleRenameRelationshipGroup(w, r, userID, groupID)
	case "delete":
		api.handleDeleteRelationshipGroup(w, r, userID, groupID)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleListRelationshipGroups(w http.ResponseWriter, r *http.Request, userID string) {
	groups, err := api.store.ListRelationshipGroups(r.Context(), userID)
	if err != nil {
		api.logger.Error("list relationship groups failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]relationshipGroupItem, 0, len(groups))
	for _, g := range groups {
		items = append(items, relationshipGroupItem{
			ID:          g.ID,
			Name:        g.Name,
			CreatedAtMs: g.CreatedAtMs,
			UpdatedAtMs: g.UpdatedAtMs,
		})
	}
	writeJSON(w, http.StatusOK, listRelationshipGroupsResponse{Groups: items})
}

func (api *v1API) handleCreateRelationshipGroup(w http.ResponseWriter, r *http.Request, userID string) {
	var req createRelationshipGroupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeAPIError(w, ErrCodeValidation, "name is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	group, created, err := api.store.CreateRelationshipGroup(r.Context(), userID, name, nowMs)
	if err != nil {
		api.logger.Error("create relationship group failed", "error", err)
		writeAPIError(w, ErrCodeValidation, "invalid group name")
		return
	}

	writeJSON(w, http.StatusOK, createRelationshipGroupResponse{
		Group: relationshipGroupItem{
			ID:          group.ID,
			Name:        group.Name,
			CreatedAtMs: group.CreatedAtMs,
			UpdatedAtMs: group.UpdatedAtMs,
		},
		Created: created,
	})
}

func (api *v1API) handleRenameRelationshipGroup(w http.ResponseWriter, r *http.Request, userID, groupID string) {
	var req renameRelationshipGroupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeAPIError(w, ErrCodeValidation, "name is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	group, err := api.store.RenameRelationshipGroup(r.Context(), userID, groupID, name, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeNotFound, "group not found")
			return
		}
		if errors.Is(err, storage.ErrGroupExists) {
			writeAPIError(w, ErrCodeValidation, "group name exists")
			return
		}
		api.logger.Error("rename relationship group failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"group": relationshipGroupItem{
			ID:          group.ID,
			Name:        group.Name,
			CreatedAtMs: group.CreatedAtMs,
			UpdatedAtMs: group.UpdatedAtMs,
		},
	})
}

func (api *v1API) handleDeleteRelationshipGroup(w http.ResponseWriter, r *http.Request, userID, groupID string) {
	if err := api.store.DeleteRelationshipGroup(r.Context(), userID, groupID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeNotFound, "group not found")
			return
		}
		api.logger.Error("delete relationship group failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
