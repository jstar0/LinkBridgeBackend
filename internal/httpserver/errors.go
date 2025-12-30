package httpserver

import (
	"net/http"
)

type ErrorCode string

const (
	ErrCodeValidation        ErrorCode = "VALIDATION_ERROR"
	ErrCodeUsernameExists    ErrorCode = "USERNAME_EXISTS"
	ErrCodeUserNotFound      ErrorCode = "USER_NOT_FOUND"
	ErrCodeInvalidCredentials ErrorCode = "INVALID_CREDENTIALS"
	ErrCodeTokenInvalid      ErrorCode = "TOKEN_INVALID"
	ErrCodeTokenExpired      ErrorCode = "TOKEN_EXPIRED"
	ErrCodeSessionNotFound   ErrorCode = "SESSION_NOT_FOUND"
	ErrCodeSessionAccessDenied ErrorCode = "SESSION_ACCESS_DENIED"
	ErrCodeCannotChatSelf    ErrorCode = "CANNOT_CHAT_SELF"
	ErrCodeInternal          ErrorCode = "INTERNAL_ERROR"
	ErrCodeMethodNotAllowed  ErrorCode = "METHOD_NOT_ALLOWED"
	ErrCodeNotFound          ErrorCode = "NOT_FOUND"
)

var errorHTTPStatus = map[ErrorCode]int{
	ErrCodeValidation:         http.StatusBadRequest,
	ErrCodeUsernameExists:     http.StatusConflict,
	ErrCodeUserNotFound:       http.StatusNotFound,
	ErrCodeInvalidCredentials: http.StatusUnauthorized,
	ErrCodeTokenInvalid:       http.StatusUnauthorized,
	ErrCodeTokenExpired:       http.StatusUnauthorized,
	ErrCodeSessionNotFound:    http.StatusNotFound,
	ErrCodeSessionAccessDenied: http.StatusForbidden,
	ErrCodeCannotChatSelf:     http.StatusBadRequest,
	ErrCodeInternal:           http.StatusInternalServerError,
	ErrCodeMethodNotAllowed:   http.StatusMethodNotAllowed,
	ErrCodeNotFound:           http.StatusNotFound,
}

func httpStatusForCode(code ErrorCode) int {
	if status, ok := errorHTTPStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}
