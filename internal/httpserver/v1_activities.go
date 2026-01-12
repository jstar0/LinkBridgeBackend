package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type activityItem struct {
	ID               string  `json:"id"`
	SessionID        string  `json:"sessionId"`
	CreatorID        string  `json:"creatorId"`
	Title            string  `json:"title"`
	Description      *string `json:"description,omitempty"`
	StartAtMs        *int64  `json:"startAtMs,omitempty"`
	EndAtMs          *int64  `json:"endAtMs,omitempty"`
	SessionStatus    string  `json:"sessionStatus"`
	Expired          bool    `json:"expired"`
	NeedsRenewPrompt bool    `json:"needsRenewPrompt"`
	CreatedAtMs      int64   `json:"createdAtMs"`
	UpdatedAtMs      int64   `json:"updatedAtMs"`
}

type createActivityRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description,omitempty"`
	StartAtMs   *int64  `json:"startAtMs,omitempty"`
	EndAtMs     *int64  `json:"endAtMs,omitempty"`
}

type createActivityResponse struct {
	Activity   activityItem `json:"activity"`
	InviteCode string       `json:"inviteCode"`
}

type getActivityResponse struct {
	Activity activityItem `json:"activity"`
}

type listActivitiesResponse struct {
	Activities []activityItem `json:"activities"`
}

type consumeActivityInviteRequest struct {
	Code  string   `json:"code"`
	AtLat *float64 `json:"atLat,omitempty"`
	AtLng *float64 `json:"atLng,omitempty"`
}

type consumeActivityInviteResponse struct {
	Activity activityItem `json:"activity"`
	Joined   bool         `json:"joined"`
}

type listActivityMembersResponse struct {
	Members []activityMemberItem `json:"members"`
}

type activityMemberItem struct {
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
	Role        string  `json:"role"`
	Status      string  `json:"status"`
	CreatedAtMs int64   `json:"createdAtMs"`
	UpdatedAtMs int64   `json:"updatedAtMs"`
}

type extendActivityRequest struct {
	EndAtMs int64 `json:"endAtMs"`
}

type upsertActivityReminderRequest struct {
	RemindAtMs *int64 `json:"remindAtMs,omitempty"`
}

type activityReminderItem struct {
	ActivityID  string  `json:"activityId"`
	UserID      string  `json:"userId"`
	RemindAtMs  int64   `json:"remindAtMs"`
	Status      string  `json:"status"`
	LastError   *string `json:"lastError,omitempty"`
	SentAtMs    *int64  `json:"sentAtMs,omitempty"`
	CreatedAtMs int64   `json:"createdAtMs"`
	UpdatedAtMs int64   `json:"updatedAtMs"`
}

type upsertActivityReminderResponse struct {
	Reminder activityReminderItem `json:"reminder"`
}

func (api *v1API) handleActivities(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/v1/activities")
	if rest == "" || rest == "/" {
		switch r.Method {
		case http.MethodGet:
			api.handleListActivities(w, r, userID)
		case http.MethodPost:
			api.handleCreateActivity(w, r, userID)
		default:
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		}
		return
	}

	rest = strings.TrimPrefix(rest, "/")
	parts := splitPath(rest)
	if len(parts) == 0 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	// POST /v1/activities/invites/consume
	if len(parts) == 2 && parts[0] == "invites" && parts[1] == "consume" {
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleConsumeActivityInvite(w, r, userID)
		return
	}

	activityID := strings.TrimSpace(parts[0])
	if activityID == "" {
		writeAPIError(w, ErrCodeValidation, "activityId is required")
		return
	}

	// GET /v1/activities/{id}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleGetActivity(w, r, userID, activityID)
		return
	}

	// GET /v1/activities/{id}/members
	if len(parts) == 2 && parts[1] == "members" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleListActivityMembers(w, r, userID, activityID)
		return
	}

	// POST /v1/activities/{id}/reminders
	if len(parts) == 2 && parts[1] == "reminders" {
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleUpsertActivityReminder(w, r, userID, activityID)
		return
	}

	// POST /v1/activities/{id}/members/{userId}/remove
	if len(parts) == 4 && parts[1] == "members" && parts[3] == "remove" {
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		targetUserID := strings.TrimSpace(parts[2])
		api.handleRemoveActivityMember(w, r, userID, activityID, targetUserID)
		return
	}

	// POST /v1/activities/{id}/extend
	if len(parts) == 2 && parts[1] == "extend" {
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleExtendActivity(w, r, userID, activityID)
		return
	}

	writeAPIError(w, ErrCodeNotFound, "not found")
}

