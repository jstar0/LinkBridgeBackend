package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

type v1API struct {
	logger    *slog.Logger
	store     Store
	wsManager *ws.Manager
	uploadDir string
}

func newV1API(logger *slog.Logger, store Store, wsManager *ws.Manager, uploadDir string) *v1API {
	return &v1API{
		logger:    logger.With("component", "v1"),
		store:     store,
		wsManager: wsManager,
		uploadDir: uploadDir,
	}
}

type apiErrorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeAPIError(w http.ResponseWriter, code ErrorCode, message string) {
	writeJSON(w, httpStatusForCode(code), apiErrorEnvelope{
		Error: apiError{
			Code:    string(code),
			Message: message,
		},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected extra JSON input")
	}
	return nil
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func (api *v1API) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.handleListSessions(w, r)
	case http.MethodPost:
		api.handleCreateSession(w, r)
	default:
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (api *v1API) handleSessionSubroutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := splitPath(rest)
	if len(parts) != 2 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	sessionID := parts[0]
	switch parts[1] {
	case "archive":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleArchiveSession(w, r, sessionID)
	case "messages":
		switch r.Method {
		case http.MethodGet:
			api.handleListMessages(w, r, sessionID)
		case http.MethodPost:
			api.handleCreateMessage(w, r, sessionID)
		default:
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		}
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

type peerItem struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

type sessionListItem struct {
	ID              string    `json:"id"`
	Peer            peerItem  `json:"peer"`
	Status          string    `json:"status"`
	LastMessageText *string   `json:"lastMessageText,omitempty"`
	LastMessageAtMs *int64    `json:"lastMessageAtMs,omitempty"`
	UpdatedAtMs     int64     `json:"updatedAtMs"`
}

type listSessionsResponse struct {
	Sessions []sessionListItem `json:"sessions"`
}

func (api *v1API) handleListSessions(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != storage.SessionStatusActive && status != storage.SessionStatusArchived {
		writeAPIError(w, ErrCodeValidation, "invalid or missing status")
		return
	}

	sessions, err := api.store.ListSessionsForUser(r.Context(), userID, status)
	if err != nil {
		api.logger.Error("list sessions failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]sessionListItem, 0, len(sessions))
	for _, s := range sessions {
		peerUserID := api.store.GetPeerUserID(s, userID)
		peerUser, err := api.store.GetUserByID(r.Context(), peerUserID)
		if err != nil {
			api.logger.Warn("get peer user failed", "error", err, "peerUserID", peerUserID)
			continue
		}

		items = append(items, sessionListItem{
			ID: s.ID,
			Peer: peerItem{
				ID:          peerUser.ID,
				Username:    peerUser.Username,
				DisplayName: peerUser.DisplayName,
				AvatarURL:   peerUser.AvatarURL,
			},
			Status:          s.Status,
			LastMessageText: s.LastMessageText,
			LastMessageAtMs: s.LastMessageAtMs,
			UpdatedAtMs:     s.UpdatedAtMs,
		})
	}

	writeJSON(w, http.StatusOK, listSessionsResponse{Sessions: items})
}

type createSessionRequest struct {
	PeerUserID string `json:"peerUserId"`
}

type createSessionResponse struct {
	Session sessionListItem `json:"session"`
	Created bool            `json:"created"`
	Hint    string          `json:"hint,omitempty"`
}

func (api *v1API) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req createSessionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	req.PeerUserID = strings.TrimSpace(req.PeerUserID)
	if req.PeerUserID == "" {
		writeAPIError(w, ErrCodeValidation, "peerUserId is required")
		return
	}

	peerUser, err := api.store.GetUserByID(r.Context(), req.PeerUserID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeUserNotFound, "peer user not found")
			return
		}
		api.logger.Error("get peer user failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	nowMs := time.Now().UnixMilli()
	session, created, err := api.store.CreateSession(r.Context(), userID, req.PeerUserID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrCannotChatSelf) {
			writeAPIError(w, ErrCodeCannotChatSelf, "cannot create session with yourself")
			return
		}
		api.logger.Error("create session failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	resp := createSessionResponse{
		Session: sessionListItem{
			ID: session.ID,
			Peer: peerItem{
				ID:          peerUser.ID,
				Username:    peerUser.Username,
				DisplayName: peerUser.DisplayName,
				AvatarURL:   peerUser.AvatarURL,
			},
			Status:          session.Status,
			LastMessageText: session.LastMessageText,
			LastMessageAtMs: session.LastMessageAtMs,
			UpdatedAtMs:     session.UpdatedAtMs,
		},
		Created: created,
	}
	if !created {
		resp.Hint = "会话已存在"
	}

	writeJSON(w, http.StatusOK, resp)

	if created {
		api.broadcast(ws.Envelope{
			Type:      "session.created",
			SessionID: session.ID,
			Payload: map[string]any{
				"session": resp.Session,
			},
		})
	}
}

type archiveSessionResponse struct {
	Session sessionArchiveItem `json:"session"`
}

type sessionArchiveItem struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
}

func (api *v1API) handleArchiveSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeAPIError(w, ErrCodeValidation, "invalid sessionId")
		return
	}

	nowMs := time.Now().UnixMilli()
	session, err := api.store.ArchiveSession(r.Context(), sessionID, userID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		api.logger.Error("archive session failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, archiveSessionResponse{
		Session: sessionArchiveItem{
			ID:          session.ID,
			Status:      session.Status,
			UpdatedAtMs: session.UpdatedAtMs,
		},
	})

	api.broadcast(ws.Envelope{
		Type:      "session.archived",
		SessionID: session.ID,
		Payload: map[string]any{
			"session": sessionArchiveItem{
				ID:          session.ID,
				Status:      session.Status,
				UpdatedAtMs: session.UpdatedAtMs,
			},
		},
	})
}

type listMessagesResponse struct {
	Messages []messageItem `json:"messages"`
	HasMore  bool          `json:"hasMore"`
}

type messageItem struct {
	ID          string               `json:"id"`
	SessionID   string               `json:"sessionId"`
	Sender      string               `json:"sender"`
	SenderID    string               `json:"senderId"`
	Type        string               `json:"type"`
	Text        string               `json:"text,omitempty"`
	Meta        *storage.MessageMeta `json:"meta,omitempty"`
	CreatedAtMs int64                `json:"createdAtMs"`
}

func (api *v1API) handleListMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeAPIError(w, ErrCodeValidation, "invalid sessionId")
		return
	}

	beforeID := r.URL.Query().Get("before")
	limit := 50

	messages, hasMore, err := api.store.ListMessages(r.Context(), sessionID, userID, limit, beforeID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		api.logger.Error("list messages failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]messageItem, 0, len(messages))
	for _, m := range messages {
		sender := "peer"
		if m.SenderID == userID {
			sender = "me"
		}

		item := messageItem{
			ID:          m.ID,
			SessionID:   m.SessionID,
			Sender:      sender,
			SenderID:    m.SenderID,
			Type:        m.Type,
			CreatedAtMs: m.CreatedAtMs,
		}
		if m.Text != nil {
			item.Text = *m.Text
		}
		if meta := parseMeta(m.MetaJSON); meta != nil {
			item.Meta = meta
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, listMessagesResponse{Messages: items, HasMore: hasMore})
}

type createMessageRequest struct {
	Type string               `json:"type"`
	Text string               `json:"text,omitempty"`
	Meta *storage.MessageMeta `json:"meta,omitempty"`
}

type createMessageResponse struct {
	Message messageItem `json:"message"`
}

func (api *v1API) handleCreateMessage(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeAPIError(w, ErrCodeValidation, "invalid sessionId")
		return
	}

	var req createMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	req.Type = strings.TrimSpace(req.Type)
	switch req.Type {
	case storage.MessageTypeText, storage.MessageTypeImage, storage.MessageTypeFile, storage.MessageTypeSystem:
	default:
		writeAPIError(w, ErrCodeValidation, "invalid message type")
		return
	}

	var text *string
	if req.Type == storage.MessageTypeText {
		req.Text = strings.TrimSpace(req.Text)
		if req.Text == "" {
			writeAPIError(w, ErrCodeValidation, "text is required for type text")
			return
		}
		text = &req.Text
	}

	nowMs := time.Now().UnixMilli()
	msg, err := api.store.CreateMessage(r.Context(), sessionID, userID, req.Type, text, req.Meta, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		api.logger.Error("create message failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := messageItem{
		ID:          msg.ID,
		SessionID:   msg.SessionID,
		Sender:      "me",
		SenderID:    msg.SenderID,
		Type:        msg.Type,
		CreatedAtMs: msg.CreatedAtMs,
	}
	if msg.Text != nil {
		item.Text = *msg.Text
	}
	if meta := parseMeta(msg.MetaJSON); meta != nil {
		item.Meta = meta
	}

	writeJSON(w, http.StatusOK, createMessageResponse{Message: item})

	api.broadcast(ws.Envelope{
		Type:      "message.created",
		SessionID: msg.SessionID,
		Payload: map[string]any{
			"message": item,
		},
	})
}

func parseMeta(b []byte) *storage.MessageMeta {
	if len(b) == 0 {
		return nil
	}
	var meta storage.MessageMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil
	}
	if meta.Name == "" && meta.SizeBytes == 0 {
		return nil
	}
	return &meta
}

func (api *v1API) broadcast(env ws.Envelope) {
	if api.wsManager == nil {
		return
	}
	api.wsManager.Broadcast(env)
}
