package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

type burnStateItem struct {
	BurnAfterMs int64  `json:"burnAfterMs"`
	OpenedAtMs  *int64 `json:"openedAtMs,omitempty"`
	BurnAtMs    *int64 `json:"burnAtMs,omitempty"`
}

type burnMessageReadResponse struct {
	MessageID string        `json:"messageId"`
	SessionID string        `json:"sessionId"`
	Burn      burnStateItem `json:"burn"`
	Started   bool          `json:"started"`
}

func (api *v1API) handleBurnMessages(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/burn-messages/")
	parts := splitPath(rest)
	if len(parts) != 2 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	messageID := parts[0]
	switch parts[1] {
	case "read":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleReadBurnMessage(w, r, messageID)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleReadBurnMessage(w http.ResponseWriter, r *http.Request, messageID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		writeAPIError(w, ErrCodeValidation, "messageId is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	row, started, err := api.store.MarkBurnMessageRead(r.Context(), messageID, userID, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeMessageNotFound, "message not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeSessionAccessDenied, "access denied")
			return
		}
		api.logger.Error("mark burn message read failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	resp := burnMessageReadResponse{
		MessageID: row.MessageID,
		SessionID: row.SessionID,
		Burn: burnStateItem{
			BurnAfterMs: row.BurnAfterMs,
			OpenedAtMs:  row.OpenedAtMs,
			BurnAtMs:    row.BurnAtMs,
		},
		Started: started,
	}
	writeJSON(w, http.StatusOK, resp)

	if started {
		api.sendToUsers([]string{row.SenderID, row.RecipientID}, ws.Envelope{
			Type:      "message.burn.read",
			SessionID: row.SessionID,
			Payload: map[string]any{
				"messageId":    row.MessageID,
				"burn":         resp.Burn,
				"readerUserId": userID,
			},
		})
	}
}
