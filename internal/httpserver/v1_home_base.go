package httpserver

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"linkbridge-backend/internal/storage"
)

type homeBaseItem struct {
	Lat            float64 `json:"lat"`
	Lng            float64 `json:"lng"`
	LastUpdatedYMD int     `json:"lastUpdatedYmd"`
	UpdatedAtMs    int64   `json:"updatedAtMs"`
}

type getHomeBaseResponse struct {
	HomeBase *homeBaseItem `json:"homeBase,omitempty"`
}

type upsertHomeBaseRequest struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (api *v1API) handleHomeBase(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.handleGetHomeBase(w, r)
	case http.MethodPut:
		api.handleUpsertHomeBase(w, r)
	default:
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
	}
}

func (api *v1API) handleGetHomeBase(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	hb, err := api.store.GetHomeBase(r.Context(), userID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusOK, getHomeBaseResponse{HomeBase: nil})
			return
		}
		api.logger.Error("get home base failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, getHomeBaseResponse{
		HomeBase: &homeBaseItem{
			Lat:            e7ToFloat(hb.LatE7),
			Lng:            e7ToFloat(hb.LngE7),
			LastUpdatedYMD: hb.LastUpdatedYMD,
			UpdatedAtMs:    hb.UpdatedAtMs,
		},
	})
}

func (api *v1API) handleUpsertHomeBase(w http.ResponseWriter, r *http.Request) {
	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	var req upsertHomeBaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, ErrCodeValidation, "invalid JSON body")
		return
	}

	if math.Abs(req.Lat) > 90 || math.Abs(req.Lng) > 180 {
		writeAPIError(w, ErrCodeValidation, "invalid lat/lng range")
		return
	}

	nowMs := time.Now().UnixMilli()
	hb, err := api.store.UpsertHomeBase(r.Context(), userID, floatToE7(req.Lat), floatToE7(req.Lng), nowMs)
	if err != nil {
		if errors.Is(err, storage.ErrHomeBaseLimited) {
			writeAPIError(w, ErrCodeHomeBaseUpdateLimited, "home base can only be updated 3 times per day (0:00 reset)")
			return
		}
		api.logger.Error("upsert home base failed", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, getHomeBaseResponse{
		HomeBase: &homeBaseItem{
			Lat:            e7ToFloat(hb.LatE7),
			Lng:            e7ToFloat(hb.LngE7),
			LastUpdatedYMD: hb.LastUpdatedYMD,
			UpdatedAtMs:    hb.UpdatedAtMs,
		},
	})
}

func floatToE7(v float64) int64 {
	return int64(math.Round(v * 1e7))
}

func e7ToFloat(v int64) float64 {
	return float64(v) / 1e7
}

func normalizeUserID(raw string) string {
	return strings.TrimSpace(raw)
}
