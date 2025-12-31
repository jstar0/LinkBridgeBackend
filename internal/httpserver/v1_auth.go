package httpserver

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"linkbridge-backend/internal/storage"
)

const tokenDuration = 7 * 24 * time.Hour

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{4,20}$`)

type registerRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	User      userItem `json:"user"`
	Token     string   `json:"token"`
	ExpiresAt int64    `json:"expiresAt"`
}

type userItem struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
}

type meResponse struct {
	User userItem `json:"user"`
}

type logoutResponse struct {
	Success bool `json:"success"`
}

func (api *v1API) handleAuth(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/auth/")
	switch rest {
	case "register":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleRegister(w, r)
	case "login":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleLogin(w, r)
	case "logout":
		if r.Method != http.MethodPost {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleLogout(w, r)
	case "me":
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleMe(w, r)
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)

	if !usernameRegex.MatchString(req.Username) {
		writeAPIError(w, ErrCodeValidation, "username must be 4-20 characters, alphanumeric and underscore only")
		return
	}

	if err := validatePassword(req.Password); err != nil {
		writeAPIError(w, ErrCodeValidation, err.Error())
		return
	}

	if len(req.DisplayName) == 0 || len(req.DisplayName) > 20 {
		writeAPIError(w, ErrCodeValidation, "displayName must be 1-20 characters")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		api.logger.Error("bcrypt hash failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	nowMs := time.Now().UnixMilli()
	user, err := api.store.CreateUser(r.Context(), req.Username, string(passwordHash), req.DisplayName, nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrUsernameExists) {
			writeAPIError(w, ErrCodeUsernameExists, "username already exists")
			return
		}
		api.logger.Error("create user failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	expiresAtMs := nowMs + tokenDuration.Milliseconds()
	tokenRow, err := api.store.CreateAuthToken(r.Context(), user.ID, nil, nowMs, expiresAtMs)
	if err != nil {
		api.logger.Error("create token failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		User: userItem{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
		},
		Token:     tokenRow.Token,
		ExpiresAt: tokenRow.ExpiresAtMs,
	})
}

func (api *v1API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeAPIError(w, ErrCodeValidation, "username and password are required")
		return
	}

	user, err := api.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeInvalidCredentials, "invalid username or password")
			return
		}
		api.logger.Error("get user failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeAPIError(w, ErrCodeInvalidCredentials, "invalid username or password")
		return
	}

	nowMs := time.Now().UnixMilli()
	expiresAtMs := nowMs + tokenDuration.Milliseconds()
	tokenRow, err := api.store.CreateAuthToken(r.Context(), user.ID, nil, nowMs, expiresAtMs)
	if err != nil {
		api.logger.Error("create token failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		User: userItem{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
		},
		Token:     tokenRow.Token,
		ExpiresAt: tokenRow.ExpiresAtMs,
	})
}

func (api *v1API) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "token required")
		return
	}

	_ = api.store.DeleteToken(r.Context(), token)
	writeJSON(w, http.StatusOK, logoutResponse{Success: true})
}

func (api *v1API) handleMe(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
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

	writeJSON(w, http.StatusOK, meResponse{
		User: userItem{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
		},
	})
}

func validatePassword(password string) error {
	if len(password) < 8 || len(password) > 32 {
		return errors.New("password must be 8-32 characters")
	}

	var hasUpper, hasLower, hasDigit bool
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsDigit(c):
			hasDigit = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit {
		return errors.New("password must contain uppercase, lowercase, and digit")
	}

	return nil
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Allow token via query param for endpoints used by <image> tags, where setting headers is awkward.
	if r != nil {
		if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
			return token
		}
	}
	return ""
}
