package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/wechat"
)

type bindWeChatRequest struct {
	Code string `json:"code"`
}

type bindWeChatResponse struct {
	Bound bool `json:"bound"`
}

type geoFenceItem struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	RadiusM int     `json:"radiusM"`
}

type inviteSettingsItem struct {
	Code        string        `json:"code"`
	ExpiresAtMs *int64        `json:"expiresAtMs,omitempty"`
	GeoFence    *geoFenceItem `json:"geoFence,omitempty"`
	UpdatedAtMs int64         `json:"updatedAtMs"`
}

type inviteSettingsResponse struct {
	Invite inviteSettingsItem `json:"invite"`
}

type subscribeTemplateItem struct {
	TemplateID string `json:"templateId"`
	Page       string `json:"page"`
}

type subscribeTemplatesResponse struct {
	Call     *subscribeTemplateItem `json:"call,omitempty"`
	Activity *subscribeTemplateItem `json:"activity,omitempty"`
}

func (api *v1API) handleWeChat(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/wechat/")
	parts := splitPath(rest)
	if len(parts) == 1 {
		switch parts[0] {
		case "bind":
			if r.Method != http.MethodPost {
				writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
				return
			}
			api.handleWeChatBind(w, r)
			return
		case "subscribe-templates":
			if r.Method != http.MethodGet {
				writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
				return
			}
			api.handleWeChatSubscribeTemplates(w, r)
			return
		default:
			writeAPIError(w, ErrCodeNotFound, "not found")
			return
		}
	}

	if len(parts) == 3 && parts[0] == "code" && parts[1] == "session" && parts[2] == "invite" {
		switch r.Method {
		case http.MethodGet:
			api.handleWeChatSessionInviteSettings(w, r)
		case http.MethodPut:
			api.handleWeChatUpdateSessionInviteSettings(w, r)
		default:
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) == 3 && parts[0] == "code" && parts[1] == "activity" && parts[2] == "invite" {
		switch r.Method {
		case http.MethodGet:
			api.handleWeChatActivityInviteSettings(w, r)
		case http.MethodPut:
			api.handleWeChatUpdateActivityInviteSettings(w, r)
		default:
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		}
		return
	}

	if len(parts) == 2 && parts[0] == "qrcode" && parts[1] == "session" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleWeChatSessionQRCode(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "code" && parts[1] == "session" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleWeChatSessionQRCode(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "qrcode" && parts[1] == "activity" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleWeChatActivityQRCode(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "code" && parts[1] == "activity" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleWeChatActivityQRCode(w, r)
		return
	}

	writeAPIError(w, ErrCodeNotFound, "not found")
}

func (api *v1API) handleWeChatSubscribeTemplates(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var callItem *subscribeTemplateItem
	if strings.TrimSpace(api.wechatCallSubscribeTemplateID) != "" {
		page := strings.TrimSpace(api.wechatCallSubscribePage)
		if page == "" {
			page = "pages/linkbridge/call/call"
		}
		callItem = &subscribeTemplateItem{
			TemplateID: api.wechatCallSubscribeTemplateID,
			Page:       page,
		}
	}

	var activityItem *subscribeTemplateItem
	if strings.TrimSpace(api.wechatActivitySubscribeTemplateID) != "" {
		page := strings.TrimSpace(api.wechatActivitySubscribePage)
		if page == "" {
			page = "pages/chat/index"
		}
		activityItem = &subscribeTemplateItem{
			TemplateID: api.wechatActivitySubscribeTemplateID,
			Page:       page,
		}
	}

	writeJSON(w, http.StatusOK, subscribeTemplatesResponse{
		Call:     callItem,
		Activity: activityItem,
	})
}

func inviteSettingsItemFromSessionInviteRow(row storage.SessionInviteRow) inviteSettingsItem {
	var gf *geoFenceItem
	if row.GeoFence != nil && row.GeoFence.RadiusM > 0 {
		gf = &geoFenceItem{
			Lat:     e7ToFloat(row.GeoFence.LatE7),
			Lng:     e7ToFloat(row.GeoFence.LngE7),
			RadiusM: row.GeoFence.RadiusM,
		}
	}
	return inviteSettingsItem{
		Code:        row.Code,
		ExpiresAtMs: row.ExpiresAtMs,
		GeoFence:    gf,
		UpdatedAtMs: row.UpdatedAtMs,
	}
}

func inviteSettingsItemFromActivityInviteRow(row storage.ActivityInviteRow) inviteSettingsItem {
	var gf *geoFenceItem
	if row.GeoFence != nil && row.GeoFence.RadiusM > 0 {
		gf = &geoFenceItem{
			Lat:     e7ToFloat(row.GeoFence.LatE7),
			Lng:     e7ToFloat(row.GeoFence.LngE7),
			RadiusM: row.GeoFence.RadiusM,
		}
	}
	return inviteSettingsItem{
		Code:        row.Code,
		ExpiresAtMs: row.ExpiresAtMs,
		GeoFence:    gf,
		UpdatedAtMs: row.UpdatedAtMs,
	}
}

func parseInviteSettingsPatch(patch map[string]json.RawMessage, nowMs int64, currentExpiresAtMs *int64, currentGeoFence *storage.GeoFence) (expiresAtMs *int64, geoFence *storage.GeoFence, ok bool, err error) {
	expiresAtMs = currentExpiresAtMs
	geoFence = currentGeoFence

	if raw, exists := patch["expiresAtMs"]; exists {
		ok = true
		v, err := parseNullableInt64(raw)
		if err != nil {
			return nil, nil, false, err
		}
		if v != nil && *v <= nowMs {
			return nil, nil, false, errors.New("expiresAtMs must be in the future")
		}
		expiresAtMs = v
	}

	if raw, exists := patch["geoFence"]; exists {
		ok = true
		v, err := parseNullableGeoFence(raw)
		if err != nil {
			return nil, nil, false, err
		}
		geoFence = v
	}

	return expiresAtMs, geoFence, ok, nil
}

func parseNullableInt64(raw json.RawMessage) (*int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}
	return &n, nil
}

func parseNullableGeoFence(raw json.RawMessage) (*storage.GeoFence, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var v geoFenceItem
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if math.Abs(v.Lat) > 90 || math.Abs(v.Lng) > 180 {
		return nil, errors.New("invalid geoFence lat/lng range")
	}
	if v.RadiusM <= 0 || v.RadiusM > 200000 {
		return nil, errors.New("invalid geoFence radiusM")
	}
	return &storage.GeoFence{
		LatE7:   floatToE7(v.Lat),
		LngE7:   floatToE7(v.Lng),
		RadiusM: v.RadiusM,
	}, nil
}

func (api *v1API) handleWeChatSessionInviteSettings(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	nowMs := time.Now().UnixMilli()
	invite, _, err := api.store.GetOrCreateSessionInvite(r.Context(), userID, nowMs)
	if err != nil {
		api.logger.Error("get session invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, inviteSettingsResponse{Invite: inviteSettingsItemFromSessionInviteRow(invite)})
}

func (api *v1API) handleWeChatUpdateSessionInviteSettings(w http.ResponseWriter, r *http.Request) {
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

	nowMs := time.Now().UnixMilli()
	current, _, err := api.store.GetOrCreateSessionInvite(r.Context(), userID, nowMs)
	if err != nil {
		api.logger.Error("get session invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	expiresAtMs, geoFence, ok, err := parseInviteSettingsPatch(patch, nowMs, current.ExpiresAtMs, current.GeoFence)
	if err != nil {
		writeAPIError(w, ErrCodeValidation, err.Error())
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeValidation, "expiresAtMs or geoFence is required")
		return
	}

	updated, err := api.store.UpdateSessionInviteSettings(r.Context(), userID, expiresAtMs, geoFence, nowMs)
	if err != nil {
		api.logger.Error("update session invite settings failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, inviteSettingsResponse{Invite: inviteSettingsItemFromSessionInviteRow(updated)})
}

func (api *v1API) handleWeChatActivityInviteSettings(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	activityID := strings.TrimSpace(r.URL.Query().Get("activityId"))
	if activityID == "" {
		writeAPIError(w, ErrCodeValidation, "activityId is required")
		return
	}

	activity, err := api.store.GetActivityByID(r.Context(), activityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if activity.CreatorID != userID {
		writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
		return
	}

	nowMs := time.Now().UnixMilli()
	invite, _, err := api.store.GetOrCreateActivityInvite(r.Context(), activityID, nowMs)
	if err != nil {
		api.logger.Error("get activity invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, inviteSettingsResponse{Invite: inviteSettingsItemFromActivityInviteRow(invite)})
}

func (api *v1API) handleWeChatUpdateActivityInviteSettings(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	activityID := strings.TrimSpace(r.URL.Query().Get("activityId"))
	if activityID == "" {
		writeAPIError(w, ErrCodeValidation, "activityId is required")
		return
	}

	activity, err := api.store.GetActivityByID(r.Context(), activityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if activity.CreatorID != userID {
		writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
		return
	}

	var patch map[string]json.RawMessage
	if err := decodeJSON(w, r, &patch); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	nowMs := time.Now().UnixMilli()
	current, _, err := api.store.GetOrCreateActivityInvite(r.Context(), activityID, nowMs)
	if err != nil {
		api.logger.Error("get activity invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	expiresAtMs, geoFence, ok, err := parseInviteSettingsPatch(patch, nowMs, current.ExpiresAtMs, current.GeoFence)
	if err != nil {
		writeAPIError(w, ErrCodeValidation, err.Error())
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeValidation, "expiresAtMs or geoFence is required")
		return
	}

	updated, err := api.store.UpdateActivityInviteSettings(r.Context(), activityID, expiresAtMs, geoFence, nowMs)
	if err != nil {
		api.logger.Error("update activity invite settings failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, inviteSettingsResponse{Invite: inviteSettingsItemFromActivityInviteRow(updated)})
}

func (api *v1API) handleWeChatBind(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	if api.wechatClient == nil || strings.TrimSpace(api.wechatAppID) == "" {
		writeAPIError(w, ErrCodeWeChatNotConfigured, "wechat integration not configured")
		return
	}

	var req bindWeChatRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		writeAPIError(w, ErrCodeValidation, "code is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	cs, err := api.wechatClient.ExchangeCode(ctx, req.Code)
	if err != nil {
		api.logger.Warn("wechat bind exchange code failed", "error", err)
		writeAPIError(w, ErrCodeWeChatAPI, "wechat API error")
		return
	}

	nowMs := time.Now().UnixMilli()
	if _, err := api.store.UpsertWeChatBinding(r.Context(), userID, cs.OpenID, cs.SessionKey, cs.UnionID, nowMs); err != nil {
		api.logger.Error("save wechat binding failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, bindWeChatResponse{Bound: true})
}

func (api *v1API) handleWeChatSessionQRCode(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	if api.wechatClient == nil || strings.TrimSpace(api.wechatAppID) == "" {
		writeAPIError(w, ErrCodeWeChatNotConfigured, "wechat integration not configured")
		return
	}

	nowMs := time.Now().UnixMilli()
	invite, _, err := api.store.GetOrCreateSessionInvite(r.Context(), userID, nowMs)
	if err != nil {
		api.logger.Error("create session invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	accessToken, err := api.wechatClient.GetAccessToken(ctx)
	if err != nil {
		api.logger.Warn("wechat get access token failed", "error", err)
		writeAPIError(w, ErrCodeWeChatAPI, "wechat API error")
		return
	}

	png, err := api.wechatClient.GetWxaCodeUnlimit(ctx, accessToken, wechat.WxaCodeUnlimitRequest{
		Scene:      "c=" + invite.Code,
		Page:       "pages/linkbridge/add-friend/add-friend",
		CheckPath:  false,
		EnvVersion: "develop",
		Width:      430,
	})
	if err != nil {
		api.logger.Warn("wechat getwxacodeunlimit failed", "error", err)
		writeAPIError(w, ErrCodeWeChatAPI, "wechat API error")
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

func (api *v1API) handleWeChatActivityQRCode(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	if api.wechatClient == nil || strings.TrimSpace(api.wechatAppID) == "" {
		writeAPIError(w, ErrCodeWeChatNotConfigured, "wechat integration not configured")
		return
	}

	activityID := strings.TrimSpace(r.URL.Query().Get("activityId"))
	if activityID == "" {
		writeAPIError(w, ErrCodeValidation, "activityId is required")
		return
	}

	activity, err := api.store.GetActivityByID(r.Context(), activityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if activity.CreatorID != userID {
		writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
		return
	}

	nowMs := time.Now().UnixMilli()
	invite, _, err := api.store.GetOrCreateActivityInvite(r.Context(), activityID, nowMs)
	if err != nil {
		api.logger.Error("create activity invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	accessToken, err := api.wechatClient.GetAccessToken(ctx)
	if err != nil {
		api.logger.Warn("wechat get access token failed", "error", err)
		writeAPIError(w, ErrCodeWeChatAPI, "wechat API error")
		return
	}

	png, err := api.wechatClient.GetWxaCodeUnlimit(ctx, accessToken, wechat.WxaCodeUnlimitRequest{
		Scene:      "a=" + invite.Code,
		Page:       "pages/linkbridge/add-friend/add-friend",
		CheckPath:  false,
		EnvVersion: "develop",
		Width:      430,
	})
	if err != nil {
		api.logger.Warn("wechat getwxacodeunlimit failed", "error", err)
		writeAPIError(w, ErrCodeWeChatAPI, "wechat API error")
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}
