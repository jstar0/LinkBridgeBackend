package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"log/slog"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/wechat"
	"linkbridge-backend/internal/ws"
)

type v1API struct {
	logger    *slog.Logger
	store     Store
	wsManager *ws.Manager
	uploadDir string

	wechatClient                      *wechat.Client
	wechatAppID                       string
	wechatCallSubscribeTemplateID     string
	wechatCallSubscribePage           string
	wechatActivitySubscribeTemplateID string
	wechatActivitySubscribePage       string
}

func newV1API(logger *slog.Logger, store Store, wsManager *ws.Manager, uploadDir string, opts HandlerOptions) *v1API {
	var wc *wechat.Client
	if strings.TrimSpace(opts.WeChatAppID) != "" && strings.TrimSpace(opts.WeChatAppSecret) != "" {
		wc = wechat.NewClient(logger, opts.WeChatAppID, opts.WeChatAppSecret)
	}
	return &v1API{
		logger:                            logger.With("component", "v1"),
		store:                             store,
		wsManager:                         wsManager,
		uploadDir:                         uploadDir,
		wechatClient:                      wc,
		wechatAppID:                       strings.TrimSpace(opts.WeChatAppID),
		wechatCallSubscribeTemplateID:     strings.TrimSpace(opts.WeChatCallSubscribeTemplateID),
		wechatCallSubscribePage:           strings.TrimSpace(opts.WeChatCallSubscribePage),
		wechatActivitySubscribeTemplateID: strings.TrimSpace(opts.WeChatActivitySubscribeTemplateID),
		wechatActivitySubscribePage:       strings.TrimSpace(opts.WeChatActivitySubscribePage),
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
	case "reactivate":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleReactivateSession(w, r, sessionID)
	case "hide":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleHideSession(w, r, sessionID)
	case "relationship":
		switch r.Method {
		case http.MethodGet:
			api.handleGetSessionRelationship(w, r, sessionID)
		case http.MethodPut:
			api.handleUpsertSessionRelationship(w, r, sessionID)
		default:
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		}
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
	ID              string                   `json:"id"`
	Peer            peerItem                 `json:"peer"`
	Status          string                   `json:"status"`
	Source          string                   `json:"source"`
	LastMessageText *string                  `json:"lastMessageText,omitempty"`
	LastMessageAtMs *int64                   `json:"lastMessageAtMs,omitempty"`
	UpdatedAtMs     int64                    `json:"updatedAtMs"`
	Relationship    *relationshipSummaryItem `json:"relationship,omitempty"`
}

type relationshipSummaryItem struct {
	Note        *string  `json:"note,omitempty"`
	GroupID     *string  `json:"groupId,omitempty"`
	GroupName   *string  `json:"groupName,omitempty"`
	Tags        []string `json:"tags"`
	UpdatedAtMs int64    `json:"updatedAtMs"`
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

		item := sessionListItem{
			ID: s.ID,
			Peer: peerItem{
				ID:          peerUser.ID,
				Username:    peerUser.Username,
				DisplayName: peerUser.DisplayName,
				AvatarURL:   peerUser.AvatarURL,
			},
			Status:          s.Status,
			Source:          s.Source,
			LastMessageText: s.LastMessageText,
			LastMessageAtMs: s.LastMessageAtMs,
			UpdatedAtMs:     s.UpdatedAtMs,
		}

		if meta, err := api.store.GetSessionUserMeta(r.Context(), s.ID, userID); err == nil {
			item.Relationship = &relationshipSummaryItem{
				Note:        meta.Note,
				GroupID:     meta.GroupID,
				GroupName:   meta.GroupName,
				Tags:        storage.ParseTagsJSON(meta.TagsJSON),
				UpdatedAtMs: meta.UpdatedAtMs,
			}
		}

		items = append(items, item)
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

	// 如果会话已存在且是归档状态，自动激活它
	if !created && session.Status == storage.SessionStatusArchived {
		session, err = api.store.ReactivateSession(r.Context(), session.ID, userID, nowMs)
		if err != nil {
			api.logger.Error("reactivate session failed", "error", err)
			writeAPIError(w, ErrCodeInternal, "internal error")
			return
		}
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
			Source:          session.Source,
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

func (api *v1API) handleReactivateSession(w http.ResponseWriter, r *http.Request, sessionID string) {
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
	session, err := api.store.ReactivateSession(r.Context(), sessionID, userID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		if errors.Is(err, storage.ErrInvalidState) {
			writeAPIError(w, ErrCodeValidation, "session is not archived")
			return
		}
		api.logger.Error("reactivate session failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id":              session.ID,
			"status":          session.Status,
			"updatedAtMs":     session.UpdatedAtMs,
			"reactivatedAtMs": session.ReactivatedAtMs,
		},
	})

	api.broadcast(ws.Envelope{
		Type:      "session.reactivated",
		SessionID: session.ID,
		Payload: map[string]any{
			"session": map[string]any{
				"id":              session.ID,
				"status":          session.Status,
				"updatedAtMs":     session.UpdatedAtMs,
				"reactivatedAtMs": session.ReactivatedAtMs,
			},
		},
	})
}

func (api *v1API) handleHideSession(w http.ResponseWriter, r *http.Request, sessionID string) {
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

	err := api.store.HideSession(r.Context(), sessionID, userID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		api.logger.Error("hide session failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
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
	MetaJSON    json.RawMessage      `json:"metaJson,omitempty"`
	Burn        *burnStateItem       `json:"burn,omitempty"`
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

	var burnMinCreatedAtMs int64
	if tokenRow, ok := getAuthTokenFromContext(r.Context()); ok {
		burnMinCreatedAtMs = tokenRow.CreatedAtMs
	}

	filtered := make([]storage.MessageRow, 0, len(messages))
	for _, m := range messages {
		if m.Type == storage.MessageTypeBurn && burnMinCreatedAtMs > 0 && m.CreatedAtMs < burnMinCreatedAtMs {
			continue
		}
		filtered = append(filtered, m)
	}

	burnIDs := make([]string, 0, 8)
	for _, m := range filtered {
		if m.Type == storage.MessageTypeBurn {
			burnIDs = append(burnIDs, m.ID)
		}
	}
	burnByID := map[string]storage.BurnMessageRow{}
	if len(burnIDs) > 0 {
		burnByID, err = api.store.GetBurnMessages(r.Context(), burnIDs)
		if err != nil {
			api.logger.Error("get burn messages failed", "error", err)
			writeAPIError(w, ErrCodeInternal, "internal error")
			return
		}
	}

	items := make([]messageItem, 0, len(filtered))
	for _, m := range filtered {
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
		if m.Type == storage.MessageTypeBurn && len(m.MetaJSON) > 0 {
			item.MetaJSON = json.RawMessage(m.MetaJSON)
		}
		if meta := parseMeta(m.MetaJSON); meta != nil {
			item.Meta = meta
		}
		if m.Type == storage.MessageTypeBurn {
			if burn, ok := burnByID[m.ID]; ok {
				item.Burn = &burnStateItem{
					BurnAfterMs: burn.BurnAfterMs,
					OpenedAtMs:  burn.OpenedAtMs,
					BurnAtMs:    burn.BurnAtMs,
				}
			}
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, listMessagesResponse{Messages: items, HasMore: hasMore})
}

type createMessageRequest struct {
	Type        string               `json:"type"`
	Text        string               `json:"text,omitempty"`
	Meta        *storage.MessageMeta `json:"meta,omitempty"`
	MetaJSON    json.RawMessage      `json:"metaJson,omitempty"`
	BurnAfterMs *int64               `json:"burnAfterMs,omitempty"`
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
	case storage.MessageTypeText, storage.MessageTypeImage, storage.MessageTypeFile, storage.MessageTypeSystem, storage.MessageTypeBurn:
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
	var (
		msg     storage.MessageRow
		burnRow storage.BurnMessageRow
		err     error
	)
	if req.Type == storage.MessageTypeBurn {
		if req.BurnAfterMs == nil || *req.BurnAfterMs <= 0 {
			writeAPIError(w, ErrCodeValidation, "burnAfterMs is required for type burn")
			return
		}
		meta := []byte(strings.TrimSpace(string(req.MetaJSON)))
		if len(meta) == 0 {
			writeAPIError(w, ErrCodeValidation, "metaJson is required for type burn")
			return
		}
		var v any
		if err := json.Unmarshal(meta, &v); err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid metaJson")
			return
		}
		if _, ok := v.(map[string]any); !ok {
			writeAPIError(w, ErrCodeValidation, "metaJson must be a JSON object")
			return
		}

		msg, burnRow, err = api.store.CreateBurnMessage(r.Context(), sessionID, userID, meta, *req.BurnAfterMs, nowMs)
	} else {
		msg, err = api.store.CreateMessage(r.Context(), sessionID, userID, req.Type, text, req.Meta, nowMs)
	}
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeSessionNotFound, "session not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		if errors.Is(err, storage.ErrSessionArchived) {
			writeAPIError(w, ErrCodeSessionArchived, "session is archived")
			return
		}
		if errors.Is(err, storage.ErrInvalidState) {
			writeAPIError(w, ErrCodeValidation, "invalid session state")
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
	if msg.Type == storage.MessageTypeBurn && len(msg.MetaJSON) > 0 {
		item.MetaJSON = json.RawMessage(msg.MetaJSON)
		item.Burn = &burnStateItem{
			BurnAfterMs: burnRow.BurnAfterMs,
			OpenedAtMs:  burnRow.OpenedAtMs,
			BurnAtMs:    burnRow.BurnAtMs,
		}
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

func (api *v1API) sendToUser(userID string, env ws.Envelope) {
	if api.wsManager == nil || strings.TrimSpace(userID) == "" {
		return
	}
	api.wsManager.SendToUser(userID, env)
}

func (api *v1API) sendToUsers(userIDs []string, env ws.Envelope) {
	if api.wsManager == nil || len(userIDs) == 0 {
		return
	}
	api.wsManager.SendToUsers(userIDs, env)
}

type wechatVoipSignResponse struct {
	GroupID   string `json:"groupId"`
	NonceStr  string `json:"nonceStr"`
	TimeStamp int64  `json:"timeStamp"`
	Signature string `json:"signature"`
	RoomType  string `json:"roomType"`
}

func computeVoipSignature(appID, groupID, nonceStr string, timeStamp int64, sessionKey string) string {
	parts := []string{
		strings.TrimSpace(appID),
		strings.TrimSpace(groupID),
		strings.TrimSpace(nonceStr),
		fmt.Sprintf("%d", timeStamp),
	}
	sort.Strings(parts)
	msg := strings.Join(parts, "")

	mac := hmac.New(sha256.New, []byte(sessionKey))
	_, _ = mac.Write([]byte(msg))
	return fmt.Sprintf("%x", mac.Sum(nil))
}
