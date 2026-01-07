package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

type consumeSessionInviteRequest struct {
	Code string `json:"code"`
}

type sessionRequestItem struct {
	ID          string `json:"id"`
	RequesterID string `json:"requesterId"`
	AddresseeID string `json:"addresseeId"`
	Status      string `json:"status"`
	CreatedAtMs int64  `json:"createdAtMs"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
}

type createSessionRequestResponse struct {
	Request sessionRequestItem `json:"request"`
	Created bool               `json:"created"`
	Hint    string             `json:"hint,omitempty"`
}

type listSessionRequestsResponse struct {
	Requests []sessionRequestItem `json:"requests"`
}

type sessionItem struct {
	ID          string `json:"id"`
	User1ID     string `json:"user1Id"`
	User2ID     string `json:"user2Id"`
	Status      string `json:"status"`
	CreatedAtMs int64  `json:"createdAtMs"`
}

func sessionItemFromRow(s storage.SessionRow) sessionItem {
	return sessionItem{
		ID:          s.ID,
		User1ID:     s.User1ID,
		User2ID:     s.User2ID,
		Status:      s.Status,
		CreatedAtMs: s.CreatedAtMs,
	}
}

func (api *v1API) handleSessionRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	api.handleListSessionRequests(w, r)
}

func (api *v1API) handleSessionRequestSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/session-requests/")
	parts := splitPath(rest)
	if len(parts) == 0 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	if len(parts) == 2 && parts[0] == "invites" && parts[1] == "consume" {
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleConsumeSessionInvite(w, r)
		return
	}

	if len(parts) != 2 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	requestID := parts[0]
	action := parts[1]
	if r.Method != http.MethodPost {
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	switch action {
	case "accept":
		api.handleAcceptSessionRequest(w, r, requestID)
	case "reject":
		api.handleRejectSessionRequest(w, r, requestID)
	case "cancel":
		api.handleCancelSessionRequest(w, r, requestID)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleConsumeSessionInvite(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req consumeSessionInviteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		writeAPIError(w, ErrCodeValidation, "code is required")
		return
	}

	invite, err := api.store.ResolveSessionInvite(r.Context(), req.Code)
	if err != nil {
		if errors.Is(err, storage.ErrInviteInvalid) || errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionInviteInvalid, "invalid invite")
			return
		}
		api.logger.Error("resolve session invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	nowMs := time.Now().UnixMilli()
	sr, created, err := api.store.CreateSessionRequest(r.Context(), userID, invite.InviterID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeValidation, "cannot add self")
			return
		}
		if errors.Is(err, storage.ErrSessionExists) {
			writeAPIError(w, ErrCodeSessionExists, "session already exists")
			return
		}
		if errors.Is(err, storage.ErrSessionArchived) {
			// 会话已归档，自动激活它
			session, reactivateErr := api.store.ReactivateSessionByParticipants(r.Context(), userID, invite.InviterID, nowMs)
			if reactivateErr != nil {
				api.logger.Error("reactivate archived session failed", "error", reactivateErr)
				writeAPIError(w, ErrCodeInternal, "internal error")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"reactivated": true,
				"sessionId":   session.ID,
				"hint":        "会话已重新激活",
			})
			return
		}
		if errors.Is(err, storage.ErrRequestExists) {
			writeAPIError(w, ErrCodeSessionRequestExists, "session request exists")
			return
		}
		api.logger.Error("create session request from invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := sessionRequestItemFromRow(sr)
	hint := ""
	if !created {
		hint = "request updated"
	}
	writeJSON(w, http.StatusOK, createSessionRequestResponse{Request: item, Created: created, Hint: hint})

	api.sendToUser(sr.AddresseeID, ws.Envelope{
		Type:      "session.requested",
		SessionID: "",
		Payload: map[string]any{
			"request": item,
		},
	})
}

func (api *v1API) handleListSessionRequests(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	box := strings.TrimSpace(r.URL.Query().Get("box"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	requests, err := api.store.ListSessionRequests(r.Context(), userID, box, status)
	if err != nil {
		api.logger.Error("list session requests failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]sessionRequestItem, 0, len(requests))
	for _, rr := range requests {
		items = append(items, sessionRequestItemFromRow(rr))
	}
	writeJSON(w, http.StatusOK, listSessionRequestsResponse{Requests: items})
}

func (api *v1API) handleAcceptSessionRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	api.handleMutateSessionRequest(w, r, requestID, "accept")
}

func (api *v1API) handleRejectSessionRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	api.handleMutateSessionRequest(w, r, requestID, "reject")
}

func (api *v1API) handleCancelSessionRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	api.handleMutateSessionRequest(w, r, requestID, "cancel")
}

func (api *v1API) handleMutateSessionRequest(w http.ResponseWriter, r *http.Request, requestID, action string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		writeAPIError(w, ErrCodeValidation, "invalid request id")
		return
	}

	nowMs := time.Now().UnixMilli()

	var (
		sr      storage.SessionRequestRow
		session *storage.SessionRow
		err     error
	)
	switch action {
	case "accept":
		sr, session, err = api.store.AcceptSessionRequest(r.Context(), requestID, userID, nowMs)
	case "reject":
		sr, err = api.store.RejectSessionRequest(r.Context(), requestID, userID, nowMs)
	case "cancel":
		sr, err = api.store.CancelSessionRequest(r.Context(), requestID, userID, nowMs)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionRequestNotFound, "session request not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionRequestAccessDenied, "access denied")
			return
		}
		if errors.Is(err, storage.ErrInvalidState) {
			writeAPIError(w, ErrCodeSessionRequestInvalidState, "invalid request state")
			return
		}
		api.logger.Error("mutate session request failed", "error", err, "action", action)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := sessionRequestItemFromRow(sr)
	resp := map[string]any{"request": item}
	if session != nil {
		resp["session"] = sessionItemFromRow(*session)
	}
	writeJSON(w, http.StatusOK, resp)

	eventType := map[string]string{
		"accept": "session.request.accepted",
		"reject": "session.request.rejected",
		"cancel": "session.request.canceled",
	}[action]

	payload := map[string]any{"request": item}
	if session != nil {
		payload["session"] = sessionItemFromRow(*session)
	}

	api.sendToUsers([]string{sr.RequesterID, sr.AddresseeID}, ws.Envelope{
		Type:      eventType,
		SessionID: "",
		Payload:   payload,
	})
}

func sessionRequestItemFromRow(sr storage.SessionRequestRow) sessionRequestItem {
	return sessionRequestItem{
		ID:          sr.ID,
		RequesterID: sr.RequesterID,
		AddresseeID: sr.AddresseeID,
		Status:      sr.Status,
		CreatedAtMs: sr.CreatedAtMs,
		UpdatedAtMs: sr.UpdatedAtMs,
	}
}
