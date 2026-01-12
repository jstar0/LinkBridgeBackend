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
	UpdateUserAvatarURL(ctx context.Context, userID string, avatarURL *string, nowMs int64) (storage.UserRow, error)

	CreateAuthToken(ctx context.Context, userID string, deviceInfo *string, nowMs, expiresAtMs int64) (storage.AuthTokenRow, error)
	ValidateToken(ctx context.Context, token string, nowMs int64) (storage.AuthTokenRow, error)
	DeleteToken(ctx context.Context, token string) error

	CreateSession(ctx context.Context, currentUserID, peerUserID string, nowMs int64) (storage.SessionRow, bool, error)
	GetSessionByID(ctx context.Context, sessionID string) (storage.SessionRow, error)
	ListSessionsForUser(ctx context.Context, userID, status string) ([]storage.SessionRow, error)
	ArchiveSession(ctx context.Context, sessionID, userID string, nowMs int64) (storage.SessionRow, error)
	ReactivateSession(ctx context.Context, sessionID, userID string, nowMs int64) (storage.SessionRow, error)
	ReactivateSessionByParticipants(ctx context.Context, user1ID, user2ID string, nowMs int64) (storage.SessionRow, error)
	HideSession(ctx context.Context, sessionID, userID string) error
	IsSessionParticipant(ctx context.Context, sessionID, userID string) (bool, error)
	GetPeerUserID(session storage.SessionRow, currentUserID string) string

	ListMessages(ctx context.Context, sessionID, userID string, limit int, beforeID string) ([]storage.MessageRow, bool, error)
	CreateMessage(ctx context.Context, sessionID, senderID, msgType string, text *string, meta *storage.MessageMeta, nowMs int64) (storage.MessageRow, error)
	CreateBurnMessage(ctx context.Context, sessionID, senderID string, metaJSON []byte, burnAfterMs int64, nowMs int64) (storage.MessageRow, storage.BurnMessageRow, error)
	GetBurnMessages(ctx context.Context, messageIDs []string) (map[string]storage.BurnMessageRow, error)
	MarkBurnMessageRead(ctx context.Context, messageID, userID string, nowMs int64) (storage.BurnMessageRow, bool, error)

	CreateCall(ctx context.Context, callerID, calleeID, mediaType, groupID string, nowMs int64) (storage.CallRow, error)
	GetCallByID(ctx context.Context, callID string) (storage.CallRow, error)
	AcceptCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)
	RejectCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)
	CancelCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)
	EndCall(ctx context.Context, callID, userID string, nowMs int64) (storage.CallRow, error)

	UpsertWeChatBinding(ctx context.Context, userID, openID, sessionKey string, unionID *string, nowMs int64) (storage.WeChatBindingRow, error)
	GetWeChatBindingByUserID(ctx context.Context, userID string) (storage.WeChatBindingRow, error)

	CreateSessionRequest(ctx context.Context, requesterID, addresseeID, source string, verificationMessage *string, nowMs int64) (storage.SessionRequestRow, bool, error)
	ListSessionRequests(ctx context.Context, userID, box, status string) ([]storage.SessionRequestRow, error)
	AcceptSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (storage.SessionRequestRow, *storage.SessionRow, error)
	RejectSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (storage.SessionRequestRow, error)
	CancelSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (storage.SessionRequestRow, error)

	GetOrCreateSessionInvite(ctx context.Context, inviterID string, nowMs int64) (storage.SessionInviteRow, bool, error)
	ResolveSessionInvite(ctx context.Context, code string) (storage.SessionInviteRow, error)
	ConsumeSessionInvite(ctx context.Context, code string, atLatE7, atLngE7 *int64, nowMs int64) (storage.SessionInviteRow, error)
	UpdateSessionInviteSettings(ctx context.Context, inviterID string, expiresAtMs *int64, geoFence *storage.GeoFence, nowMs int64) (storage.SessionInviteRow, error)

	GetHomeBase(ctx context.Context, userID string) (storage.HomeBaseRow, error)
	UpsertHomeBase(ctx context.Context, userID string, latE7, lngE7 int64, visibilityRadiusM *int, nowMs int64) (storage.HomeBaseRow, error)

	CreateLocalFeedPost(ctx context.Context, userID string, text *string, imageURLs []string, expiresAtMs int64, isPinned bool, nowMs int64) (storage.LocalFeedPostRow, []storage.LocalFeedPostImageRow, error)
	DeleteLocalFeedPost(ctx context.Context, userID, postID string) error
	ListLocalFeedPostsForSource(ctx context.Context, sourceUserID string, atLatE7, atLngE7 *int64, nowMs int64, limit int) ([]storage.LocalFeedPostWithImages, error)
	ListLocalFeedPins(ctx context.Context, minLatE7, maxLatE7, minLngE7, maxLngE7, centerLatE7, centerLngE7 int64, limit int) ([]storage.LocalFeedPinRow, error)

	GetUserCardProfile(ctx context.Context, userID string) (storage.UserProfileRow, error)
	UpsertUserCardProfile(ctx context.Context, userID string, nicknameOverride, avatarURLOverride *string, profileJSON string, nowMs int64) (storage.UserProfileRow, error)
	GetUserMapProfile(ctx context.Context, userID string) (storage.UserProfileRow, error)
	UpsertUserMapProfile(ctx context.Context, userID string, nicknameOverride, avatarURLOverride *string, profileJSON string, nowMs int64) (storage.UserProfileRow, error)

	ListRelationshipGroups(ctx context.Context, userID string) ([]storage.RelationshipGroupRow, error)
	GetRelationshipGroupByID(ctx context.Context, userID, groupID string) (storage.RelationshipGroupRow, error)
	CreateRelationshipGroup(ctx context.Context, userID, name string, nowMs int64) (storage.RelationshipGroupRow, bool, error)
	RenameRelationshipGroup(ctx context.Context, userID, groupID, name string, nowMs int64) (storage.RelationshipGroupRow, error)
	DeleteRelationshipGroup(ctx context.Context, userID, groupID string) error

	GetSessionUserMeta(ctx context.Context, sessionID, userID string) (storage.SessionUserMetaRow, error)
	UpsertSessionUserMeta(ctx context.Context, sessionID, userID string, note *string, groupID *string, tags []string, nowMs int64) (storage.SessionUserMetaRow, error)

	CreateActivity(ctx context.Context, creatorID, title string, description *string, startAtMs, endAtMs *int64, nowMs int64) (storage.ActivityRow, storage.ActivityInviteRow, error)
	GetActivityByID(ctx context.Context, activityID string) (storage.ActivityRow, error)
	GetOrCreateActivityInvite(ctx context.Context, activityID string, nowMs int64) (storage.ActivityInviteRow, bool, error)
	UpdateActivityInviteSettings(ctx context.Context, activityID string, expiresAtMs *int64, geoFence *storage.GeoFence, nowMs int64) (storage.ActivityInviteRow, error)
	ConsumeActivityInvite(ctx context.Context, userID, code string, atLatE7, atLngE7 *int64, nowMs int64) (storage.ActivityRow, storage.SessionRow, bool, error)
	ListActivityMembers(ctx context.Context, activityID string) ([]storage.SessionParticipantRow, error)
	RemoveActivityMember(ctx context.Context, activityID, actorUserID, targetUserID string, nowMs int64) error
	ExtendActivity(ctx context.Context, activityID, actorUserID string, newEndAtMs int64, nowMs int64) (storage.ActivityRow, error)
	ListActivitiesForUser(ctx context.Context, userID, status string, nowMs int64, limit int) ([]storage.ActivityRow, error)
	ArchiveExpiredActivitySessions(ctx context.Context, nowMs int64) (int64, error)
	ArchiveActivitySessionIfExpired(ctx context.Context, activityID string, nowMs int64) (bool, error)

	UpsertActivityReminder(ctx context.Context, activityID, userID string, remindAtMs, nowMs int64) (storage.ActivityReminderRow, error)
}