func (api *v1API) handleCreateActivity(w http.ResponseWriter, r *http.Request, userID string) {
	var req createActivityRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeAPIError(w, ErrCodeValidation, "title is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	activity, invite, err := api.store.CreateActivity(r.Context(), userID, title, req.Description, req.StartAtMs, req.EndAtMs, nowMs)
	if err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid activity fields")
		return
	}

	api.handleGetActivityWithInvite(w, r, userID, activity.ID, &invite.Code)
}

func (api *v1API) handleListActivities(w http.ResponseWriter, r *http.Request, userID string) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != storage.SessionStatusActive && status != storage.SessionStatusArchived {
		status = storage.SessionStatusActive
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	nowMs := time.Now().UnixMilli()
	if status == storage.SessionStatusActive {
		// Best-effort: ensure ended activities are archived before listing.
		_, _ = api.store.ArchiveExpiredActivitySessions(r.Context(), nowMs)
	}

	activities, err := api.store.ListActivitiesForUser(r.Context(), userID, status, nowMs, limit)
	if err != nil {
		api.logger.Error("list activities failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]activityItem, 0, len(activities))
	for _, a := range activities {
		sess, err := api.store.GetSessionByID(r.Context(), a.SessionID)
		if err != nil {
			continue
		}
		items = append(items, activityItemFromRows(a, sess, userID, nowMs))
	}

	writeJSON(w, http.StatusOK, listActivitiesResponse{Activities: items})
}

func (api *v1API) handleGetActivity(w http.ResponseWriter, r *http.Request, userID, activityID string) {
	api.handleGetActivityWithInvite(w, r, userID, activityID, nil)
}

func (api *v1API) handleGetActivityWithInvite(w http.ResponseWriter, r *http.Request, userID, activityID string, inviteCode *string) {
	nowMs := time.Now().UnixMilli()
	_, _ = api.store.ArchiveActivitySessionIfExpired(r.Context(), activityID, nowMs)

	activity, err := api.store.GetActivityByID(r.Context(), activityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	sess, err := api.store.GetSessionByID(r.Context(), activity.SessionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity session failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	ok, err := api.store.IsSessionParticipant(r.Context(), sess.ID, userID)
	if err != nil {
		api.logger.Error("check activity participant failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
		return
	}

	item := activityItemFromRows(activity, sess, userID, nowMs)

	if inviteCode != nil {
		writeJSON(w, http.StatusOK, createActivityResponse{Activity: item, InviteCode: *inviteCode})
		return
	}

	writeJSON(w, http.StatusOK, getActivityResponse{Activity: item})
}

func (api *v1API) handleConsumeActivityInvite(w http.ResponseWriter, r *http.Request, userID string) {
	var req consumeActivityInviteRequest
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
	activity, session, joined, err := api.store.ConsumeActivityInvite(r.Context(), userID, req.Code, atLatE7, atLngE7, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrInviteInvalid) {
			writeAPIError(w, ErrCodeActivityInviteInvalid, "invalid invite")
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
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		if errors.Is(err, storage.ErrInvalidState) {
			writeAPIError(w, ErrCodeActivityInvalidState, "activity ended")
			return
		}
		if errors.Is(err, storage.ErrSessionArchived) {
			writeAPIError(w, ErrCodeSessionArchived, "session is archived")
			return
		}
		api.logger.Error("consume activity invite failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, consumeActivityInviteResponse{
		Activity: activityItemFromRows(activity, session, userID, nowMs),
		Joined:   joined,
	})
}

func (api *v1API) handleListActivityMembers(w http.ResponseWriter, r *http.Request, userID, activityID string) {
	nowMs := time.Now().UnixMilli()
	_, _ = api.store.ArchiveActivitySessionIfExpired(r.Context(), activityID, nowMs)

	activity, err := api.store.GetActivityByID(r.Context(), activityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	ok, err := api.store.IsSessionParticipant(r.Context(), activity.SessionID, userID)
	if err != nil {
		api.logger.Error("check activity participant failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
		return
	}

	members, err := api.store.ListActivityMembers(r.Context(), activityID)
	if err != nil {
		api.logger.Error("list activity members failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]activityMemberItem, 0, len(members))
	for _, m := range members {
		u, err := api.store.GetUserByID(r.Context(), m.UserID)
		if err != nil {
			continue
		}
		items = append(items, activityMemberItem{
			UserID:      u.ID,
			DisplayName: u.DisplayName,
			AvatarURL:   u.AvatarURL,
			Role:        m.Role,
			Status:      m.Status,
			CreatedAtMs: m.CreatedAtMs,
			UpdatedAtMs: m.UpdatedAtMs,
		})
	}

	writeJSON(w, http.StatusOK, listActivityMembersResponse{Members: items})
}

func (api *v1API) handleRemoveActivityMember(w http.ResponseWriter, r *http.Request, userID, activityID, targetUserID string) {
	targetUserID = strings.TrimSpace(targetUserID)
	if targetUserID == "" {
		writeAPIError(w, ErrCodeValidation, "target userId is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	if err := api.store.RemoveActivityMember(r.Context(), activityID, userID, targetUserID, nowMs); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity/member not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
			return
		}
		api.logger.Error("remove activity member failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (api *v1API) handleExtendActivity(w http.ResponseWriter, r *http.Request, userID, activityID string) {
	var req extendActivityRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}
	if req.EndAtMs <= 0 {
		writeAPIError(w, ErrCodeValidation, "endAtMs is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	activity, err := api.store.ExtendActivity(r.Context(), activityID, userID, req.EndAtMs, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		if errors.Is(err, storage.ErrAccessDenied) {
			writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
			return
		}
		writeAPIError(w, ErrCodeValidation, "invalid endAtMs")
		return
	}

	sess, err := api.store.GetSessionByID(r.Context(), activity.SessionID)
	if err != nil {
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, getActivityResponse{
		Activity: activityItemFromRows(activity, sess, userID, nowMs),
	})
}

func activityItemFromRows(a storage.ActivityRow, sess storage.SessionRow, viewerID string, nowMs int64) activityItem {
	expired := a.EndAtMs != nil && nowMs > *a.EndAtMs
	return activityItem{
		ID:               a.ID,
		SessionID:        a.SessionID,
		CreatorID:        a.CreatorID,
		Title:            a.Title,
		Description:      a.Description,
		StartAtMs:        a.StartAtMs,
		EndAtMs:          a.EndAtMs,
		SessionStatus:    sess.Status,
		Expired:          expired,
		NeedsRenewPrompt: expired && viewerID == a.CreatorID,
		CreatedAtMs:      a.CreatedAtMs,
		UpdatedAtMs:      a.UpdatedAtMs,
	}
}

func activityReminderItemFromRow(row storage.ActivityReminderRow) activityReminderItem {
	return activityReminderItem{
		ActivityID:  row.ActivityID,
		UserID:      row.UserID,
		RemindAtMs:  row.RemindAtMs,
		Status:      row.Status,
		LastError:   row.LastError,
		SentAtMs:    row.SentAtMs,
		CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs,
	}
}

func (api *v1API) handleUpsertActivityReminder(w http.ResponseWriter, r *http.Request, userID, activityID string) {
	var req upsertActivityReminderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	nowMs := time.Now().UnixMilli()
	_, _ = api.store.ArchiveActivitySessionIfExpired(r.Context(), activityID, nowMs)

	activity, err := api.store.GetActivityByID(r.Context(), activityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeActivityNotFound, "activity not found")
			return
		}
		api.logger.Error("get activity failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	ok, err := api.store.IsSessionParticipant(r.Context(), activity.SessionID, userID)
	if err != nil {
		api.logger.Error("check activity participant failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	if !ok {
		writeAPIError(w, ErrCodeActivityAccessDenied, "access denied")
		return
	}

	var remindAtMs int64
	if req.RemindAtMs != nil && *req.RemindAtMs > 0 {
		remindAtMs = *req.RemindAtMs
	} else if activity.StartAtMs != nil && *activity.StartAtMs > 0 {
		remindAtMs = *activity.StartAtMs
	} else if activity.EndAtMs != nil && *activity.EndAtMs > 0 {
		remindAtMs = *activity.EndAtMs
	}

	if remindAtMs <= 0 {
		writeAPIError(w, ErrCodeValidation, "remindAtMs is required (activity has no start/end time)")
		return
	}
	if remindAtMs <= nowMs {
		writeAPIError(w, ErrCodeValidation, "remindAtMs must be in the future")
		return
	}
	if activity.EndAtMs != nil && *activity.EndAtMs > 0 && remindAtMs > *activity.EndAtMs {
		writeAPIError(w, ErrCodeValidation, "remindAtMs must be <= endAtMs")
		return
	}

	row, err := api.store.UpsertActivityReminder(r.Context(), activityID, userID, remindAtMs, nowMs)
	if err != nil {
		api.logger.Error("upsert activity reminder failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, upsertActivityReminderResponse{Reminder: activityReminderItemFromRow(row)})
}
