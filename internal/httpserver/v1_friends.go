package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

type friendUserItem struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

type listFriendsResponse struct {
	Friends []friendUserItem `json:"friends"`
}

type createFriendRequestRequest struct {
	UserID string `json:"userId"`
}

type consumeFriendInviteRequest struct {
	Code string `json:"code"`
}

type friendRequestItem struct {
	ID          string `json:"id"`
	RequesterID string `json:"requesterId"`
	AddresseeID string `json:"addresseeId"`
	Status      string `json:"status"`
	CreatedAtMs int64  `json:"createdAtMs"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
}

type createFriendRequestResponse struct {
	Request friendRequestItem `json:"request"`
	Created bool              `json:"created"`
	Hint    string            `json:"hint,omitempty"`
}

type listFriendRequestsResponse struct {
	Requests []friendRequestItem `json:"requests"`
}

func (api *v1API) handleFriends(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.handleListFriends(w, r)
	case http.MethodPost:
		api.handleCreateFriendRequest(w, r)
	default:
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (api *v1API) handleFriendSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/friends/")
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
		api.handleConsumeFriendInvite(w, r)
		return
	}

	if len(parts) == 1 && parts[0] == "requests" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleListFriendRequests(w, r)
		return
	}

	if len(parts) != 3 || parts[0] != "requests" {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	requestID := parts[1]
	action := parts[2]
	if r.Method != http.MethodPost {
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	switch action {
	case "accept":
		api.handleAcceptFriendRequest(w, r, requestID)
	case "reject":
		api.handleRejectFriendRequest(w, r, requestID)
	case "cancel":
		api.handleCancelFriendRequest(w, r, requestID)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleListFriends(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	friends, err := api.store.ListFriends(r.Context(), userID)
	if err != nil {
		api.logger.Error("list friends failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]friendUserItem, 0, len(friends))
	for _, u := range friends {
		items = append(items, friendUserItem{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			AvatarURL:   u.AvatarURL,
		})
	}

	writeJSON(w, http.StatusOK, listFriendsResponse{Friends: items})
}

func (api *v1API) handleCreateFriendRequest(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req createFriendRequestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" {
		writeAPIError(w, ErrCodeValidation, "userId is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	fr, created, err := api.store.CreateFriendRequest(r.Context(), userID, req.UserID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeValidation, "cannot add self")
			return
		}
		if errors.Is(err, storage.ErrAlreadyFriends) {
			writeAPIError(w, ErrCodeAlreadyFriends, "already friends")
			return
		}
		if errors.Is(err, storage.ErrRequestExists) {
			writeAPIError(w, ErrCodeFriendRequestExists, "friend request exists")
			return
		}
		api.logger.Error("create friend request failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := friendRequestItemFromRow(fr)
	hint := ""
	if !created {
		hint = "request updated"
	}
	writeJSON(w, http.StatusOK, createFriendRequestResponse{Request: item, Created: created, Hint: hint})

	api.sendToUser(fr.AddresseeID, ws.Envelope{
		Type:      "friend.requested",
		SessionID: "",
		Payload: map[string]any{
			"request": item,
		},
	})
}

func (api *v1API) handleConsumeFriendInvite(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req consumeFriendInviteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		writeAPIError(w, ErrCodeValidation, "code is required")
		return
	}

	invite, err := api.store.ResolveFriendInvite(r.Context(), req.Code)
	if err != nil {
		if errors.Is(err, storage.ErrInviteInvalid) || errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeFriendInviteInvalid, "invalid invite")
			return
		}
		api.logger.Error("resolve friend invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	nowMs := time.Now().UnixMilli()
	fr, created, err := api.store.CreateFriendRequest(r.Context(), userID, invite.InviterID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeValidation, "cannot add self")
			return
		}
		if errors.Is(err, storage.ErrAlreadyFriends) {
			writeAPIError(w, ErrCodeAlreadyFriends, "already friends")
			return
		}
		if errors.Is(err, storage.ErrRequestExists) {
			writeAPIError(w, ErrCodeFriendRequestExists, "friend request exists")
			return
		}
		api.logger.Error("create friend request from invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := friendRequestItemFromRow(fr)
	hint := ""
	if !created {
		hint = "request updated"
	}
	writeJSON(w, http.StatusOK, createFriendRequestResponse{Request: item, Created: created, Hint: hint})

	api.sendToUser(fr.AddresseeID, ws.Envelope{
		Type:      "friend.requested",
		SessionID: "",
		Payload: map[string]any{
			"request": item,
		},
	})
}

func (api *v1API) handleListFriendRequests(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	box := strings.TrimSpace(r.URL.Query().Get("box"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	requests, err := api.store.ListFriendRequests(r.Context(), userID, box, status)
	if err != nil {
		api.logger.Error("list friend requests failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]friendRequestItem, 0, len(requests))
	for _, rr := range requests {
		items = append(items, friendRequestItemFromRow(rr))
	}
	writeJSON(w, http.StatusOK, listFriendRequestsResponse{Requests: items})
}

func (api *v1API) handleAcceptFriendRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	api.handleMutateFriendRequest(w, r, requestID, "accept")
}

func (api *v1API) handleRejectFriendRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	api.handleMutateFriendRequest(w, r, requestID, "reject")
}

func (api *v1API) handleCancelFriendRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	api.handleMutateFriendRequest(w, r, requestID, "cancel")
}

func (api *v1API) handleMutateFriendRequest(w http.ResponseWriter, r *http.Request, requestID, action string) {
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
		fr  storage.FriendRequestRow
		err error
	)
	switch action {
	case "accept":
		fr, err = api.store.AcceptFriendRequest(r.Context(), requestID, userID, nowMs)
	case "reject":
		fr, err = api.store.RejectFriendRequest(r.Context(), requestID, userID, nowMs)
	case "cancel":
		fr, err = api.store.CancelFriendRequest(r.Context(), requestID, userID, nowMs)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeFriendRequestNotFound, "friend request not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeFriendRequestAccessDenied, "access denied")
			return
		}
		if errors.Is(err, storage.ErrInvalidState) {
			writeAPIError(w, ErrCodeFriendRequestInvalidState, "invalid request state")
			return
		}
		api.logger.Error("mutate friend request failed", "error", err, "action", action)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := friendRequestItemFromRow(fr)
	writeJSON(w, http.StatusOK, map[string]any{"request": item})

	eventType := map[string]string{
		"accept": "friend.accepted",
		"reject": "friend.rejected",
		"cancel": "friend.canceled",
	}[action]

	api.sendToUsers([]string{fr.RequesterID, fr.AddresseeID}, ws.Envelope{
		Type:      eventType,
		SessionID: "",
		Payload: map[string]any{
			"request": item,
		},
	})
}

func friendRequestItemFromRow(fr storage.FriendRequestRow) friendRequestItem {
	return friendRequestItem{
		ID:          fr.ID,
		RequesterID: fr.RequesterID,
		AddresseeID: fr.AddresseeID,
		Status:      fr.Status,
		CreatedAtMs: fr.CreatedAtMs,
		UpdatedAtMs: fr.UpdatedAtMs,
	}
}
