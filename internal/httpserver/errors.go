package httpserver

import (
	"net/http"
)

type ErrorCode string

const (
	ErrCodeValidation                 ErrorCode = "VALIDATION_ERROR"
	ErrCodeUsernameExists             ErrorCode = "USERNAME_EXISTS"
	ErrCodeUserNotFound               ErrorCode = "USER_NOT_FOUND"
	ErrCodeInvalidCredentials         ErrorCode = "INVALID_CREDENTIALS"
	ErrCodeTokenInvalid               ErrorCode = "TOKEN_INVALID"
	ErrCodeTokenExpired               ErrorCode = "TOKEN_EXPIRED"
	ErrCodeInviteExpired              ErrorCode = "INVITE_EXPIRED"
	ErrCodeGeoFenceRequired           ErrorCode = "GEOFENCE_REQUIRED"
	ErrCodeGeoFenceForbidden          ErrorCode = "GEOFENCE_FORBIDDEN"
	ErrCodeMessageNotFound            ErrorCode = "MESSAGE_NOT_FOUND"
	ErrCodeSessionNotFound            ErrorCode = "SESSION_NOT_FOUND"
	ErrCodeSessionAccessDenied        ErrorCode = "SESSION_ACCESS_DENIED"
	ErrCodeSessionArchived            ErrorCode = "SESSION_ARCHIVED"
	ErrCodeSessionExists              ErrorCode = "SESSION_EXISTS"
	ErrCodeCannotChatSelf             ErrorCode = "CANNOT_CHAT_SELF"
	ErrCodeCallNotFound               ErrorCode = "CALL_NOT_FOUND"
	ErrCodeCallAccessDenied           ErrorCode = "CALL_ACCESS_DENIED"
	ErrCodeCallInvalidState           ErrorCode = "CALL_INVALID_STATE"
	ErrCodeSessionRequestNotFound     ErrorCode = "SESSION_REQUEST_NOT_FOUND"
	ErrCodeSessionRequestAccessDenied ErrorCode = "SESSION_REQUEST_ACCESS_DENIED"
	ErrCodeSessionRequestInvalidState ErrorCode = "SESSION_REQUEST_INVALID_STATE"
	ErrCodeSessionRequestExists       ErrorCode = "SESSION_REQUEST_EXISTS"
	ErrCodeSessionInviteInvalid       ErrorCode = "SESSION_INVITE_INVALID"
	ErrCodeActivityNotFound           ErrorCode = "ACTIVITY_NOT_FOUND"
	ErrCodeActivityAccessDenied       ErrorCode = "ACTIVITY_ACCESS_DENIED"
	ErrCodeActivityInvalidState       ErrorCode = "ACTIVITY_INVALID_STATE"
	ErrCodeActivityInviteInvalid      ErrorCode = "ACTIVITY_INVITE_INVALID"
	ErrCodeRateLimited                ErrorCode = "RATE_LIMITED"
	ErrCodeCooldownActive             ErrorCode = "COOLDOWN_ACTIVE"
	ErrCodeHomeBaseUpdateLimited      ErrorCode = "HOME_BASE_UPDATE_LIMITED"
	ErrCodeLocalFeedPostNotFound      ErrorCode = "LOCAL_FEED_POST_NOT_FOUND"
	ErrCodeWeChatNotConfigured        ErrorCode = "WECHAT_NOT_CONFIGURED"
	ErrCodeWeChatNotBound             ErrorCode = "WECHAT_NOT_BOUND"
	ErrCodeWeChatAPI                  ErrorCode = "WECHAT_API_ERROR"
	ErrCodeInternal                   ErrorCode = "INTERNAL_ERROR"
	ErrCodeMethodNotAllowed           ErrorCode = "METHOD_NOT_ALLOWED"
	ErrCodeNotFound                   ErrorCode = "NOT_FOUND"
)

var errorHTTPStatus = map[ErrorCode]int{
	ErrCodeValidation:                 http.StatusBadRequest,
	ErrCodeUsernameExists:             http.StatusConflict,
	ErrCodeUserNotFound:               http.StatusNotFound,
	ErrCodeInvalidCredentials:         http.StatusUnauthorized,
	ErrCodeTokenInvalid:               http.StatusUnauthorized,
	ErrCodeTokenExpired:               http.StatusUnauthorized,
	ErrCodeInviteExpired:              http.StatusGone,
	ErrCodeGeoFenceRequired:           http.StatusBadRequest,
	ErrCodeGeoFenceForbidden:          http.StatusForbidden,
	ErrCodeMessageNotFound:            http.StatusNotFound,
	ErrCodeSessionNotFound:            http.StatusNotFound,
	ErrCodeSessionAccessDenied:        http.StatusForbidden,
	ErrCodeSessionArchived:            http.StatusForbidden,
	ErrCodeSessionExists:              http.StatusConflict,
	ErrCodeCannotChatSelf:             http.StatusBadRequest,
	ErrCodeCallNotFound:               http.StatusNotFound,
	ErrCodeCallAccessDenied:           http.StatusForbidden,
	ErrCodeCallInvalidState:           http.StatusConflict,
	ErrCodeSessionRequestNotFound:     http.StatusNotFound,
	ErrCodeSessionRequestAccessDenied: http.StatusForbidden,
	ErrCodeSessionRequestInvalidState: http.StatusConflict,
	ErrCodeSessionRequestExists:       http.StatusConflict,
	ErrCodeSessionInviteInvalid:       http.StatusNotFound,
	ErrCodeActivityNotFound:           http.StatusNotFound,
	ErrCodeActivityAccessDenied:       http.StatusForbidden,
	ErrCodeActivityInvalidState:       http.StatusConflict,
	ErrCodeActivityInviteInvalid:      http.StatusNotFound,
	ErrCodeRateLimited:                http.StatusTooManyRequests,
	ErrCodeCooldownActive:             http.StatusTooManyRequests,
	ErrCodeHomeBaseUpdateLimited:      http.StatusTooManyRequests,
	ErrCodeLocalFeedPostNotFound:      http.StatusNotFound,
	ErrCodeWeChatNotConfigured:        http.StatusNotImplemented,
	ErrCodeWeChatNotBound:             http.StatusPreconditionFailed,
	ErrCodeWeChatAPI:                  http.StatusBadGateway,
	ErrCodeInternal:                   http.StatusInternalServerError,
	ErrCodeMethodNotAllowed:           http.StatusMethodNotAllowed,
	ErrCodeNotFound:                   http.StatusNotFound,
}

func httpStatusForCode(code ErrorCode) int {
	if status, ok := errorHTTPStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}
