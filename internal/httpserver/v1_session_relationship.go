package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type sessionRelationshipItem struct {
	SessionID   string   `json:"sessionId"`
	Source      string   `json:"source"`
	Note        *string  `json:"note,omitempty"`
	GroupID     *string  `json:"groupId,omitempty"`
	GroupName   *string  `json:"groupName,omitempty"`
	Tags        []string `json:"tags"`
	UpdatedAtMs int64    `json:"updatedAtMs"`
}

type getSessionRelationshipResponse struct {
	Relationship sessionRelationshipItem `json:"relationship"`
}

func (api *v1API) handleGetSessionRelationship(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeAPIError(w, ErrCodeValidation, "sessionId is required")
		return
	}

	ok, err := api.store.IsSessionParticipant(r.Context(), sessionID, userID)
	if err != nil {
		api.logger.Error("check session participant failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
		return
	}

	session, err := api.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		api.logger.Error("get session failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	meta, err := api.store.GetSessionUserMeta(r.Context(), sessionID, userID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		api.logger.Error("get session meta failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	var item sessionRelationshipItem
	item.SessionID = sessionID
	item.Source = session.Source
	if errors.Is(err, storage.ErrNotFound) {
		item.Tags = nil
		item.UpdatedAtMs = session.UpdatedAtMs
	} else {
		item.Note = meta.Note
		item.GroupID = meta.GroupID
		item.GroupName = meta.GroupName
		item.Tags = storage.ParseTagsJSON(meta.TagsJSON)
		item.UpdatedAtMs = meta.UpdatedAtMs
	}

	writeJSON(w, http.StatusOK, getSessionRelationshipResponse{Relationship: item})
}

func (api *v1API) handleUpsertSessionRelationship(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeAPIError(w, ErrCodeValidation, "sessionId is required")
		return
	}

	ok, err := api.store.IsSessionParticipant(r.Context(), sessionID, userID)
	if err != nil {
		api.logger.Error("check session participant failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
		return
	}

	// Load existing meta for patch semantics.
	existing, err := api.store.GetSessionUserMeta(r.Context(), sessionID, userID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		api.logger.Error("get existing session meta failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	currentNote := existing.Note
	currentGroupID := existing.GroupID
	currentTags := storage.ParseTagsJSON(existing.TagsJSON)

	var patch map[string]json.RawMessage
	if err := decodeJSON(w, r, &patch); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	note := currentNote
	groupID := currentGroupID
	tags := currentTags

	if raw, ok := patch["note"]; ok {
		v, err := parseNullableTrimmedString(raw)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid note")
			return
		}
		note = v
	}

	if raw, ok := patch["groupId"]; ok {
		v, err := parseNullableTrimmedString(raw)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid groupId")
			return
		}
		if v != nil {
			if _, err := api.store.GetRelationshipGroupByID(r.Context(), userID, *v); err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					writeAPIError(w, ErrCodeValidation, "group not found")
					return
				}
				api.logger.Error("get relationship group failed", "error", err)
				writeAPIError(w, ErrCodeInternal, "internal error")
				return
			}
		}
		groupID = v
	}

	if raw, ok := patch["tags"]; ok {
		v, err := parseStringArray(raw)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, err.Error())
			return
		}
		tags = v
	}

	nowMs := time.Now().UnixMilli()
	if _, err := api.store.UpsertSessionUserMeta(r.Context(), sessionID, userID, note, groupID, tags, nowMs); err != nil {
		api.logger.Error("upsert session relationship failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	api.handleGetSessionRelationship(w, r, sessionID)
}

func parseStringArray(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, errors.New("tags must be a JSON array")
	}
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		s, ok := it.(string)
		if !ok {
			return nil, errors.New("tags must be string array")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
