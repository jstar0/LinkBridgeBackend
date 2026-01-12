package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type searchUsersResponse struct {
	Users []userItem `json:"users"`
	Hint  string     `json:"hint,omitempty"`
}

type getUserResponse struct {
	User userItem `json:"user"`
}

type updateMeRequest struct {
	DisplayName *string `json:"displayName,omitempty"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

type updateMeResponse struct {
	User userItem `json:"user"`
}

func (api *v1API) handleUsers(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/users")
	if rest == "" || rest == "/" {
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleSearchUsers(w, r)
		return
	}

	if rest == "/me" {
		if r.Method == http.MethodPut {
			api.handleUpdateMe(w, r)
			return
		}
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	if strings.HasPrefix(rest, "/") {
		userID := strings.TrimPrefix(rest, "/")
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleGetUser(w, r, userID)
		return
	}

	writeAPIError(w, ErrCodeNotFound, "not found")
}

func (api *v1API) handleSearchUsers(w http.ResponseWriter, r *http.Request) {
	currentUserID := getUserIDFromContext(r.Context())
	if currentUserID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeAPIError(w, ErrCodeValidation, "query parameter 'q' is required")
		return
	}

	users, err := api.store.SearchUsers(r.Context(), query, 20)
	if err != nil {
		api.logger.Error("search users failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]userItem, 0, len(users))
	for _, u := range users {
		if u.ID == currentUserID {
			continue
		}
		items = append(items, userItem{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			AvatarURL:   u.AvatarURL,
		})
	}

	resp := searchUsersResponse{Users: items}
	if len(items) == 0 {
		resp.Hint = "未找到匹配的用户"
	}

	writeJSON(w, http.StatusOK, resp)
}

func (api *v1API) handleGetUser(w http.ResponseWriter, r *http.Request, userID string) {
	currentUserID := getUserIDFromContext(r.Context())
	if currentUserID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		writeAPIError(w, ErrCodeValidation, "user ID is required")
		return
	}

	user, err := api.store.GetUserByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeUserNotFound, "user not found")
			return
		}
		api.logger.Error("get user failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, getUserResponse{
		User: userItem{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
		},
	})
}

func (api *v1API) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	currentUserID := getUserIDFromContext(r.Context())
	if currentUserID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req updateMeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	var (
		updateDisplayName bool
		displayName       string
		updateAvatar      bool
		avatarURL         *string
	)

	if req.DisplayName != nil {
		updateDisplayName = true
		displayName = strings.TrimSpace(*req.DisplayName)
		if displayName == "" {
			writeAPIError(w, ErrCodeValidation, "displayName is required")
			return
		}
		if len(displayName) > 20 {
			writeAPIError(w, ErrCodeValidation, "displayName must be at most 20 characters")
			return
		}
	}

	if req.AvatarURL != nil {
		updateAvatar = true
		trimmed := strings.TrimSpace(*req.AvatarURL)
		if trimmed != "" {
			avatarURL = &trimmed
		}
	}

	if !updateDisplayName && !updateAvatar {
		writeAPIError(w, ErrCodeValidation, "displayName or avatarUrl is required")
		return
	}

	nowMs := time.Now().UnixMilli()
	user, err := api.store.GetUserByID(r.Context(), currentUserID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeUserNotFound, "user not found")
			return
		}
		api.logger.Error("get current user failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	if updateDisplayName {
		user, err = api.store.UpdateUserDisplayName(r.Context(), currentUserID, displayName, nowMs)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeAPIError(w, ErrCodeUserNotFound, "user not found")
				return
			}
			api.logger.Error("update user display name failed", "error", err)
			writeAPIError(w, ErrCodeInternal, "internal error")
			return
		}
	}

	if updateAvatar {
		user, err = api.store.UpdateUserAvatarURL(r.Context(), currentUserID, avatarURL, nowMs)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeAPIError(w, ErrCodeUserNotFound, "user not found")
				return
			}
			api.logger.Error("update user avatar failed", "error", err)
			writeAPIError(w, ErrCodeInternal, "internal error")
			return
		}
	}

	writeJSON(w, http.StatusOK, updateMeResponse{
		User: userItem{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
		},
	})
}
