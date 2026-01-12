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
	Code  string   `json:"code"`
	AtLat *float64 `json:"atLat,omitempty"`
	AtLng *float64 `json:"atLng,omitempty"`
}

type sessionRequestItem struct {
	ID                  string  `json:"id"`
	RequesterID         string  `json:"requesterId"`
	AddresseeID         string  `json:"addresseeId"`
	Status              string  `json:"status"`
	Source              string  `json:"source"`
	VerificationMessage *string `json:"verificationMessage,omitempty"`
	CreatedAtMs         int64   `json:"createdAtMs"`
	UpdatedAtMs         int64   `json:"updatedAtMs"`
	LastOpenedAtMs      int64   `json:"lastOpenedAtMs"`
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
	switch r.Method {
	case http.MethodGet:
		api.handleListSessionRequests(w, r)
	case http.MethodPost:
		api.handleCreateSessionRequest(w, r)
	default:
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
	}
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

	var (
		atLatE7 *int64
		atLngE7 *int64
	)
	if req.AtLat != nil {
		if *req.AtLat < -90 || *req.AtLat > 90 {
			writeAPIError(w, ErrCodeValidation, "invalid atLat range")
			return
		}
		v := floatToE7(*req.AtLat)
		atLatE7 = &v
	}
	if req.AtLng != nil {
		if *req.AtLng < -180 || *req.AtLng > 180 {
			writeAPIError(w, ErrCodeValidation, "invalid atLng range")
			return
		}
		v := floatToE7(*req.AtLng)
		atLngE7 = &v
	}

	nowMs := time.Now().UnixMilli()
	invite, err := api.store.ConsumeSessionInvite(r.Context(), req.Code, atLatE7, atLngE7, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrInviteInvalid) || errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionInviteInvalid, "invalid invite")
			return
		}
		if errors.Is(err, storage.ErrInviteExpired) {
			writeAPIError(w, ErrCodeInviteExpired, "invite expired")
			return
		}
		if errors.Is(err, storage.ErrGeoFenceRequired) {
			writeAPIError(w, ErrCodeGeoFenceRequired, "location required")
			return
		}
		if errors.Is(err, storage.ErrGeoFenceForbidden) {
			writeAPIError(w, ErrCodeGeoFenceForbidden, "outside allowed area")
			return
		}
		api.logger.Error("consume session invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	sr, created, err := api.store.CreateSessionRequest(r.Context(), userID, invite.InviterID, storage.SessionRequestSourceWeChatCode, nil, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeValidation, "cannot add self")
			return
		}
		if errors.Is(err, storage.ErrSessionExists) {
			writeAPIError(w, ErrCodeSessionExists, "session already exists")
			return
		}
		if errors.Is(err, storage.ErrRequestExists) {
			writeAPIError(w, ErrCodeSessionRequestExists, "session request exists")
			return
		}
		if errors.Is(err, storage.ErrRateLimited) {
			writeAPIError(w, ErrCodeRateLimited, "rate limited")
			return
		}
		if errors.Is(err, storage.ErrCooldownActive) {
			writeAPIError(w, ErrCodeCooldownActive, "cooldown active")
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

type createSessionRequestRequest struct {
	AddresseeID         string  `json:"addresseeId"`
	VerificationMessage *string `json:"verificationMessage,omitempty"`
}

func (api *v1API) handleCreateSessionRequest(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req createSessionRequestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	req.AddresseeID = strings.TrimSpace(req.AddresseeID)
	if req.AddresseeID == "" {
		writeAPIError(w, ErrCodeValidation, "addresseeId is required")
		return
	}
	if req.VerificationMessage != nil {
		msg := strings.TrimSpace(*req.VerificationMessage)
		if msg == "" {
			req.VerificationMessage = nil
		} else {
			req.VerificationMessage = &msg
		}
	}

	nowMs := time.Now().UnixMilli()
	sr, created, err := api.store.CreateSessionRequest(r.Context(), userID, req.AddresseeID, storage.SessionRequestSourceMap, req.VerificationMessage, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeValidation, "cannot add self")
			return
		}
		if errors.Is(err, storage.ErrSessionExists) {
			writeAPIError(w, ErrCodeSessionExists, "session already exists")
			return
		}
		if errors.Is(err, storage.ErrRequestExists) {
			writeAPIError(w, ErrCodeSessionRequestExists, "session request exists")
			return
		}
		if errors.Is(err, storage.ErrRateLimited) {
			writeAPIError(w, ErrCodeRateLimited, "rate limited")
			return
		}
		if errors.Is(err, storage.ErrCooldownActive) {
			writeAPIError(w, ErrCodeCooldownActive, "cooldown active")
			return
		}
		api.logger.Error("create session request failed", "error", err)
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
		ID:                  sr.ID,
		RequesterID:         sr.RequesterID,
		AddresseeID:         sr.AddresseeID,
		Status:              sr.Status,
		Source:              sr.Source,
		VerificationMessage: sr.VerificationMessage,
		CreatedAtMs:         sr.CreatedAtMs,
		UpdatedAtMs:         sr.UpdatedAtMs,
		LastOpenedAtMs:      sr.LastOpenedAtMs,
	}
}
