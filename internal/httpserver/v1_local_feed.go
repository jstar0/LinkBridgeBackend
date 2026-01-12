package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type localFeedPostImageItem struct {
	URL       string `json:"url"`
	SortOrder int    `json:"sortOrder"`
}

type localFeedPostItem struct {
	ID          string                   `json:"id"`
	UserID      string                   `json:"userId"`
	Text        *string                  `json:"text,omitempty"`
	RadiusM     int                      `json:"radiusM"`
	ExpiresAtMs int64                    `json:"expiresAtMs"`
	IsPinned    bool                     `json:"isPinned"`
	CreatedAtMs int64                    `json:"createdAtMs"`
	UpdatedAtMs int64                    `json:"updatedAtMs"`
	Images      []localFeedPostImageItem `json:"images"`
}

type createLocalFeedPostRequest struct {
	Text        *string  `json:"text,omitempty"`
	ImageURLs   []string `json:"imageUrls,omitempty"`
	RadiusM     *int     `json:"radiusM,omitempty"`
	ExpiresAtMs *int64   `json:"expiresAtMs,omitempty"`
	IsPinned    *bool    `json:"isPinned,omitempty"`
}

type createLocalFeedPostResponse struct {
	Post localFeedPostItem `json:"post"`
}

type listLocalFeedPostsResponse struct {
	Posts []localFeedPostItem `json:"posts"`
}

