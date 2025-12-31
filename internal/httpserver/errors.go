package httpserver

import (
	"net/http"
)

type ErrorCode string

const (
	ErrCodeValidation                ErrorCode = "VALIDATION_ERROR"
	ErrCodeUsernameExists            ErrorCode = "USERNAME_EXISTS"
	ErrCodeUserNotFound              ErrorCode = "USER_NOT_FOUND"
	ErrCodeInvalidCredentials        ErrorCode = "INVALID_CREDENTIALS"
	ErrCodeTokenInvalid              ErrorCode = "TOKEN_INVALID"
	ErrCodeTokenExpired              ErrorCode = "TOKEN_EXPIRED"
	ErrCodeSessionNotFound           ErrorCode = "SESSION_NOT_FOUND"
	ErrCodeSessionAccessDenied       ErrorCode = "SESSION_ACCESS_DENIED"
	ErrCodeCannotChatSelf            ErrorCode = "CANNOT_CHAT_SELF"
	ErrCodeCallNotFound              ErrorCode = "CALL_NOT_FOUND"
	ErrCodeCallAccessDenied          ErrorCode = "CALL_ACCESS_DENIED"
	ErrCodeCallInvalidState          ErrorCode = "CALL_INVALID_STATE"
	ErrCodeFriendRequestNotFound     ErrorCode = "FRIEND_REQUEST_NOT_FOUND"
	ErrCodeFriendRequestAccessDenied ErrorCode = "FRIEND_REQUEST_ACCESS_DENIED"
	ErrCodeFriendRequestInvalidState ErrorCode = "FRIEND_REQUEST_INVALID_STATE"
	ErrCodeAlreadyFriends            ErrorCode = "ALREADY_FRIENDS"
	ErrCodeFriendRequestExists       ErrorCode = "FRIEND_REQUEST_EXISTS"
	ErrCodeFriendInviteInvalid       ErrorCode = "FRIEND_INVITE_INVALID"
	ErrCodeWeChatNotConfigured       ErrorCode = "WECHAT_NOT_CONFIGURED"
	ErrCodeWeChatNotBound            ErrorCode = "WECHAT_NOT_BOUND"
	ErrCodeWeChatAPI                 ErrorCode = "WECHAT_API_ERROR"
	ErrCodeInternal                  ErrorCode = "INTERNAL_ERROR"
	ErrCodeMethodNotAllowed          ErrorCode = "METHOD_NOT_ALLOWED"
	ErrCodeNotFound                  ErrorCode = "NOT_FOUND"
)

var errorHTTPStatus = map[ErrorCode]int{
	ErrCodeValidation:                http.StatusBadRequest,
	ErrCodeUsernameExists:            http.StatusConflict,
	ErrCodeUserNotFound:              http.StatusNotFound,
	ErrCodeInvalidCredentials:        http.StatusUnauthorized,
	ErrCodeTokenInvalid:              http.StatusUnauthorized,
	ErrCodeTokenExpired:              http.StatusUnauthorized,
	ErrCodeSessionNotFound:           http.StatusNotFound,
	ErrCodeSessionAccessDenied:       http.StatusForbidden,
	ErrCodeCannotChatSelf:            http.StatusBadRequest,
	ErrCodeCallNotFound:              http.StatusNotFound,
	ErrCodeCallAccessDenied:          http.StatusForbidden,
	ErrCodeCallInvalidState:          http.StatusConflict,
	ErrCodeFriendRequestNotFound:     http.StatusNotFound,
	ErrCodeFriendRequestAccessDenied: http.StatusForbidden,
	ErrCodeFriendRequestInvalidState: http.StatusConflict,
	ErrCodeAlreadyFriends:            http.StatusConflict,
	ErrCodeFriendRequestExists:       http.StatusConflict,
	ErrCodeFriendInviteInvalid:       http.StatusNotFound,
	ErrCodeWeChatNotConfigured:       http.StatusNotImplemented,
	ErrCodeWeChatNotBound:            http.StatusPreconditionFailed,
	ErrCodeWeChatAPI:                 http.StatusBadGateway,
	ErrCodeInternal:                  http.StatusInternalServerError,
	ErrCodeMethodNotAllowed:          http.StatusMethodNotAllowed,
	ErrCodeNotFound:                  http.StatusNotFound,
}

func httpStatusForCode(code ErrorCode) int {
	if status, ok := errorHTTPStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}
