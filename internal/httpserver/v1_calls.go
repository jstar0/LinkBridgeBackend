package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/wechat"
	"linkbridge-backend/internal/ws"
)

type createCallRequest struct {
	CalleeUserID string `json:"calleeUserId"`
	MediaType    string `json:"mediaType"` // voice|video
}

type callItem struct {
	ID          string `json:"id"`
	GroupID     string `json:"groupId"`
	CallerID    string `json:"callerId"`
	CalleeID    string `json:"calleeId"`
	MediaType   string `json:"mediaType"`
	Status      string `json:"status"`
	CreatedAtMs int64  `json:"createdAtMs"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
}

type createCallResponse struct {
	Call callItem `json:"call"`
}

func (api *v1API) handleCalls(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.handleCreateCall(w, r)
	default:
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (api *v1API) handleCallSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/calls/")
	parts := splitPath(rest)
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleGetCall(w, r, parts[0])
		return
	}
	if len(parts) != 2 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	callID := parts[0]
	switch parts[1] {
	case "accept":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleAcceptCall(w, r, callID)
	case "reject":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleRejectCall(w, r, callID)
	case "cancel":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleCancelCall(w, r, callID)
	case "end":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleEndCall(w, r, callID)
	case "voip":
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleGetVoipSign(w, r, callID)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleGetCall(w http.ResponseWriter, r *http.Request, callID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	call, err := api.store.GetCallByID(r.Context(), callID)
	if err != nil {
		api.writeCallError(w, err)
		return
	}
	if call.CallerID != userID && call.CalleeID != userID {
		writeAPIError(w, ErrCodeCallAccessDenied, "access denied")
		return
	}

	item := callItemFromRow(call)
	resp := map[string]any{"call": item}

	caller, err := api.store.GetUserByID(r.Context(), call.CallerID)
	if err == nil && caller.ID != "" {
		resp["caller"] = map[string]any{
			"id":          caller.ID,
			"displayName": caller.DisplayName,
			"avatarUrl":   caller.AvatarURL,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (api *v1API) handleCreateCall(w http.ResponseWriter, r *http.Request) {
	callerID := getUserIDFromContext(r.Context())
	if callerID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req createCallRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	req.CalleeUserID = strings.TrimSpace(req.CalleeUserID)
	req.MediaType = strings.TrimSpace(req.MediaType)
	if req.MediaType == "" {
		req.MediaType = storage.CallMediaTypeVoice
	}
	if req.MediaType != storage.CallMediaTypeVoice && req.MediaType != storage.CallMediaTypeVideo {
		writeAPIError(w, ErrCodeValidation, "invalid mediaType")
		return
	}
	if req.CalleeUserID == "" {
		writeAPIError(w, ErrCodeValidation, "calleeUserId is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	groupID, err := newNumericGroupID(18)
	if err != nil {
		api.logger.Error("generate call groupId failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	call, err := api.store.CreateCall(r.Context(), callerID, req.CalleeUserID, req.MediaType, groupID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeValidation, "cannot call self")
			return
		}
		api.logger.Error("create call failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := callItemFromRow(call)
	writeJSON(w, http.StatusOK, createCallResponse{Call: item})

	caller, err := api.store.GetUserByID(r.Context(), call.CallerID)
	if err != nil {
		api.logger.Warn("get caller user failed", "error", err, "callerID", call.CallerID)
	}

	payload := map[string]any{
		"call": item,
	}
	if caller.ID != "" {
		payload["caller"] = map[string]any{
			"id":          caller.ID,
			"displayName": caller.DisplayName,
			"avatarUrl":   caller.AvatarURL,
		}
	}

	api.sendToUsers([]string{call.CallerID, call.CalleeID}, ws.Envelope{
		Type:      "call.created",
		SessionID: "",
		Payload:   payload,
	})

	api.sendToUser(call.CalleeID, ws.Envelope{
		Type:      "call.invite",
		SessionID: "",
		Payload:   payload,
	})

	go api.bestEffortOfflineCallNotify(call)
}

func (api *v1API) handleAcceptCall(w http.ResponseWriter, r *http.Request, callID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	nowMs := time.Now().UnixMilli()
	call, err := api.store.AcceptCall(r.Context(), callID, userID, nowMs)
	if err != nil {
		api.writeCallError(w, err)
		return
	}

	item := callItemFromRow(call)
	writeJSON(w, http.StatusOK, map[string]any{"call": item})
	api.sendToUsers([]string{call.CallerID, call.CalleeID}, ws.Envelope{
		Type:      "call.accepted",
		SessionID: "",
		Payload: map[string]any{
			"call": item,
		},
	})
}

func (api *v1API) handleRejectCall(w http.ResponseWriter, r *http.Request, callID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	nowMs := time.Now().UnixMilli()
	call, err := api.store.RejectCall(r.Context(), callID, userID, nowMs)
	if err != nil {
		api.writeCallError(w, err)
		return
	}

	item := callItemFromRow(call)
	writeJSON(w, http.StatusOK, map[string]any{"call": item})
	api.sendToUsers([]string{call.CallerID, call.CalleeID}, ws.Envelope{
		Type:      "call.rejected",
		SessionID: "",
		Payload: map[string]any{
			"call": item,
		},
	})
}

func (api *v1API) handleCancelCall(w http.ResponseWriter, r *http.Request, callID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	nowMs := time.Now().UnixMilli()
	call, err := api.store.CancelCall(r.Context(), callID, userID, nowMs)
	if err != nil {
		api.writeCallError(w, err)
		return
	}

	item := callItemFromRow(call)
	writeJSON(w, http.StatusOK, map[string]any{"call": item})
	api.sendToUsers([]string{call.CallerID, call.CalleeID}, ws.Envelope{
		Type:      "call.canceled",
		SessionID: "",
		Payload: map[string]any{
			"call": item,
		},
	})
}

func (api *v1API) handleEndCall(w http.ResponseWriter, r *http.Request, callID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	nowMs := time.Now().UnixMilli()
	call, err := api.store.EndCall(r.Context(), callID, userID, nowMs)
	if err != nil {
		api.writeCallError(w, err)
		return
	}

	item := callItemFromRow(call)
	writeJSON(w, http.StatusOK, map[string]any{"call": item})
	api.sendToUsers([]string{call.CallerID, call.CalleeID}, ws.Envelope{
		Type:      "call.ended",
		SessionID: "",
		Payload: map[string]any{
			"call": item,
		},
	})
}

func (api *v1API) handleGetVoipSign(w http.ResponseWriter, r *http.Request, callID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	if api.wechatClient == nil || strings.TrimSpace(api.wechatAppID) == "" {
		writeAPIError(w, ErrCodeWeChatNotConfigured, "wechat integration not configured")
		return
	}

	call, err := api.store.GetCallByID(r.Context(), callID)
	if err != nil {
		api.writeCallError(w, err)
		return
	}
	if call.CallerID != userID && call.CalleeID != userID {
		writeAPIError(w, ErrCodeCallAccessDenied, "access denied")
		return
	}

	binding, err := api.store.GetWeChatBindingByUserID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeWeChatNotBound, "wechat not bound")
			return
		}
		api.logger.Error("get wechat binding failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	nonceStr, err := randomHex(16)
	if err != nil {
		api.logger.Error("generate nonce failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	timeStamp := time.Now().Unix()
	signature := computeVoipSignature(api.wechatAppID, call.GroupID, nonceStr, timeStamp, binding.SessionKey)
	writeJSON(w, http.StatusOK, wechatVoipSignResponse{
		GroupID:   call.GroupID,
		NonceStr:  nonceStr,
		TimeStamp: timeStamp,
		Signature: signature,
		RoomType:  call.MediaType,
	})
}

func (api *v1API) writeCallError(w http.ResponseWriter, err error) {
	if err == nil {
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		writeAPIError(w, ErrCodeCallNotFound, "call not found")
		return
	}
	if errors.Is(err, storage.ErrAccessDenied) {
		writeAPIError(w, ErrCodeCallAccessDenied, "access denied")
		return
	}
	if errors.Is(err, storage.ErrInvalidState) {
		writeAPIError(w, ErrCodeCallInvalidState, "invalid call state")
		return
	}
	api.logger.Error("call operation failed", "error", err)
	writeAPIError(w, ErrCodeInternal, "internal error")
}

func callItemFromRow(call storage.CallRow) callItem {
	return callItem{
		ID:          call.ID,
		GroupID:     call.GroupID,
		CallerID:    call.CallerID,
		CalleeID:    call.CalleeID,
		MediaType:   call.MediaType,
		Status:      call.Status,
		CreatedAtMs: call.CreatedAtMs,
		UpdatedAtMs: call.UpdatedAtMs,
	}
}

func randomHex(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", fmt.Errorf("invalid nBytes")
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func newNumericGroupID(length int) (string, error) {
	if length <= 0 || length > 64 {
		return "", fmt.Errorf("invalid length")
	}

	var b strings.Builder
	b.Grow(length)
	max := big.NewInt(10)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(byte('0' + n.Int64()))
	}

	id := b.String()
	if strings.Trim(id, "0") == "" {
		return "", fmt.Errorf("generated all-zero id")
	}
	return id, nil
}

func (api *v1API) bestEffortOfflineCallNotify(call storage.CallRow) {
	if api.wechatClient == nil || api.wechatCallSubscribeTemplateID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	binding, err := api.store.GetWeChatBindingByUserID(ctx, call.CalleeID)
	if err != nil {
		return
	}

	accessToken, err := api.wechatClient.GetAccessToken(ctx)
	if err != nil {
		api.logger.Warn("wechat get access token failed", "error", err)
		return
	}

	caller, err := api.store.GetUserByID(ctx, call.CallerID)
	if err != nil {
		return
	}

	page := strings.TrimSpace(api.wechatCallSubscribePage)
	if page == "" {
		page = "pages/linkbridge/call/call"
	}
	page = fmt.Sprintf("%s?callId=%s&incoming=1", page, url.QueryEscape(call.ID))

	title := "语音通话"
	if call.MediaType == storage.CallMediaTypeVideo {
		title = "视频通话"
	}

	createdAt := time.UnixMilli(call.CreatedAtMs).Format("2006-01-02 15:04:05")
	callerName := strings.TrimSpace(caller.DisplayName)
	if callerName == "" {
		callerName = "对方"
	}
	content := fmt.Sprintf("%s 邀请你%s，点击进入接听", callerName, title)

	data := map[string]any{
		"time2":  map[string]any{"value": createdAt},
		"thing4": map[string]any{"value": title},
		"thing5": map[string]any{"value": callerName},
		"thing6": map[string]any{"value": content},
	}

	err = api.wechatClient.SendSubscribeMessage(ctx, accessToken, wechat.SubscribeSendRequest{
		ToUser:     binding.OpenID,
		TemplateID: api.wechatCallSubscribeTemplateID,
		Page:       page,
		Data:       data,
	})
	if err != nil {
		api.logger.Warn("wechat subscribe send failed", "error", err)
	}
}
