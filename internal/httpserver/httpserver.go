package httpserver

import (
	"context"
	"net/http"

	"log/slog"

	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/ws"
)

type Store interface {
	Ready(ctx context.Context) error

	CreateUser(ctx context.Context, username, passwordHash, displayName string, nowMs int64) (storage.UserRow, error)
	GetUserByID(ctx context.Context, userID string) (storage.UserRow, error)
	GetUserByUsername(ctx context.Context, username string) (storage.UserRow, error)
	SearchUsers(ctx context.Context, query string, limit int) ([]storage.UserRow, error)
	UpdateUserDisplayName(ctx context.Context, userID, displayName string, nowMs int64) (storage.UserRow, error)

	CreateAuthToken(ctx context.Context, userID string, deviceInfo *string, nowMs, expiresAtMs int64) (storage.AuthTokenRow, error)
	ValidateToken(ctx context.Context, token string, nowMs int64) (storage.AuthTokenRow, error)
	DeleteToken(ctx context.Context, token string) error

	CreateSession(ctx context.Context, currentUserID, peerUserID string, nowMs int64) (storage.SessionRow, bool, error)
	GetSessionByID(ctx context.Context, sessionID string) (storage.SessionRow, error)
	ListSessionsForUser(ctx context.Context, userID, status string) ([]storage.SessionRow, error)
	ArchiveSession(ctx context.Context, sessionID, userID string, nowMs int64) (storage.SessionRow, error)
	ReactivateSession(ctx context.Context, sessionID, userID string, nowMs int64) (storage.SessionRow, error)
	HideSession(ctx context.Context, sessionID, userID string) error
	IsSessionParticipant(ctx context.Context, sessionID, userID string) (bool, error)
	GetPeerUserID(session storage.SessionRow, currentUserID string) string

	ListMessages(ctx context.Context, sessionID, userID string, limit int, beforeID string) ([]storage.MessageRow, bool, error)
	CreateMessage(ctx context.Context, sessionID, senderID, msgType string, text *string, meta *storage.MessageMeta, nowMs int64) (storage.MessageRow, error)

	CreateCall(ctx context.Context, callerID, calleeID, mediaType, groupID string, nowMs int64) (storage.CallRow, error)
	GetCallByID(ctx context.Context, callID string) (storage.CallRow, error)
	AcceptCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)
	RejectCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)
	CancelCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)
	EndCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)

	UpsertWeChatBinding(ctx context.Context, userID, openID, sessionKey string, unionID *string, nowMs int64) (storage.WeChatBindingRow, error)
	GetWeChatBindingByUserID(ctx context.Context, userID string) (storage.WeChatBindingRow, error)

	CreateSessionRequest(ctx context.Context, requesterID, addresseeID string, nowMs int64) (storage.SessionRequestRow, bool, error)
	ListSessionRequests(ctx context.Context, userID, box, status string) ([]storage.SessionRequestRow, error)
	AcceptSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (storage.SessionRequestRow, *storage.SessionRow, error)
	RejectSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (storage.SessionRequestRow, error)
	CancelSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (storage.SessionRequestRow, error)

	GetOrCreateSessionInvite(ctx context.Context, inviterID string, nowMs int64) (storage.SessionInviteRow, bool, error)
	ResolveSessionInvite(ctx context.Context, code string) (storage.SessionInviteRow, error)
}

type HandlerOptions struct {
	WeChatAppID                   string
	WeChatAppSecret               string
	WeChatCallSubscribeTemplateID string
	WeChatCallSubscribePage       string
}

func NewHandler(logger *slog.Logger, store Store, wsManager *ws.Manager, uploadDir string, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()
	api := newV1API(logger, store, wsManager, uploadDir, opts)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := store.Ready(r.Context()); err != nil {
			logger.Warn("ready check failed", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	mux.Handle("/v1/ws", wsManager.Handler())
	mux.HandleFunc("/v1/auth/", api.handleAuth)
	mux.HandleFunc("/v1/users", api.handleUsers)
	mux.HandleFunc("/v1/users/", api.handleUsers)
	mux.HandleFunc("/v1/sessions", api.handleSessions)
	mux.HandleFunc("/v1/sessions/", api.handleSessionSubroutes)
	mux.HandleFunc("/v1/calls", api.handleCalls)
	mux.HandleFunc("/v1/calls/", api.handleCallSubroutes)
	mux.HandleFunc("/v1/wechat/", api.handleWeChat)
	mux.HandleFunc("/v1/session-requests", api.handleSessionRequests)
	mux.HandleFunc("/v1/session-requests/", api.handleSessionRequestSubroutes)
	mux.HandleFunc("/v1/upload", api.handleUpload)

	// Serve uploaded files
	if uploadDir != "" {
		fs := http.FileServer(http.Dir(uploadDir))
		mux.Handle("/uploads/", http.StripPrefix("/uploads/", fs))
	}

	return chain(
		mux,
		recoverMiddleware(logger),
		requestLogMiddleware(logger),
		corsMiddleware(),
		authMiddleware(store),
	)
}