type HandlerOptions struct {
	WeChatAppID                       string
	WeChatAppSecret                   string
	WeChatCallSubscribeTemplateID     string
	WeChatCallSubscribePage           string
	WeChatActivitySubscribeTemplateID string
	WeChatActivitySubscribePage       string
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
	mux.HandleFunc("/v1/burn-messages/", api.handleBurnMessages)
	mux.HandleFunc("/v1/calls", api.handleCalls)
	mux.HandleFunc("/v1/calls/", api.handleCallSubroutes)
	mux.HandleFunc("/v1/wechat/", api.handleWeChat)
	mux.HandleFunc("/v1/session-requests", api.handleSessionRequests)
	mux.HandleFunc("/v1/session-requests/", api.handleSessionRequestSubroutes)
	mux.HandleFunc("/v1/upload", api.handleUpload)
	mux.HandleFunc("/v1/home-base", api.handleHomeBase)
	mux.HandleFunc("/v1/local-feed", api.handleLocalFeed)
	mux.HandleFunc("/v1/local-feed/", api.handleLocalFeed)
	mux.HandleFunc("/v1/activities", api.handleActivities)
	mux.HandleFunc("/v1/activities/", api.handleActivities)
	mux.HandleFunc("/v1/profiles/", api.handleProfiles)
	mux.HandleFunc("/v1/relationship-groups", api.handleRelationshipGroups)
	mux.HandleFunc("/v1/relationship-groups/", api.handleRelationshipGroups)

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
