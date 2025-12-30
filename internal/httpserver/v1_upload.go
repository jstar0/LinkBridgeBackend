package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxUploadSize = 50 << 20 // 50MB

type uploadResponse struct {
	URL       string `json:"url"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
}

func (api *v1API) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}

	userID := getUserIDFromContext(r.Context())
	if userID == "" {
		writeAPIError(w, ErrCodeTokenInvalid, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeAPIError(w, ErrCodeValidation, "file too large or invalid form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, ErrCodeValidation, "file is required")
		return
	}
	defer file.Close()

	originalName := header.Filename
	ext := filepath.Ext(originalName)

	// Generate unique filename using timestamp and hash
	hash := sha256.New()
	hash.Write([]byte(fmt.Sprintf("%s-%d-%s", userID, time.Now().UnixNano(), originalName)))
	uniqueName := hex.EncodeToString(hash.Sum(nil))[:16] + ext

	// Create upload directory if not exists
	uploadDir := api.uploadDir
	if uploadDir == "" {
		uploadDir = "./uploads"
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		api.logger.Error("failed to create upload dir", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	// Save file
	destPath := filepath.Join(uploadDir, uniqueName)
	dest, err := os.Create(destPath)
	if err != nil {
		api.logger.Error("failed to create file", "error", err)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}
	defer dest.Close()

	written, err := io.Copy(dest, file)
	if err != nil {
		api.logger.Error("failed to write file", "error", err)
		os.Remove(destPath)
		writeAPIError(w, ErrCodeInternal, "internal error")
		return
	}

	// Return file URL
	fileURL := "/uploads/" + uniqueName

	writeJSON(w, http.StatusOK, uploadResponse{
		URL:       fileURL,
		Name:      sanitizeFilename(originalName),
		SizeBytes: written,
	})
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	if len(name) > 255 {
		ext := filepath.Ext(name)
		name = name[:255-len(ext)] + ext
	}
	return name
}
