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
	IsSessionParticipant(ctx context.Context, sessionID, userID string) (bool, error)
	GetPeerUserID(session storage.SessionRow, currentUserID string) string

	ListMessages(ctx context.Context, sessionID, userID string, limit int, beforeID string) ([]storage.MessageRow, bool, error)
	CreateMessage(ctx context.Context, sessionID, senderID, msgType string, text *string, meta *storage.MessageMeta, nowMs int64) (storage.MessageRow, error)
}

func NewHandler(logger *slog.Logger, store Store, wsManager *ws.Manager, uploadDir string) http.Handler {
	mux := http.NewServeMux()
	api := newV1API(logger, store, wsManager, uploadDir)

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
