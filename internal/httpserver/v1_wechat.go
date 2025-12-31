package httpserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/wechat"
)

type bindWeChatRequest struct {
	Code string `json:"code"`
}

type bindWeChatResponse struct {
	Bound bool `json:"bound"`
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
		default:
			writeAPIError(w, ErrCodeNotFound, "not found")
			return
		}
	}

	if len(parts) == 2 && parts[0] == "qrcode" && parts[1] == "friend" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleWeChatFriendQRCode(w, r)
		return
	}

	writeAPIError(w, ErrCodeNotFound, "not found")
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

func (api *v1API) handleWeChatFriendQRCode(w http.ResponseWriter, r *http.Request) {
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
	invite, _, err := api.store.GetOrCreateFriendInvite(r.Context(), userID, nowMs)
	if err != nil {
		api.logger.Error("create friend invite failed", "error", err)
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