type localFeedPinItem struct {
	UserID      string  `json:"userId"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
	UpdatedAtMs int64   `json:"updatedAtMs"`
}

type listLocalFeedPinsResponse struct {
	Pins []localFeedPinItem `json:"pins"`
}

func (api *v1API) handleLocalFeed(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/local-feed")
	if rest == "" || rest == "/" {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	rest = strings.TrimPrefix(rest, "/")
	parts := splitPath(rest)
	if len(parts) == 0 {
		writeAPIError(w, ErrCodeNotFound, "not found")
		return
	}

	switch parts[0] {
	case "pins":
		if r.Method != http.MethodGet {
			writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			return
		}
		api.handleListLocalFeedPins(w, r)
	case "posts":
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				api.handleListMyLocalFeedPosts(w, r)
			case http.MethodPost:
				api.handleCreateLocalFeedPost(w, r)
			default:
				writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
			}
			return
		}

		if len(parts) == 3 && parts[2] == "delete" && r.Method == http.MethodPost {
			api.handleDeleteLocalFeedPost(w, r, parts[1])
			return
		}
		writeAPIError(w, ErrCodeNotFound, "not found")
	case "users":
		if len(parts) == 3 && parts[2] == "posts" && r.Method == http.MethodGet {
			api.handleListLocalFeedPostsForUser(w, r, parts[1])
			return
		}
		writeAPIError(w, ErrCodeNotFound, "not found")
	default:
		writeAPIError(w, ErrCodeNotFound, "not found")
	}
}

func (api *v1API) handleCreateLocalFeedPost(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req createLocalFeedPostRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	nowMs := time.Now().UnixMilli()

	expiresAtMs := nowMs + 30*24*60*60*1000
	if req.ExpiresAtMs != nil {
		expiresAtMs = *req.ExpiresAtMs
	}
	if expiresAtMs <= nowMs {
		writeAPIError(w, ErrCodeValidation, "expiresAtMs must be in the future")
		return
	}

	isPinned := false
	if req.IsPinned != nil {
		isPinned = *req.IsPinned
	}

	hasText := req.Text != nil && strings.TrimSpace(*req.Text) != ""
	hasImage := false
	for _, u := range req.ImageURLs {
		if strings.TrimSpace(u) != "" {
			hasImage = true
			break
		}
	}
	if !hasText && !hasImage {
		writeAPIError(w, ErrCodeValidation, "text or imageUrls is required")
		return
	}

	post, images, err := api.store.CreateLocalFeedPost(r.Context(), userID, req.Text, req.ImageURLs, expiresAtMs, isPinned, nowMs)
	if err != nil {
		api.logger.Error("create local feed post failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	item := localFeedPostItemFromStorage(post, images)
	writeJSON(w, http.StatusOK, createLocalFeedPostResponse{Post: item})
}

func (api *v1API) handleDeleteLocalFeedPost(w http.ResponseWriter, r *http.Request, postID string) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	postID = strings.TrimSpace(postID)
	if postID == "" {
		writeAPIError(w, ErrCodeValidation, "postId is required")
		return
	}

	if err := api.store.DeleteLocalFeedPost(r.Context(), userID, postID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAPIError(w, ErrCodeLocalFeedPostNotFound, "post not found")
			return
		}
		api.logger.Error("delete local feed post failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (api *v1API) handleListMyLocalFeedPosts(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	nowMs := time.Now().UnixMilli()
	posts, err := api.store.ListLocalFeedPostsForSource(r.Context(), userID, nil, nil, nowMs, 50)
	if err != nil {
		api.logger.Error("list local feed posts failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]localFeedPostItem, 0, len(posts))
	for _, p := range posts {
		items = append(items, localFeedPostItemFromStorage(p.Post, p.Images))
	}
	writeJSON(w, http.StatusOK, listLocalFeedPostsResponse{Posts: items})
}

func (api *v1API) handleListLocalFeedPostsForUser(w http.ResponseWriter, r *http.Request, userID string) {
	if getUserIDFromContext(r.Context()) == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		writeAPIError(w, ErrCodeValidation, "userId is required")
		return
	}

	var (
		atLatE7 *int64
		atLngE7 *int64
	)
	if latStr := strings.TrimSpace(r.URL.Query().Get("atLat")); latStr != "" {
		lat, err := strconv.ParseFloat(latStr, 64)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid atLat")
			return
		}
		v := floatToE7(lat)
		atLatE7 = &v
	}
	if lngStr := strings.TrimSpace(r.URL.Query().Get("atLng")); lngStr != "" {
		lng, err := strconv.ParseFloat(lngStr, 64)
		if err != nil {
			writeAPIError(w, ErrCodeValidation, "invalid atLng")
			return
		}
		v := floatToE7(lng)
		atLngE7 = &v
	}

	nowMs := time.Now().UnixMilli()
	posts, err := api.store.ListLocalFeedPostsForSource(r.Context(), userID, atLatE7, atLngE7, nowMs, 50)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusOK, listLocalFeedPostsResponse{Posts: nil})
			return
		}
		api.logger.Error("list local feed user posts failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]localFeedPostItem, 0, len(posts))
	for _, p := range posts {
		items = append(items, localFeedPostItemFromStorage(p.Post, p.Images))
	}
	writeJSON(w, http.StatusOK, listLocalFeedPostsResponse{Posts: items})
}

func (api *v1API) handleListLocalFeedPins(w http.ResponseWriter, r *http.Request) {
	if getUserIDFromContext(r.Context()) == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	parseE7 := func(key string) (int64, bool) {
		raw := strings.TrimSpace(r.URL.Query().Get(key))
		if raw == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, false
		}
		return floatToE7(f), true
	}

	minLat, ok := parseE7("minLat")
	if !ok {
		writeAPIError(w, ErrCodeValidation, "minLat is required")
		return
	}
	maxLat, ok := parseE7("maxLat")
	if !ok {
		writeAPIError(w, ErrCodeValidation, "maxLat is required")
		return
	}
	minLng, ok := parseE7("minLng")
	if !ok {
		writeAPIError(w, ErrCodeValidation, "minLng is required")
		return
	}
	maxLng, ok := parseE7("maxLng")
	if !ok {
		writeAPIError(w, ErrCodeValidation, "maxLng is required")
		return
	}
	centerLat, ok := parseE7("centerLat")
	if !ok {
		writeAPIError(w, ErrCodeValidation, "centerLat is required")
		return
	}
	centerLng, ok := parseE7("centerLng")
	if !ok {
		writeAPIError(w, ErrCodeValidation, "centerLng is required")
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	pins, err := api.store.ListLocalFeedPins(r.Context(), minLat, maxLat, minLng, maxLng, centerLat, centerLng, limit)
	if err != nil {
		api.logger.Error("list local feed pins failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	items := make([]localFeedPinItem, 0, len(pins))
	for _, p := range pins {
		items = append(items, localFeedPinItem{
			UserID:      p.UserID,
			Lat:         e7ToFloat(p.LatE7),
			Lng:         e7ToFloat(p.LngE7),
			DisplayName: p.DisplayName,
			AvatarURL:   p.AvatarURL,
			UpdatedAtMs: p.UpdatedAtMs,
		})
	}

	writeJSON(w, http.StatusOK, listLocalFeedPinsResponse{Pins: items})
}

func localFeedPostItemFromStorage(post storage.LocalFeedPostRow, images []storage.LocalFeedPostImageRow) localFeedPostItem {
	var imgItems []localFeedPostImageItem
	for _, img := range images {
		imgItems = append(imgItems, localFeedPostImageItem{
			URL:       img.URL,
			SortOrder: img.SortOrder,
		})
	}
	return localFeedPostItem{
		ID:          post.ID,
		UserID:      post.UserID,
		Text:        post.Text,
		RadiusM:     post.RadiusM,
		ExpiresAtMs: post.ExpiresAtMs,
		IsPinned:    post.IsPinned,
		CreatedAtMs: post.CreatedAtMs,
		UpdatedAtMs: post.UpdatedAtMs,
		Images:      imgItems,
	}
}
