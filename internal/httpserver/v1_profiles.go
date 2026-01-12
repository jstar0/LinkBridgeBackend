package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type profileCoreItem struct {
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

type profileItem struct {
	Nickname          string          `json:"nickname"`
	AvatarURL         *string         `json:"avatarUrl,omitempty"`
	NicknameOverride  *string         `json:"nicknameOverride,omitempty"`
	AvatarURLOverride *string         `json:"avatarUrlOverride,omitempty"`
	Fields            json.RawMessage `json:"fields"`
	UpdatedAtMs       int64           `json:"updatedAtMs"`
}

type getProfileResponse struct {
	Core    profileCoreItem `json:"core"`
	Profile profileItem     `json:"profile"`
}

func (api *v1API) handleProfiles(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/profiles/")
	parts := splitPath(rest)
	if len(parts) != 1 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	switch parts[0] {
	case "card":
		api.handleProfileKind(w, r, "card")
	case "map":
		api.handleProfileKind(w, r, "map")
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleProfileKind(w http.ResponseWriter, r *http.Request, kind string) {
	switch r.Method {
	case http.MethodGet:
		api.handleGetProfile(w, r, kind)
	case http.MethodPut:
		api.handleUpsertProfile(w, r, kind)
	default:
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (api *v1API) handleGetProfile(w http.ResponseWriter, r *http.Request, kind string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	user, err := api.store.GetUserByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeUserNotFound, "user not found")
			return
		}
		api.logger.Error("get user failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	profile, err := api.getProfileRow(r, kind, userID)
	if err != nil {
		api.logger.Error("get profile failed", "error", err, "kind", kind)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	core := profileCoreItem{
		UserID:      user.ID,
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL,
	}

	resolvedNickname := user.DisplayName
	if profile.NicknameOverride != nil && strings.TrimSpace(*profile.NicknameOverride) != "" {
		resolvedNickname = strings.TrimSpace(*profile.NicknameOverride)
	}
	resolvedAvatar := user.AvatarURL
	if profile.AvatarURLOverride != nil && strings.TrimSpace(*profile.AvatarURLOverride) != "" {
		val := strings.TrimSpace(*profile.AvatarURLOverride)
		resolvedAvatar = &val
	}

	fields := normalizeRawJSONObject(profile.ProfileJSON)
	writeJSON(w, http.StatusOK, getProfileResponse{
		Core: core,
		Profile: profileItem{
			Nickname:          resolvedNickname,
			AvatarURL:         resolvedAvatar,
			NicknameOverride:  profile.NicknameOverride,
			AvatarURLOverride: profile.AvatarURLOverride,
			Fields:            fields,
			UpdatedAtMs:       profile.UpdatedAtMs,
		},
	})
}

func (api *v1API) handleUpsertProfile(w http.ResponseWriter, r *http.Request, kind string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var patch map[string]json.RawMessage
	if err := decodeJSON(w, r, &patch); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	existing, err := api.getProfileRow(r, kind, userID)
	if err != nil {
		api.logger.Error("get existing profile failed", "error", err, "kind", kind)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	nicknameOverride := existing.NicknameOverride
	avatarOverride := existing.AvatarURLOverride
	profileJSON := existing.ProfileJSON

	if raw, ok := patch["nicknameOverride"]; ok {
		v, err := parseNullableTrimmedString(raw)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid nicknameOverride")
			return
		}
		nicknameOverride = v
	}
	if raw, ok := patch["avatarUrlOverride"]; ok {
		v, err := parseNullableTrimmedString(raw)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid avatarUrlOverride")
			return
		}
		avatarOverride = v
	}
	if raw, ok := patch["fields"]; ok {
		v, err := parseFieldsObject(raw)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, err.Error())
			return
		}
		profileJSON = v
	}

	nowMs := time.Now().UnixMilli()
	switch kind {
	case "card":
		if _, err := api.store.UpsertUserCardProfile(r.Context(), userID, nicknameOverride, avatarOverride, profileJSON, nowMs); err != nil {
			api.logger.Error("upsert card profile failed", "error", err)
			writeAPIError(w, ErrCodeInternal, "internal error")
			return
		}
	case "map":
		if _, err := api.store.UpsertUserMapProfile(r.Context(), userID, nicknameOverride, avatarOverride, profileJSON, nowMs); err != nil {
			api.logger.Error("upsert map profile failed", "error", err)
			writeAPIError(w, ErrCodeInternal, "internal error")
			return
		}
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	api.handleGetProfile(w, r, kind)
}

func (api *v1API) getProfileRow(r *http.Request, kind string, userID string) (storage.UserProfileRow, error) {
	switch kind {
	case "card":
		row, err := api.store.GetUserCardProfile(r.Context(), userID)
		if err == nil {
			return row, nil
		}
		if errors.Is(err, storage.ErrNotFound) {
			return storage.UserProfileRow{UserID: userID, ProfileJSON: "{}"}, nil
		}
		return storage.UserProfileRow{}, err
	case "map":
		row, err := api.store.GetUserMapProfile(r.Context(), userID)
		if err == nil {
			return row, nil
		}
		if errors.Is(err, storage.ErrNotFound) {
			return storage.UserProfileRow{UserID: userID, ProfileJSON: "{}"}, nil
		}
		return storage.UserProfileRow{}, err
	default:
		return storage.UserProfileRow{}, errors.New("unknown profile kind")
	}
}

func parseNullableTrimmedString(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	return &s, nil
}

func parseFieldsObject(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	if v == nil {
		return "{}", nil
	}
	if _, ok := v.(map[string]any); !ok {
		return "", errors.New("fields must be a JSON object")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func normalizeRawJSONObject(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage(`{}`)
	}
	if raw[0] != '{' {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}
